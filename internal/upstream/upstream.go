package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	loginPath            = "/sso/login"
	captchaPath          = "/sso/captcha.jpg"
	qfhyAdminPath        = "/xg/app/qddk/admin/qddkdk"
	qfhyLoginServicePath = "/site/appware/system/sso/login"
	taskListPath         = "/site/qddk/qdrw/api/myList.rst"
	taskDetailPath       = "/site/qddk/qdrw/qdxx/api/detailList.rst"
	submitCheckinPath    = "/site/qddk/qdrw/api/checkSignLocationWithPhoto.rst"

	maxBody            = 4 << 20
	maxCaptchaBody     = 2 << 20
	maxRedirects       = 10
	defaultHTTPTimeout = 25 * time.Second
	defaultUserAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
)

type LoginErrorKind string

const (
	KindCaptcha     LoginErrorKind = "captcha"
	KindCredentials LoginErrorKind = "credentials"
	KindSMSRequired LoginErrorKind = "sms_required"
	KindSession     LoginErrorKind = "session"
	KindNetwork     LoginErrorKind = "network"
	KindUnknown     LoginErrorKind = "unknown"
)

type LoginError struct {
	Kind       LoginErrorKind
	Message    string
	HTTPStatus int
	Err        error
}

func (e *LoginError) Error() string { return e.Message }
func (e *LoginError) Unwrap() error { return e.Err }

func (e *LoginError) Retryable() bool {
	return e.Kind == KindCaptcha || e.Kind == KindSession || e.Kind == KindNetwork
}

type Client struct {
	uiasBase string
	qfhyBase string
	timeout  time.Duration
	ua       string
}

func New(uiasBase, qfhyBase string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	return &Client{
		uiasBase: strings.TrimRight(uiasBase, "/"),
		qfhyBase: strings.TrimRight(qfhyBase, "/"),
		timeout:  timeout,
		ua:       defaultUserAgent,
	}
}

func (c *Client) LoginPageURL() string {
	target := c.qfhyBase + qfhyAdminPath
	service := c.qfhyBase + qfhyLoginServicePath + "?target=" + target
	q := url.Values{}
	q.Set("service", service)
	return c.uiasBase + loginPath + "?" + q.Encode()
}

type LoginAttempt struct {
	client       *Client
	hc           *http.Client
	jar          http.CookieJar
	loginPageURL string
	execution    string
	captcha      []byte
}

func (c *Client) BeginPasswordLogin(ctx context.Context) (*LoginAttempt, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Cookie 容器: %w", err)
	}
	hc := c.newHTTPClient(jar)
	loginPageURL := c.LoginPageURL()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建登录页请求: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	c.setCommonHeaders(req)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问登录页: %w", err)
	}
	defer resp.Body.Close()
	body, err := readBody(resp.Body, maxBody)
	if err != nil {
		return nil, fmt.Errorf("读取登录页: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("登录页返回 HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	execution := parseExecution(string(body))
	if execution == "" {
		return nil, errors.New("登录页未找到 execution token")
	}

	capReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.uiasBase+captchaPath, nil)
	if err != nil {
		return nil, fmt.Errorf("创建验证码请求: %w", err)
	}
	capReq.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	capReq.Header.Set("Referer", loginPageURL)
	c.setCommonHeaders(capReq)
	capResp, err := hc.Do(capReq)
	if err != nil {
		return nil, fmt.Errorf("获取验证码: %w", err)
	}
	defer capResp.Body.Close()
	captchaBytes, err := readBody(capResp.Body, maxCaptchaBody)
	if err != nil {
		return nil, fmt.Errorf("读取验证码: %w", err)
	}
	if capResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("验证码接口返回 HTTP %d: %s", capResp.StatusCode, snippet(captchaBytes))
	}
	if ct := capResp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "image/") {
		return nil, fmt.Errorf("验证码接口返回了非图片内容: %s", ct)
	}
	if len(captchaBytes) == 0 {
		return nil, errors.New("验证码图片为空")
	}

	return &LoginAttempt{
		client:       c,
		hc:           hc,
		jar:          jar,
		loginPageURL: loginPageURL,
		execution:    execution,
		captcha:      captchaBytes,
	}, nil
}

func (a *LoginAttempt) Captcha() []byte {
	return append([]byte(nil), a.captcha...)
}

