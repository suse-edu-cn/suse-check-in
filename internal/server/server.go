package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"suse-checkin/internal/checkin"
	"suse-checkin/internal/config"
	"suse-checkin/internal/locations"
	"suse-checkin/internal/oaa"
	"suse-checkin/internal/scheduler"
	"suse-checkin/internal/store"
	"suse-checkin/internal/version"
	"suse-checkin/internal/wechat"
	webassets "suse-checkin/web"
)

var (
	errUnsupportedMedia = errors.New("仅支持 application/json")
	sampleDateRe        = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	sampleFileRe        = regexp.MustCompile(`^[A-Za-z0-9._-]+\.(jpg|jpeg|png|gif)$`)
	deviceRe            = regexp.MustCompile(`^[A-Za-z0-9._-]{1,20}$`)
)

type WechatLoginService interface {
	Start(ctx context.Context, ownerID uint64) (wechat.LoginView, error)
	Status(id string, ownerID uint64) (wechat.LoginView, error)
	Complete(id string, ownerID uint64, in wechat.CompleteInput) (store.AccountView, error)
	Cancel(id string, ownerID uint64)
}

type Server struct {
	cfg                    config.Config
	st                     *store.Store
	svc                    *checkin.Service
	sched                  *scheduler.Scheduler
	auth                   *oaa.Client
	wechat                 WechatLoginService
	notificationsAvailable bool
	logger                 *log.Logger
	loc                    *time.Location
	handler                http.Handler
	static                 http.Handler
	index                  []byte
}

