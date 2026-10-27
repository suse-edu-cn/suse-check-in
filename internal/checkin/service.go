package checkin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"suse-checkin/internal/captcha"
	"suse-checkin/internal/locations"
	"suse-checkin/internal/store"
	"suse-checkin/internal/upstream"
)

const (
	StatusSuccess         = "success"
	StatusAlreadyChecked  = "already_checked"
	StatusNoTask          = "no_task"
	StatusCaptchaFailed   = "captcha_failed"
	StatusLoginFailed     = "login_failed"
	StatusSessionExpired  = "session_expired"
	StatusSMSRequired     = "sms_required"
	StatusCheckinFailed   = "checkin_failed"
	StatusLocationMissing = "location_missing"
	StatusError           = "error"

	attemptTimeout   = 2 * time.Minute
	rememberDuration = 30 * 24 * time.Hour
	maxMessageRunes  = 300
)

var (
	ErrAlreadyRunning = errors.New("已有打卡任务正在运行")
	ErrNoAccounts     = errors.New("没有可用的账号")
)

type Progress struct {
	Running   bool       `json:"running"`
	Trigger   string     `json:"trigger,omitempty"`
	Current   string     `json:"current,omitempty"`
	Done      int        `json:"done"`
	Total     int        `json:"total"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
}

type Options struct {
	Store        *store.Store
	Client       *upstream.Client
	Recognizer   captcha.Recognizer
	DataDir      string
	Location     *time.Location
	Logger       *log.Logger
	Now          func() time.Time
	Sleep        func(context.Context, time.Duration) error
	AccountDelay time.Duration
}

type Service struct {
	store        *store.Store
	client       *upstream.Client
	recognizer   captcha.Recognizer
	dataDir      string
	loc          *time.Location
	logger       *log.Logger
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	accountDelay time.Duration

	mu       sync.Mutex
	running  bool
	progress Progress
}

func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("缺少 store")
	}
	if opts.Client == nil {
		return nil, errors.New("缺少 upstream client")
	}
	if opts.Recognizer == nil {
		return nil, errors.New("缺少验证码识别器")
	}
	if opts.Location == nil {
		return nil, errors.New("缺少时区")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return nil, errors.New("缺少数据目录")
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = sleepContext
	}
	delay := opts.AccountDelay
	if delay < 0 {
		delay = 0
	}
	return &Service{
		store:        opts.Store,
		client:       opts.Client,
		recognizer:   opts.Recognizer,
		dataDir:      opts.DataDir,
		loc:          opts.Location,
		logger:       logger,
		now:          nowFn,
		sleep:        sleepFn,
		accountDelay: delay,
	}, nil
}

func (s *Service) RunAccounts(ctx context.Context, accountIDs []int64, trigger string) (store.Run, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(accountIDs) == 0 {
		return store.Run{}, ErrNoAccounts
	}
	if !s.beginRun(trigger, len(accountIDs)) {
		return store.Run{}, ErrAlreadyRunning
	}
	defer s.endRun()
	return s.runAccounts(ctx, accountIDs, trigger)
}

func (s *Service) StartRun(accountIDs []int64, trigger string) error {
	if len(accountIDs) == 0 {
		return ErrNoAccounts
	}
	if !s.beginRun(trigger, len(accountIDs)) {
		return ErrAlreadyRunning
	}
	go func() {
		defer s.endRun()
		if _, err := s.runAccounts(context.Background(), accountIDs, trigger); err != nil {
			s.logger.Printf("[checkin] %s 运行失败: %v", trigger, err)
		}
	}()
	return nil
}

func (s *Service) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Service) Progress() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.progress
	out.StartedAt = copyTimePtr(s.progress.StartedAt)
	return out
}

func (s *Service) beginRun(trigger string, total int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	now := s.nowTime()
	s.running = true
	s.progress = Progress{
		Running:   true,
		Trigger:   trigger,
		Total:     total,
		StartedAt: &now,
	}
	return true
}

func (s *Service) endRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.progress.Running = false
	s.progress.Current = ""
	s.progress.StartedAt = nil
}

func (s *Service) runAccounts(ctx context.Context, accountIDs []int64, trigger string) (store.Run, error) {
	schedule := s.store.Schedule()
	run := store.Run{
		Trigger:   trigger,
		StartedAt: s.nowTime(),
		Results:   make([]store.AccountResult, 0, len(accountIDs)),
	}
	var canceled error
	for i, accountID := range accountIDs {
		if err := ctx.Err(); err != nil {
			canceled = err
			break
		}
		acc, ok := s.store.Account(accountID)
		if !ok {
			now := s.nowTime()
			run.Results = append(run.Results, store.AccountResult{
				AccountID:  accountID,
				Status:     StatusError,
				Message:    "账号不存在",
				StartedAt:  now,
				FinishedAt: now,
			})
			s.setProgressDone(i+1, "账号不存在")
			continue
		}
		display := displayName(acc)
		s.setProgressCurrent(display, i)
		result := s.runAccount(ctx, acc, schedule.MaxRetries, time.Duration(schedule.RetryIntervalMinutes)*time.Minute)
		run.Results = append(run.Results, result)
		statusText := "成功：" + result.Message
		if isFailure(result.Status) {
			statusText = "失败：" + result.Message
		}
		if err := s.store.UpdateCheckinStatus(acc.ID, statusText, s.nowTime()); err != nil {
			s.logger.Printf("[checkin] 账号 %d 回写状态失败: %v", acc.ID, err)
		}
		s.setProgressDone(i+1, display)
		if i < len(accountIDs)-1 && s.accountDelay > 0 {
			if err := s.sleep(ctx, s.accountDelay); err != nil {
				canceled = err
				break
			}
		}
	}
	run.FinishedAt = s.nowTime()
	run.Summary = summarize(run.Results)
	if canceled != nil {
		run.Summary += "（已取消）"
	}
	saved, err := s.store.AppendRun(run)
	if err != nil {
		s.logger.Printf("[checkin] 保存运行记录失败: %v", err)
		return run, err
	}
	s.logger.Printf("[checkin] %s", saved.Summary)
	if canceled != nil {
		return saved, canceled
	}
	return saved, nil
}

func (s *Service) runAccount(ctx context.Context, acc store.Account, maxRetries int, interval time.Duration) store.AccountResult {
	if acc.LoginType == store.LoginWechat {
		return s.runWechatAccount(ctx, acc, maxRetries, interval)
	}
	result := store.AccountResult{
		AccountID:   acc.ID,
		StudentID:   acc.StudentID,
		AccountName: displayName(acc),
		StartedAt:   s.nowTime(),
	}
	password, err := s.store.Password(acc)
	if err != nil {
		s.logger.Printf("[checkin] 账号 %d 读取密码失败: %v", acc.ID, err)
		result.Status = StatusError
		result.Message = "读取账号密码失败，请重新保存密码"
		result.FinishedAt = s.nowTime()
		return result
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			s.logger.Printf("[checkin] 账号 %d 第 %d 次重试", acc.ID, attempt)
			if err := s.sleep(ctx, interval); err != nil {
				result.Status = StatusError
				result.Message = "任务已取消"
				break
			}
		}
		attemptResult, retryable := s.attempt(ctx, acc, password, attempt+1)
		attemptResult.AccountID = acc.ID
		attemptResult.StudentID = acc.StudentID
		attemptResult.AccountName = displayName(acc)
		attemptResult.StartedAt = result.StartedAt
		result = attemptResult
		if !retryable || attempt == maxRetries {
			break
		}
	}
	result.FinishedAt = s.nowTime()
	return result
}

func (s *Service) runWechatAccount(ctx context.Context, acc store.Account, maxRetries int, interval time.Duration) store.AccountResult {
	result := store.AccountResult{
		AccountID:   acc.ID,
		StudentID:   acc.StudentID,
		AccountName: displayName(acc),
		StartedAt:   s.nowTime(),
	}
	cookies, err := s.store.RememberCookies(acc)
	if err != nil {
		s.logger.Printf("[checkin] 账号 %d 读取扫码会话失败: %v", acc.ID, err)
		result.Status = StatusSessionExpired
		result.Message = "读取扫码会话失败，请重新扫码登录"
		result.FinishedAt = s.nowTime()
		return result
	}
	if cookies == "" {
		result.Status = StatusSessionExpired
		result.Message = "扫码会话不存在，请重新扫码登录"
		result.FinishedAt = s.nowTime()
		return result
	}
	if acc.RememberExpiresAt != nil && !acc.RememberExpiresAt.After(s.now()) {
		result.Status = StatusSessionExpired
		result.Message = "扫码会话已过期，请重新扫码登录"
		result.FinishedAt = s.nowTime()
		return result
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			s.logger.Printf("[checkin] 账号 %d 扫码会话第 %d 次重试", acc.ID, attempt)
			if err := s.sleep(ctx, interval); err != nil {
				result.Status = StatusError
				result.Message = "任务已取消"
				break
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		session, updated, expires, err := s.client.WechatSession(attemptCtx, cookies)
		if err != nil {
			cancel()
			if errors.Is(err, upstream.ErrWechatSessionExpired) {
				result.Status = StatusSessionExpired
				result.Message = "扫码会话已过期，请重新扫码登录"
				break
			}
			result.Status = StatusLoginFailed
			result.Message = "刷新扫码会话失败: " + s.errText(err)
			if attempt == maxRetries {
				break
			}
			continue
		}
		cookies = updated
		if err := s.store.SaveRememberCookies(acc.ID, updated, expires); err != nil {
			s.logger.Printf("[checkin] 账号 %d 更新扫码会话失败: %v", acc.ID, err)
		}
		attemptResult := result
		attemptResult, retryable := s.perform(attemptCtx, session, acc, attemptResult)
		cancel()
		result = attemptResult
		if !retryable || attempt == maxRetries {
			break
		}
	}
	result.FinishedAt = s.nowTime()
	return result
}

func (s *Service) attempt(ctx context.Context, acc store.Account, password string, attemptNo int) (result store.AccountResult, retryable bool) {
	result = store.AccountResult{
		AccountID:   acc.ID,
		StudentID:   acc.StudentID,
		AccountName: displayName(acc),
		StartedAt:   s.nowTime(),
	}
	var captchaImage []byte
	var ocrText string
	var ocrErr error
	var ocrMillis int64
	defer func() {
		if len(captchaImage) == 0 {
			return
		}
		sample, err := s.saveCaptchaSample(acc.ID, attemptNo, captchaImage, ocrText, ocrErr, result.Status, result.Message, ocrMillis)
		if err != nil {
			s.logger.Printf("[checkin] 保存验证码样本失败: %v", err)
			return
		}
		result.CaptchaSample = sample
	}()

	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	if attemptNo == 1 && acc.RememberCookiesEnc != "" && (acc.RememberExpiresAt == nil || acc.RememberExpiresAt.After(s.now())) {
		cookies, err := s.store.RememberCookies(acc)
		if err != nil {
			s.logger.Printf("[checkin] 账号 %d 读取 rememberMe 失败: %v", acc.ID, err)
		} else if cookies != "" {
			session, ok, err := s.client.TryRememberLogin(ctx, cookies)
			if err != nil {
				s.logger.Printf("[checkin] 账号 %d rememberMe 失败，回退验证码登录: %v", acc.ID, err)
			} else if ok {
				return s.perform(ctx, session, acc, result)
			}
		}
	}

	loginAttempt, err := s.client.BeginPasswordLogin(ctx)
	if err != nil {
		result.Status = StatusLoginFailed
		result.Message = "获取登录验证码失败: " + s.errText(err)
		return result, true
	}
	captchaImage = loginAttempt.Captcha()
	ocrStart := time.Now()
	ocrText, ocrErr = s.recognizer.Recognize(captchaImage)
	ocrMillis = time.Since(ocrStart).Milliseconds()
	result.OCRText = ocrText
	result.OCRMillis = ocrMillis
	if ocrErr != nil {
		result.Status = StatusCaptchaFailed
		result.Message = "验证码识别失败: " + s.errText(ocrErr)
		return result, true
	}

	session, err := loginAttempt.Submit(ctx, acc.StudentID, password, ocrText)
	if err != nil {
		var loginErr *upstream.LoginError
		if errors.As(err, &loginErr) {
			switch loginErr.Kind {
			case upstream.KindSMSRequired:
				result.Status = StatusSMSRequired
				result.Message = loginErr.Message
				return result, false
			case upstream.KindCredentials:
				result.Status = StatusLoginFailed
				result.Message = loginErr.Message
				return result, false
			case upstream.KindCaptcha:
				result.Status = StatusCaptchaFailed
				result.Message = loginErr.Message
				return result, true
			default:
				result.Status = StatusLoginFailed
				result.Message = loginErr.Message
				return result, loginErr.Retryable()
			}
		}
		result.Status = StatusLoginFailed
		result.Message = "登录失败: " + s.errText(err)
		return result, true
	}

	if cookies := session.RememberCookies(); cookies != "" {
		if err := s.store.SaveRememberCookies(acc.ID, cookies, s.nowTime().Add(rememberDuration)); err != nil {
			s.logger.Printf("[checkin] 账号 %d 保存 rememberMe 失败: %v", acc.ID, err)
		}
	}
	return s.perform(ctx, session, acc, result)
}

func (s *Service) perform(ctx context.Context, session *upstream.Session, acc store.Account, result store.AccountResult) (store.AccountResult, bool) {
	tasks, err := session.PendingTasks(ctx)
	if err != nil {
		result.Status = StatusCheckinFailed
		result.Message = "获取待签到任务失败: " + s.errText(err)
		return result, true
	}
	if len(tasks) == 0 {
		result.Status = StatusNoTask
		result.Message = "当前没有待签到的任务"
		return result, false
	}

	task := pickTask(tasks, s.nowTime().Format("2006-01-02"))
	result.TaskID = task.ID
	result.TaskName = strings.TrimSpace(task.Name)
	if result.TaskName == "" {
		result.TaskName = fmt.Sprintf("任务 #%d", task.ID)
	}

	detail, err := session.TaskDetail(ctx, task.ID)
	if err != nil {
		result.Status = StatusCheckinFailed
		result.Message = "获取任务详情失败: " + s.errText(err)
		return result, true
	}
	if detail.CheckStatus == 1 {
		result.Status = StatusAlreadyChecked
		result.Message = "今日已签到，无需重复操作"
		return result, false
	}
	loc, ok := locations.Pick(acc.Campus)
	if !ok {
		result.Status = StatusLocationMissing
		result.Message = "未找到该校区的内置签到位置"
		return result, false
	}
	result.LocationAddress = loc.Address
	result.LocationLatitude = loc.Latitude
	result.LocationLongitude = loc.Longitude

	submit, err := session.SubmitCheckin(ctx, upstream.SignRequest{
		TaskID:       task.ID,
		SignTime:     s.nowTime(),
		Address:      loc.Address,
		Latitude:     loc.Latitude,
		Longitude:    loc.Longitude,
		LocationJSON: loc.LocationJSON,
	})
	if err != nil {
		result.Status = StatusCheckinFailed
		result.Message = "提交签到失败: " + s.errText(err)
		return result, true
	}
	if !submit.Success || submit.ResultCode != 0 {
		result.Status = StatusCheckinFailed
		result.Message = nonEmpty(submit.ErrorMsg, "服务端返回签到失败")
		return result, false
	}
	result.Status = StatusSuccess
	result.Message = "签到成功"
	return result, false
}

func (s *Service) saveCaptchaSample(accountID int64, attempt int, image []byte, ocrText string, ocrErr error, status, message string, ocrMillis int64) (string, error) {
	now := s.nowTime()
	dir := filepath.Join(s.dataDir, "captchas", now.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ext := ".jpg"
	switch http.DetectContentType(image) {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	}
	name := fmt.Sprintf("%d-%s-%d%s", accountID, now.Format("20060102-150405.000"), attempt, ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return "", err
	}
	meta := struct {
		AccountID int64     `json:"accountId"`
		Attempt   int       `json:"attempt"`
		CreatedAt time.Time `json:"createdAt"`
		OCRText   string    `json:"ocrText"`
		OCRMillis int64     `json:"ocrMillis"`
		OCRError  string    `json:"ocrError,omitempty"`
		Status    string    `json:"status"`
		Message   string    `json:"message"`
	}{
		AccountID: accountID,
		Attempt:   attempt,
		CreatedAt: now,
		OCRText:   ocrText,
		OCRMillis: ocrMillis,
		Status:    status,
		Message:   s.errText(errors.New(message)),
	}
	if ocrErr != nil {
		meta.OCRError = s.errText(ocrErr)
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(strings.TrimSuffix(path, ext)+".json", raw, 0o600); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.dataDir, path)
	if err != nil {
		rel = filepath.Join("captchas", now.Format("2006-01-02"), name)
	}
	return filepath.ToSlash(rel), nil
}

func (s *Service) nowTime() time.Time {
	return s.now().In(s.loc)
}

func (s *Service) setProgressCurrent(current string, done int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress.Current = current
	s.progress.Done = done
}

func (s *Service) setProgressDone(done int, current string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress.Done = done
	s.progress.Current = current
}

func (s *Service) errText(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	r := []rune(text)
	if len(r) > maxMessageRunes {
		return string(r[:maxMessageRunes]) + "…"
	}
	return text
}

func pickTask(tasks []upstream.Task, today string) upstream.Task {
	for _, task := range tasks {
		need := strings.TrimSpace(task.NeedTime)
		if strings.TrimSpace(task.Status) == "进行中" && (need == today || strings.HasPrefix(need, today)) {
			return task
		}
	}
	for _, task := range tasks {
		if strings.TrimSpace(task.Status) == "进行中" && strings.HasPrefix(strings.TrimSpace(task.StartDate), today) {
			return task
		}
	}
	return tasks[0]
}

func summarize(results []store.AccountResult) string {
	success, failed := 0, 0
	for _, result := range results {
		if isFailure(result.Status) {
			failed++
		} else {
			success++
		}
	}
	return fmt.Sprintf("成功 %d，失败 %d，共 %d 个账号", success, failed, len(results))
}

func isFailure(status string) bool {
	switch status {
	case StatusSuccess, StatusAlreadyChecked, StatusNoTask:
		return false
	default:
		return true
	}
}

func displayName(acc store.Account) string {
	if name := strings.TrimSpace(acc.Name); name != "" {
		return name
	}
	return acc.StudentID
}

func nonEmpty(v, fallback string) string {
	if v = strings.TrimSpace(v); v == "" {
		return fallback
	}
	return v
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