func (a *LoginAttempt) Submit(ctx context.Context, username, password, captchaCode string) (*Session, error) {
	encrypted, err := EncryptPassword(password)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", encrypted)
	form.Set("authcode", captchaCode)
	form.Set("execution", a.execution)
	form.Set("encrypted", "true")
	form.Set("_eventId", "submit")
	form.Set("loginType", "1")
	form.Set("rememberMe", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.loginPageURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建登录请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Origin", a.client.uiasBase)
	req.Header.Set("Referer", a.loginPageURL)
	a.client.setCommonHeaders(req)

	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, &LoginError{Kind: KindNetwork, Message: "提交登录请求失败: " + err.Error(), Err: err}
	}
	defer resp.Body.Close()

	if isRedirect(resp.StatusCode) {
		location := strings.TrimSpace(resp.Header.Get("Location"))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		if location == "" {
			return nil, &LoginError{Kind: KindSession, Message: "登录重定向缺少 Location", HTTPStatus: resp.StatusCode}
		}
		target := resolveURL(a.loginPageURL, location)
		final, finalURL, err := followRedirects(ctx, a.hc, a.client.ua, target, a.client.uiasBase+"/")
		if err != nil {
			return nil, &LoginError{Kind: KindSession, Message: "登录重定向失败: " + err.Error(), Err: err}
		}
		final.Body.Close()
		if !hasCookie(a.jar, finalURL, "SESSION") && !hasCookie(a.jar, a.client.qfhyBase+"/", "SESSION") {
			return nil, &LoginError{Kind: KindSession, Message: "登录后未获取到 qfhy SESSION"}
		}
		return &Session{client: a.client, hc: a.hc, jar: a.jar}, nil
	}

	body, readErr := readBody(resp.Body, maxBody)
	if readErr != nil {
		return nil, &LoginError{Kind: KindUnknown, Message: "读取登录响应失败: " + readErr.Error(), Err: readErr}
	}
	page := string(body)
	if dir := strings.TrimSpace(os.Getenv("CHECKIN_LOGIN_DEBUG_DIR")); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			name := "login-fail-" + time.Now().Format("20060102-150405.000") + ".html"
			_ = os.WriteFile(filepath.Join(dir, name), body, 0o600)
		}
	}
	if requiresSMS(page) {
		msg := "检测到短信二次验证，自动打卡无法继续"
		if phone := phoneMasked(page); phone != "" {
			msg += "（手机号 " + phone + "）"
		}
		return nil, &LoginError{Kind: KindSMSRequired, Message: msg, HTTPStatus: resp.StatusCode}
	}
	kind, msg := classifyLoginFailure(page, resp.StatusCode)
	return nil, &LoginError{Kind: kind, Message: msg, HTTPStatus: resp.StatusCode}
}

func (c *Client) TryRememberLogin(ctx context.Context, cookies string) (*Session, bool, error) {
	if !strings.Contains(cookies, "CASTGC=") {
		return nil, false, nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, false, fmt.Errorf("创建 Cookie 容器: %w", err)
	}
	uiasURL, err := url.Parse(c.uiasBase + "/sso/")
	if err != nil {
		return nil, false, fmt.Errorf("解析 UIAS 地址: %w", err)
	}
	for _, part := range strings.Split(cookies, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		jar.SetCookies(uiasURL, []*http.Cookie{{
			Name:  strings.TrimSpace(name),
			Value: value,
			Path:  "/",
		}})
	}
	hc := c.newHTTPClient(jar)
	loginPageURL := c.LoginPageURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	c.setCommonHeaders(req)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("rememberMe 登录请求: %w", err)
	}
	if !isRedirect(resp.StatusCode) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		return nil, false, nil
	}
	location := strings.TrimSpace(resp.Header.Get("Location"))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if location == "" {
		return nil, false, nil
	}
	final, finalURL, err := followRedirects(ctx, hc, c.ua, resolveURL(loginPageURL, location), c.uiasBase+"/")
	if err != nil {
		return nil, false, err
	}
	final.Body.Close()
	if !hasCookie(jar, finalURL, "SESSION") && !hasCookie(jar, c.qfhyBase+"/", "SESSION") {
		return nil, false, nil
	}
	return &Session{client: c, hc: hc, jar: jar}, true, nil
}

type Session struct {
	client *Client
	hc     *http.Client
	jar    http.CookieJar
	openID string
}