func New(cfg config.Config, st *store.Store, svc *checkin.Service, sched *scheduler.Scheduler, auth *oaa.Client, wechatSvc WechatLoginService, notificationsAvailable bool, logger *log.Logger) (*Server, error) {
	if auth == nil {
		return nil, errors.New("OAA 认证客户端不能为空")
	}
	if wechatSvc == nil {
		return nil, errors.New("微信扫码登录服务不能为空")
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("加载时区: %w", err)
	}
	index, err := webassets.FS.ReadFile("index.html")
	if err != nil {
		return nil, fmt.Errorf("读取内嵌 index.html: %w", err)
	}
	staticFS, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		return nil, fmt.Errorf("读取内嵌静态资源: %w", err)
	}
	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	s := &Server{
		cfg:                    cfg,
		st:                     st,
		svc:                    svc,
		sched:                  sched,
		auth:                   auth,
		wechat:                 wechatSvc,
		notificationsAvailable: notificationsAvailable,
		logger:                 logger,
		loc:                    loc,
		static:                 staticHandler,
		index:                  index,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/wechat/login", s.requireAuth(s.handleWechatStart))
	mux.HandleFunc("GET /api/wechat/login/{id}", s.requireAuth(s.handleWechatStatus))
	mux.HandleFunc("POST /api/wechat/login/{id}/complete", s.requireAuth(s.handleWechatComplete))
	mux.HandleFunc("DELETE /api/wechat/login/{id}", s.requireAuth(s.handleWechatCancel))
	mux.HandleFunc("GET /api/state", s.requireAuth(s.handleState))
	mux.HandleFunc("POST /api/accounts", s.requireAuth(s.handleCreateAccount))
	mux.HandleFunc("PUT /api/accounts/{id}", s.requireAuth(s.handleUpdateAccount))
	mux.HandleFunc("DELETE /api/accounts/{id}", s.requireAuth(s.handleDeleteAccount))
	mux.HandleFunc("PUT /api/schedule", s.requireAuth(s.handleSaveSchedule))
	mux.HandleFunc("POST /api/run", s.requireAuth(s.handleRun))
	mux.HandleFunc("GET /api/samples/{date}/{file}", s.requireAuth(s.handleSample))
	mux.Handle("GET /static/", s.static)
	mux.HandleFunc("GET /", s.handleIndex)
	s.handler = s.securityHeaders(mux)
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

type accountRequest struct {
	StudentID string          `json:"studentId"`
	Password  string          `json:"password"`
	Name      string          `json:"name"`
	Campus    string          `json:"campus"`
	LoginType store.LoginType `json:"loginType"`
}

type scheduleRequest struct {
	Enabled              bool    `json:"enabled"`
	TimeOfDay            string  `json:"timeOfDay"`
	AccountIDs           []int64 `json:"accountIds"`
	MaxRetries           int     `json:"maxRetries"`
	RetryIntervalMinutes int     `json:"retryIntervalMinutes"`
	NotifyEnabled        bool    `json:"notifyEnabled"`
	NotifyEmail          string  `json:"notifyEmail"`
}

type runRequest struct {
	AccountIDs []int64 `json:"accountIds"`
}

type stateResponse struct {
	Accounts               []store.AccountView `json:"accounts"`
	Campuses               []locations.Summary `json:"campuses"`
	Schedule               store.Schedule      `json:"schedule"`
	Scheduler              scheduler.Status    `json:"scheduler"`
	Progress               checkin.Progress    `json:"progress"`
	Runs                   []store.Run         `json:"runs"`
	ServerTime             time.Time           `json:"serverTime"`
	Timezone               string              `json:"timezone"`
	NotificationsAvailable bool                `json:"notificationsAvailable"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version.Version})
}

type loginRequest struct {
	Account  string `json:"account"`
	Password string `json:"password"`
	Device   string `json:"device"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
	Device       string `json:"device"`
}

type authResponse struct {
	Token        string   `json:"token"`
	RefreshToken string   `json:"refresh_token"`
	User         oaa.User `json:"user"`
}

type meResponse struct {
	User oaa.User `json:"user"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	req.Account = strings.TrimSpace(req.Account)
	req.Device = strings.TrimSpace(req.Device)
	if req.Account == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "账号和密码不能为空")
		return
	}
	if !deviceRe.MatchString(req.Device) {
		writeError(w, http.StatusBadRequest, "设备标识格式不正确")
		return
	}

	ctx, cancel := s.oaaContext(r)
	defer cancel()
	tokens, err := s.auth.Login(ctx, req.Account, req.Password, req.Device)
	if err != nil {
		s.writeLoginError(w, err)
		return
	}
	user, err := s.auth.Validate(ctx, tokens.Token)
	if err != nil {
		s.logger.Printf("[server] OAA 登录后获取用户信息失败: %v", err)
		writeError(w, http.StatusBadGateway, "登录成功但读取账号信息失败，请重试")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{
		Token:        tokens.Token,
		RefreshToken: tokens.RefreshToken,
		User:         user,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	if req.RefreshToken == "" {
		writeError(w, http.StatusUnauthorized, "登录已失效，请重新登录")
		return
	}

	ctx, cancel := s.oaaContext(r)
	defer cancel()
	tokens, err := s.auth.Refresh(ctx, req.RefreshToken)
	if err != nil {
		s.writeRefreshError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	req.Device = strings.TrimSpace(req.Device)
	if !deviceRe.MatchString(req.Device) {
		writeError(w, http.StatusBadRequest, "设备标识格式不正确")
		return
	}
	if req.RefreshToken != "" {
		ctx, cancel := s.oaaContext(r)
		tokens, err := s.auth.Refresh(ctx, req.RefreshToken)
		if err == nil {
			s.auth.Invalidate(tokens.Token)
			if err := s.auth.Logout(ctx, tokens.Token, req.Device); err != nil {
				s.logger.Printf("[server] OAA 登出失败: %v", err)
			}
		} else {
			s.logger.Printf("[server] OAA 登出前刷新令牌失败: %v", err)
		}
		cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{User: user})
}

type wechatCompleteRequest struct {
	AccountID int64  `json:"accountId"`
	Name      string `json:"name"`
	Campus    string `json:"campus"`
}

func (s *Server) handleWechatStart(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	ctx, cancel := s.oaaContext(r)
	defer cancel()
	view, err := s.wechat.Start(ctx, user.UserID)
	if err != nil {
		s.logger.Printf("[server] 创建微信扫码登录失败: %v", err)
		writeError(w, http.StatusBadGateway, "获取微信二维码失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleWechatStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "扫码登录 ID 无效")
		return
	}
	view, err := s.wechat.Status(id, user.UserID)
	if err != nil {
		if errors.Is(err, wechat.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.logger.Printf("[server] 查询微信扫码状态失败: %v", err)
		writeError(w, http.StatusBadGateway, "查询扫码状态失败")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleWechatComplete(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "扫码登录 ID 无效")
		return
	}
	var req wechatCompleteRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	view, err := s.wechat.Complete(id, user.UserID, wechat.CompleteInput{
		AccountID: req.AccountID,
		Name:      strings.TrimSpace(req.Name),
		Campus:    strings.TrimSpace(req.Campus),
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrNotFound):
			s.writeStoreError(w, err)
		case errors.Is(err, wechat.ErrNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleWechatCancel(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "扫码登录 ID 无效")
		return
	}
	s.wechat.Cancel(id, user.UserID)
	w.WriteHeader(http.StatusNoContent)
}

type oaaUserKey struct{}

func oaaUserFrom(ctx context.Context) (oaa.User, bool) {
	user, ok := ctx.Value(oaaUserKey{}).(oaa.User)
	return user, ok
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		ctx, cancel := s.oaaContext(r)
		defer cancel()
		user, err := s.auth.Validate(ctx, token)
		if err != nil {
			if errors.Is(err, oaa.ErrUnauthorized) {
				writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
				return
			}
			s.logger.Printf("[server] OAA 校验登录状态失败: %v", err)
			writeError(w, http.StatusBadGateway, "OAA 认证服务暂不可用")
			return
		}
		s.claimLegacy(user)
		next(w, r.WithContext(context.WithValue(r.Context(), oaaUserKey{}, user)))
	}
}

func bearerToken(r *http.Request) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(r.Header.Get("Authorization")), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	return token, token != ""
}

func (s *Server) oaaContext(r *http.Request) (context.Context, context.CancelFunc) {
	timeout := s.cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	return context.WithTimeout(r.Context(), timeout)
}

func (s *Server) writeLoginError(w http.ResponseWriter, err error) {
	var apiErr *oaa.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests:
			writeError(w, apiErr.Status, apiErr.Message)
			return
		}
	}
	s.logger.Printf("[server] OAA 登录失败: %v", err)
	writeError(w, http.StatusBadGateway, "OAA 登录服务暂不可用")
}

func (s *Server) writeRefreshError(w http.ResponseWriter, err error) {
	var apiErr *oaa.Error
	if errors.As(err, &apiErr) && (apiErr.Status == http.StatusBadRequest || apiErr.Status == http.StatusUnauthorized) {
		writeError(w, http.StatusUnauthorized, "登录已失效，请重新登录")
		return
	}
	s.logger.Printf("[server] OAA 刷新令牌失败: %v", err)
	writeError(w, http.StatusBadGateway, "OAA 认证服务暂不可用")
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	snap := s.st.Snapshot(user.UserID, 50)
	writeJSON(w, http.StatusOK, stateResponse{
		Accounts:               snap.Accounts,
		Campuses:               locations.Summaries(),
		Schedule:               snap.Schedule,
		Scheduler:              s.sched.Status(user.UserID),
		Progress:               s.svc.ProgressFor(user.UserID),
		Runs:                   snap.Runs,
		ServerTime:             time.Now().In(s.loc),
		Timezone:               s.cfg.Timezone,
		NotificationsAvailable: s.notificationsAvailable,
	})
}

// claimLegacy 把旧版 state.json 里的数据迁移给第一个登录的 OAA 用户。
func (s *Server) claimLegacy(user oaa.User) {
	imported, err := s.st.ClaimLegacy(user.UserID)
	if err != nil {
		s.logger.Printf("[server] 迁移旧版数据失败: %v", err)
		return
	}
	if imported {
		s.logger.Printf("[server] 已把旧版 state.json 数据迁移给 OAA 用户 %d（%s）", user.UserID, user.StudentID)
	}
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req accountRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	view, err := s.st.AddAccount(user.UserID, store.AccountInput{
		StudentID: req.StudentID,
		Password:  req.Password,
		Name:      req.Name,
		Campus:    req.Campus,
		LoginType: store.LoginPassword,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	user, userOK := oaaUserFrom(r.Context())
	if !userOK {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req accountRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	view, err := s.st.UpdateAccount(user.UserID, id, store.AccountInput{
		StudentID: req.StudentID,
		Password:  req.Password,
		Name:      req.Name,
		Campus:    req.Campus,
		LoginType: req.LoginType,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user, userOK := oaaUserFrom(r.Context())
	if !userOK {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteAccount(user.UserID, id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.sched.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSaveSchedule(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req scheduleRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if req.NotifyEnabled && !s.notificationsAvailable {
		writeError(w, http.StatusBadRequest, "服务端未配置 SMTP 发信邮箱，暂不能开启失败通知")
		return
	}
	saved, err := s.st.SaveSchedule(user.UserID, store.Schedule{
		Enabled:              req.Enabled,
		TimeOfDay:            req.TimeOfDay,
		AccountIDs:           req.AccountIDs,
		MaxRetries:           req.MaxRetries,
		RetryIntervalMinutes: req.RetryIntervalMinutes,
		NotifyEnabled:        req.NotifyEnabled,
		NotifyEmail:          req.NotifyEmail,
	})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.sched.Wake()
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	user, ok := oaaUserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req runRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	ids := uniqueIDs(req.AccountIDs)
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "请选择要运行的账号")
		return
	}
	for _, id := range ids {
		if _, ok := s.st.Account(user.UserID, id); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("账号 %d 不存在", id))
			return
		}
	}
	if err := s.svc.StartRun(user.UserID, ids, "manual"); err != nil {
		switch {
		case errors.Is(err, checkin.ErrAlreadyRunning):
			writeError(w, http.StatusConflict, "已有打卡任务正在运行")
		case errors.Is(err, checkin.ErrNoAccounts):
			writeError(w, http.StatusBadRequest, "请选择要运行的账号")
		default:
			s.logger.Printf("[server] 启动打卡失败: %v", err)
			writeError(w, http.StatusInternalServerError, "启动打卡失败")
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "accountIds": ids})
}

func (s *Server) handleSample(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	file := r.PathValue("file")
	if !sampleDateRe.MatchString(date) || !sampleFileRe.MatchString(file) || filepath.Base(file) != file {
		http.NotFound(w, r)
		return
	}
	base := filepath.Join(s.cfg.DataDir, "captchas")
	path := filepath.Clean(filepath.Join(base, date, file))
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, path)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(s.index)
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))); !strings.HasPrefix(ct, "application/json") {
		return errUnsupportedMedia
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("请求体包含多余内容")
	}
	return nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnsupportedMedia) {
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "请求体过大")
		return
	}
	writeError(w, http.StatusBadRequest, "请求体无效: "+err.Error())
}

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid), errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound):
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
		} else if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, publicStoreError(err))
	default:
		s.logger.Printf("[server] 存储错误: %v", err)
		writeError(w, http.StatusInternalServerError, "服务端保存失败")
	}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "账号 ID 无效")
		return 0, false
	}
	return id, true
}

func uniqueIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func publicStoreError(err error) string {
	msg := err.Error()
	for _, prefix := range []string{
		store.ErrInvalid.Error() + ": ",
		store.ErrConflict.Error() + ": ",
		store.ErrNotFound.Error() + ": ",
	} {
		msg = strings.TrimPrefix(msg, prefix)
	}
	return msg
}