func (s *Session) RememberCookies() string {
	u, err := url.Parse(s.client.uiasBase + "/sso/login")
	if err != nil {
		return ""
	}
	cookies := s.jar.Cookies(u)
	parts := make([]string, 0, len(cookies))
	hasCASTGC := false
	for _, c := range cookies {
		if c.Name == "" {
			continue
		}
		if c.Name == "CASTGC" && c.Value != "" {
			hasCASTGC = true
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	if !hasCASTGC {
		return ""
	}
	return strings.Join(parts, "; ")
}

func (s *Session) PendingTasks(ctx context.Context) ([]Task, error) {
	endpoint := s.client.qfhyBase + taskListPath + "?status=1"
	var out TaskListResponse
	if err := s.getJSON(ctx, endpoint, &out); err != nil {
		return nil, fmt.Errorf("获取任务列表: %w", err)
	}
	if !out.Success || out.ResultCode != 0 {
		return nil, fmt.Errorf("获取任务列表失败: %s", nonEmpty(out.ErrorMsg, "服务端返回失败"))
	}
	if out.Result == nil {
		return nil, errors.New("任务列表数据为空")
	}
	return out.Result.Data, nil
}

func (s *Session) TaskDetail(ctx context.Context, taskID int64) (Detail, error) {
	q := url.Values{}
	q.Set("qdrwId", strconv.FormatInt(taskID, 10))
	endpoint := s.client.qfhyBase + taskDetailPath + "?" + q.Encode()
	var out DetailResponse
	if err := s.getJSON(ctx, endpoint, &out); err != nil {
		return Detail{}, fmt.Errorf("获取任务详情: %w", err)
	}
	if !out.Success || out.ResultCode != 0 {
		return Detail{}, fmt.Errorf("获取任务详情失败: %s", nonEmpty(out.ErrorMsg, "服务端返回失败"))
	}
	if out.Result == nil || out.Result.Data == nil || out.Result.Data.DKXX == nil {
		return Detail{}, errors.New("任务详情数据为空")
	}
	return *out.Result.Data.DKXX, nil
}

func (s *Session) SubmitCheckin(ctx context.Context, req SignRequest) (SubmitResponse, error) {
	body, err := BuildSignBody(req)
	if err != nil {
		return SubmitResponse{}, err
	}
	endpoint := s.client.qfhyBase + submitCheckinPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("创建签到请求: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	s.setAPIHeaders(httpReq)
	resp, err := s.hc.Do(httpReq)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("提交签到: %w", err)
	}
	defer resp.Body.Close()
	raw, err := readBody(resp.Body, maxBody)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("读取签到响应: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return SubmitResponse{}, fmt.Errorf("签到接口返回 HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var out SubmitResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return SubmitResponse{}, fmt.Errorf("解析签到响应: %w", err)
	}
	return out, nil
}

func (s *Session) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	s.setAPIHeaders(req)
	resp, err := s.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := readBody(resp.Body, maxBody)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析 JSON: %w", err)
	}
	return nil
}

func (s *Session) setAPIHeaders(req *http.Request) {
	referer := s.client.qfhyBase + qfhyAdminPath
	if s.openID != "" {
		referer = s.client.qfhyBase + qfhyQRAdminPath + "?open_id=" + url.QueryEscape(s.openID)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", referer)
	req.Header.Set("appcode", "qddk")
	s.client.setCommonHeaders(req)
}

type Task struct {
	ID          int64   `json:"id"`
	Name        string  `json:"rwmc"`
	Status      string  `json:"rwzt"`
	NeedTime    string  `json:"needTime"`
	StartDate   string  `json:"qdksrq"`
	CheckTime   *string `json:"qdsj"`
	CheckStatus *int    `json:"qdzt"`
}

type TaskListResponse struct {
	ResultCode int             `json:"resultCode"`
	ErrorMsg   string          `json:"errorMsg"`
	Success    bool            `json:"success"`
	Result     *TaskListResult `json:"result"`
}

type TaskListResult struct {
	Data  []Task `json:"data"`
	Total int    `json:"total"`
}

type Detail struct {
	ID          int64   `json:"id"`
	CheckStatus int     `json:"qdzt"`
	CheckTime   *string `json:"qdsj"`
	NeedTime    *string `json:"needTime"`
	Address     *string `json:"qdddjtdz"`
}

type DetailResponse struct {
	ResultCode int           `json:"resultCode"`
	ErrorMsg   string        `json:"errorMsg"`
	Success    bool          `json:"success"`
	Result     *DetailResult `json:"result"`
}

type DetailResult struct {
	Data *DetailData `json:"data"`
}

type DetailData struct {
	DKXX *Detail `json:"dkxx"`
}

type SubmitResponse struct {
	ResultCode int           `json:"resultCode"`
	ErrorMsg   string        `json:"errorMsg"`
	Success    bool          `json:"success"`
	Result     *SubmitResult `json:"result"`
}

type SubmitResult struct {
	Data  bool `json:"data"`
	Total int  `json:"total"`
}

type SignRequest struct {
	TaskID       int64
	SignTime     time.Time
	Address      string
	Latitude     float64
	Longitude    float64
	LocationJSON string
}

func BuildSignBody(req SignRequest) ([]byte, error) {
	if req.TaskID <= 0 {
		return nil, errors.New("签到任务 ID 无效")
	}
	if req.SignTime.IsZero() {
		return nil, errors.New("签到时间不能为空")
	}
	address := strings.TrimSpace(req.Address)
	locationJSON := strings.TrimSpace(req.LocationJSON)
	if locationJSON != "" {
		var parsed struct {
			Point   []float64 `json:"point"`
			Address string    `json:"address"`
		}
		if err := json.Unmarshal([]byte(locationJSON), &parsed); err != nil {
			return nil, errors.New("签到位置 JSON 无效")
		}
		if len(parsed.Point) != 2 {
			return nil, errors.New("签到位置 JSON 缺少 point")
		}
		if parsed.Point[1] < -90 || parsed.Point[1] > 90 || parsed.Point[0] < -180 || parsed.Point[0] > 180 {
			return nil, errors.New("签到经纬度超出范围")
		}
		if parsed.Point[0] == 0 && parsed.Point[1] == 0 {
			return nil, errors.New("签到经纬度无效")
		}
		if strings.TrimSpace(parsed.Address) != "" {
			address = strings.TrimSpace(parsed.Address)
		}
		if address == "" {
			return nil, errors.New("签到地址不能为空")
		}
	} else {
		if address == "" {
			return nil, errors.New("签到地址不能为空")
		}
		if req.Latitude < -90 || req.Latitude > 90 || req.Longitude < -180 || req.Longitude > 180 {
			return nil, errors.New("签到经纬度超出范围")
		}
		if req.Latitude == 0 && req.Longitude == 0 {
			return nil, errors.New("签到经纬度无效")
		}
		raw, err := json.Marshal(struct {
			Point   []float64 `json:"point"`
			Address string    `json:"address"`
		}{
			Point:   []float64{req.Longitude, req.Latitude},
			Address: address,
		})
		if err != nil {
			return nil, err
		}
		locationJSON = string(raw)
	}
	payload := struct {
		ID        int64  `json:"id"`
		QDZT      int    `json:"qdzt"`
		QDSJ      string `json:"qdsj"`
		IsOuted   int    `json:"isOuted"`
		IsLated   int    `json:"isLated"`
		DKDDPhoto string `json:"dkddPhoto"`
		QDDDJTDZ  string `json:"qdddjtdz"`
		Location  string `json:"location"`
		TXXX      string `json:"txxx"`
	}{
		ID:        req.TaskID,
		QDZT:      1,
		QDSJ:      req.SignTime.Format("2006-01-02 15:04:05"),
		DKDDPhoto: "",
		QDDDJTDZ:  address,
		Location:  locationJSON,
		TXXX:      "{}",
	}
	return json.Marshal(payload)
}

func (c *Client) newHTTPClient(jar http.CookieJar) *http.Client {
	return &http.Client{
		Timeout: c.timeout,
		Jar:     jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (c *Client) setCommonHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
}

func followRedirects(ctx context.Context, hc *http.Client, ua, start, referer string) (*http.Response, string, error) {
	current := start
	for i := 0; i <= maxRedirects; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return nil, current, err
		}
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		req.Header.Set("User-Agent", ua)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, current, err
		}
		if !isRedirect(resp.StatusCode) {
			return resp, current, nil
		}
		location := strings.TrimSpace(resp.Header.Get("Location"))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if location == "" {
			return nil, current, errors.New("重定向响应缺少 Location")
		}
		referer = current
		current = resolveURL(current, location)
	}
	return nil, current, fmt.Errorf("重定向超过 %d 次", maxRedirects)
}

func resolveURL(baseURL, location string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return location
	}
	ref, err := url.Parse(location)
	if err != nil {
		return location
	}
	return base.ResolveReference(ref).String()
}

func hasCookie(jar http.CookieJar, rawURL, name string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func readBody(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("响应超过 %d 字节", limit)
	}
	return raw, nil
}

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, 300)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var (
	executionNameFirst  = regexp.MustCompile(`(?is)<input\b[^>]*\bname\s*=\s*["']execution["'][^>]*\bvalue\s*=\s*["']([^"']+)["']`)
	executionValueFirst = regexp.MustCompile(`(?is)<input\b[^>]*\bvalue\s*=\s*["']([^"']+)["'][^>]*\bname\s*=\s*["']execution["']`)
	phoneMaskedRe       = regexp.MustCompile(`(?is)name\s*=\s*["']phone["'][^>]*value\s*=\s*["']([^"']+)["']`)
	loginErrorBlockRe   = regexp.MustCompile(`(?is)<(div|span|p)\b[^>]*(?:id\s*=\s*["']msg[12]?["']|class\s*=\s*["'][^"']*(?:form-error|error|alert)[^"']*["'])[^>]*>(.*?)</(?:div|span|p)>`)
	htmlTagRe           = regexp.MustCompile(`(?is)<[^>]+>`)
	passwordFormRe      = regexp.MustCompile(`(?is)<form\b[^>]*\bid\s*=\s*["']fm1["']`)
	passwordInputRe     = regexp.MustCompile(`(?is)<input\b[^>]*\bname\s*=\s*["']password["']`)
)

var knownLoginErrors = []string{
	"用户名或密码错误",
	"密码错误次数过多",
	"密码错误",
	"验证码不正确",
	"验证码错误",
	"验证码已过期",
	"账号已被锁定",
	"账号不存在",
	"用户不存在",
	"密码已过期",
	"请求过于频繁",
	"系统繁忙",
	"非法请求",
	"滑动验证",
}

func parseExecution(page string) string {
	for _, re := range []*regexp.Regexp{executionNameFirst, executionValueFirst} {
		if m := re.FindStringSubmatch(page); len(m) > 1 {
			return stdhtml.UnescapeString(strings.TrimSpace(m[1]))
		}
	}
	return ""
}

func phoneMasked(page string) string {
	if m := phoneMaskedRe.FindStringSubmatch(page); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func hasPasswordLoginForm(page string) bool {
	return passwordFormRe.MatchString(page) && passwordInputRe.MatchString(page)
}

func requiresSMS(page string) bool {
	lower := strings.ToLower(page)
	if strings.Contains(lower, "sendsms_double") || strings.Contains(lower, "doublesubmit") {
		return true
	}
	// 普通登录页本身带有“短信登录”标签和 smscode 输入框，
	// 不能据此判断为短信二次验证，否则任何登录失败都会被误报。
	if hasPasswordLoginForm(page) {
		return false
	}
	return strings.Contains(lower, "smscode") ||
		strings.Contains(page, "短信验证") ||
		strings.Contains(page, "短信二次验证")
}

func classifyLoginFailure(page string, status int) (LoginErrorKind, string) {
	if requiresSMS(page) {
		return KindSMSRequired, "检测到短信二次验证，自动打卡无法继续"
	}
	if msg := extractLoginError(page); msg != "" {
		return classifyLoginMessage(msg), msg
	}
	for _, msg := range knownLoginErrors {
		if strings.Contains(page, msg) {
			return classifyLoginMessage(msg), msg
		}
	}
	return KindUnknown, fmt.Sprintf("登录失败 (HTTP %d)", status)
}

func extractLoginError(page string) string {
	for _, m := range loginErrorBlockRe.FindAllStringSubmatch(page, -1) {
		if len(m) < 3 {
			continue
		}
		text := stdhtml.UnescapeString(htmlTagRe.ReplaceAllString(m[2], " "))
		text = strings.Join(strings.Fields(text), " ")
		if text != "" {
			return truncateRunes(text, 200)
		}
	}
	return ""
}

func classifyLoginMessage(msg string) LoginErrorKind {
	switch {
	case strings.Contains(msg, "短信"):
		return KindSMSRequired
	case strings.Contains(msg, "验证码"):
		return KindCaptcha
	case strings.Contains(msg, "滑动"), strings.Contains(msg, "滑块"):
		return KindUnknown
	case strings.Contains(msg, "密码"), strings.Contains(msg, "用户"), strings.Contains(msg, "账号"):
		return KindCredentials
	default:
		return KindUnknown
	}
}
