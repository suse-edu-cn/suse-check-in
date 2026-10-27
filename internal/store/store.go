package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"suse-checkin/internal/locations"
)

const (
	keyFileName    = "key"
	dbFileName     = "state.db"
	legacyFileName = "state.json"
	schemaVersion  = 1
	maxRuns        = 200
)

var (
	ErrNotFound = errors.New("记录不存在")
	ErrConflict = errors.New("记录冲突")
	ErrInvalid  = errors.New("参数无效")
)

type LoginType string

const (
	LoginPassword LoginType = "password"
	LoginWechat   LoginType = "wechat"
)

func normalizeLoginType(value LoginType) LoginType {
	if value == LoginWechat {
		return LoginWechat
	}
	return LoginPassword
}

type Account struct {
	ID                 int64      `json:"id"`
	StudentID          string     `json:"studentId"`
	Name               string     `json:"name"`
	LoginType          LoginType  `json:"loginType"`
	PasswordEnc        string     `json:"passwordEnc,omitempty"`
	RememberCookiesEnc string     `json:"rememberCookiesEnc,omitempty"`
	RememberExpiresAt  *time.Time `json:"rememberExpiresAt,omitempty"`
	Campus             string     `json:"campus"`
	Scheduled          bool       `json:"scheduled"`
	LastCheckinAt      *time.Time `json:"lastCheckinAt,omitempty"`
	LastStatus         string     `json:"lastStatus,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type AccountInput struct {
	StudentID string
	Password  string
	Name      string
	Campus    string
	LoginType LoginType
}

type WechatAccountInput struct {
	StudentID string
	Name      string
	Campus    string
	Cookies   string
	ExpiresAt time.Time
}

type AccountView struct {
	ID                int64      `json:"id"`
	StudentID         string     `json:"studentId"`
	Name              string     `json:"name"`
	LoginType         LoginType  `json:"loginType"`
	Campus            string     `json:"campus"`
	Scheduled         bool       `json:"scheduled"`
	HasPassword       bool       `json:"hasPassword"`
	HasRememberMe     bool       `json:"hasRememberMe"`
	RememberValid     bool       `json:"rememberValid"`
	RememberExpiresAt *time.Time `json:"rememberExpiresAt,omitempty"`
	LastCheckinAt     *time.Time `json:"lastCheckinAt,omitempty"`
	LastStatus        string     `json:"lastStatus,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type Schedule struct {
	Enabled              bool       `json:"enabled"`
	TimeOfDay            string     `json:"timeOfDay"`
	AccountIDs           []int64    `json:"accountIds"`
	MaxRetries           int        `json:"maxRetries"`
	RetryIntervalMinutes int        `json:"retryIntervalMinutes"`
	NotifyEnabled        bool       `json:"notifyEnabled"`
	NotifyEmail          string     `json:"notifyEmail,omitempty"`
	LastRunDate          string     `json:"lastRunDate,omitempty"`
	LastRunAt            *time.Time `json:"lastRunAt,omitempty"`
	LastResult           string     `json:"lastResult,omitempty"`
}

// ScheduledPlan 是调度器每个 tick 需要的数据：某个 OAA 用户的定时配置与参与账号。
type ScheduledPlan struct {
	OwnerID    uint64
	Schedule   Schedule
	AccountIDs []int64
}

type AccountResult struct {
	AccountID         int64     `json:"accountId"`
	StudentID         string    `json:"studentId"`
	AccountName       string    `json:"accountName"`
	Status            string    `json:"status"`
	Message           string    `json:"message"`
	OCRText           string    `json:"ocrText,omitempty"`
	OCRMillis         int64     `json:"ocrMillis,omitempty"`
	TaskID            int64     `json:"taskId,omitempty"`
	TaskName          string    `json:"taskName,omitempty"`
	LocationAddress   string    `json:"locationAddress,omitempty"`
	LocationLatitude  float64   `json:"locationLatitude,omitempty"`
	LocationLongitude float64   `json:"locationLongitude,omitempty"`
	CaptchaSample     string    `json:"captchaSample,omitempty"`
	StartedAt         time.Time `json:"startedAt"`
	FinishedAt        time.Time `json:"finishedAt"`
}

type Run struct {
	ID         int64           `json:"id"`
	Trigger    string          `json:"trigger"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt time.Time       `json:"finishedAt"`
	Summary    string          `json:"summary"`
	Results    []AccountResult `json:"results"`
}

type Snapshot struct {
	Accounts []AccountView
	Schedule Schedule
	Runs     []Run
}

type Store struct {
	db   *sql.DB
	dir  string
	aead cipher.AEAD

	mu          sync.Mutex
	legacy      *legacyData
	legacyTried map[uint64]bool
	legacyError error
}

func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("数据目录不能为空")
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	_ = os.Chmod(dir, 0o700)

	key, err := loadOrCreateKey(filepath.Join(dir, keyFileName))
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-GCM: %w", err)
	}

	path := filepath.Join(dir, dbFileName)
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	// 单写连接：本项目的写入都是低频短事务，串行化可以彻底避免 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		db.Close()
		return nil, fmt.Errorf("设置数据库权限: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{
		db:          db,
		dir:         dir,
		aead:        aead,
		legacyTried: make(map[uint64]bool),
	}
	s.loadLegacy()
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Dir 返回数据目录，验证码留样等文件仍落在文件系统里。
func (s *Store) Dir() string { return s.dir }

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_id INTEGER NOT NULL,
			student_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			login_type TEXT NOT NULL DEFAULT 'password',
			password_enc TEXT NOT NULL DEFAULT '',
			cookies_enc TEXT NOT NULL DEFAULT '',
			cookies_expires_at TEXT,
			campus TEXT NOT NULL DEFAULT '',
			scheduled INTEGER NOT NULL DEFAULT 0,
			last_checkin_at TEXT,
			last_status TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(owner_id, student_id)
		)`,
		`CREATE TABLE IF NOT EXISTS schedules (
			owner_id INTEGER PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 0,
			time_of_day TEXT NOT NULL DEFAULT '07:00:00',
			max_retries INTEGER NOT NULL DEFAULT 3,
			retry_interval_minutes INTEGER NOT NULL DEFAULT 5,
			notify_enabled INTEGER NOT NULL DEFAULT 0,
			notify_email TEXT NOT NULL DEFAULT '',
			last_run_date TEXT NOT NULL DEFAULT '',
			last_run_at TEXT,
			last_result TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_id INTEGER NOT NULL,
			trigger TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_owner ON runs(owner_id, id DESC)`,
		`CREATE TABLE IF NOT EXISTS run_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			account_id INTEGER NOT NULL,
			student_id TEXT NOT NULL DEFAULT '',
			account_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			ocr_text TEXT NOT NULL DEFAULT '',
			ocr_millis INTEGER NOT NULL DEFAULT 0,
			task_id INTEGER NOT NULL DEFAULT 0,
			task_name TEXT NOT NULL DEFAULT '',
			location_address TEXT NOT NULL DEFAULT '',
			location_latitude REAL NOT NULL DEFAULT 0,
			location_longitude REAL NOT NULL DEFAULT 0,
			captcha_sample TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_run_results_run ON run_results(run_id)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("初始化数据库: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("初始化数据库: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("写入数据库版本: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交数据库初始化: %w", err)
	}
	return nil
}

func (s *Store) seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (s *Store) open(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("解码密文: %w", err)
	}
	nonceSize := s.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("密文长度不足")
	}
	plain, err := s.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（密钥可能已更换）: %w", err)
	}
	return string(plain), nil
}

func defaultSchedule() Schedule {
	return Schedule{
		Enabled:              false,
		TimeOfDay:            "07:00:00",
		AccountIDs:           []int64{},
		MaxRetries:           3,
		RetryIntervalMinutes: 5,
	}
}

func normalizeInput(in AccountInput, passwordRequired bool) (AccountInput, error) {
	in.StudentID = strings.TrimSpace(in.StudentID)
	in.Name = strings.TrimSpace(in.Name)
	in.Campus = strings.TrimSpace(in.Campus)
	in.LoginType = normalizeLoginType(in.LoginType)
	if in.StudentID == "" {
		return AccountInput{}, invalid("学号不能为空")
	}
	if len([]rune(in.StudentID)) > 64 {
		return AccountInput{}, invalid("学号过长")
	}
	if in.Name == "" {
		in.Name = in.StudentID
	}
	if len([]rune(in.Name)) > 64 {
		return AccountInput{}, invalid("姓名过长")
	}
	if in.Campus == "" {
		in.Campus = locations.DefaultCampus
	}
	if !locations.HasCampus(in.Campus) {
		return AccountInput{}, invalid("校区必须是宜宾、李白河或汇东")
	}
	if passwordRequired && in.Password == "" {
		return AccountInput{}, invalid("密码不能为空")
	}
	if len([]rune(in.Password)) > 128 {
		return AccountInput{}, invalid("密码过长")
	}
	return in, nil
}

func normalizeWechatInput(in WechatAccountInput) (WechatAccountInput, error) {
	in.StudentID = strings.TrimSpace(in.StudentID)
	in.Name = strings.TrimSpace(in.Name)
	in.Campus = strings.TrimSpace(in.Campus)
	in.Cookies = strings.TrimSpace(in.Cookies)
	if in.StudentID == "" {
		return WechatAccountInput{}, invalid("学号不能为空")
	}
	if len([]rune(in.StudentID)) > 64 {
		return WechatAccountInput{}, invalid("学号过长")
	}
	if in.Name == "" {
		in.Name = in.StudentID
	}
	if len([]rune(in.Name)) > 64 {
		return WechatAccountInput{}, invalid("姓名过长")
	}
	if in.Campus == "" {
		in.Campus = locations.DefaultCampus
	}
	if !locations.HasCampus(in.Campus) {
		return WechatAccountInput{}, invalid("校区必须是宜宾、李白河或汇东")
	}
	if in.Cookies == "" {
		return WechatAccountInput{}, invalid("扫码会话不能为空")
	}
	if len(in.Cookies) > 8192 {
		return WechatAccountInput{}, invalid("扫码会话过长")
	}
	if in.ExpiresAt.IsZero() || !in.ExpiresAt.After(time.Now()) {
		return WechatAccountInput{}, invalid("扫码会话有效期无效")
	}
	return in, nil
}

func normalizeSchedule(in Schedule) (Schedule, error) {
	timeOfDay, err := normalizeTimeOfDay(in.TimeOfDay)
	if err != nil {
		return Schedule{}, err
	}
	if in.MaxRetries < 0 || in.MaxRetries > 10 {
		return Schedule{}, invalid("重试次数需在 0 到 10 之间")
	}
	if in.RetryIntervalMinutes < 1 || in.RetryIntervalMinutes > 60 {
		return Schedule{}, invalid("重试间隔需在 1 到 60 分钟之间")
	}
	in.TimeOfDay = timeOfDay
	in.NotifyEmail = strings.TrimSpace(in.NotifyEmail)
	if in.NotifyEnabled && in.NotifyEmail == "" {
		return Schedule{}, invalid("开启失败通知后必须填写通知邮箱")
	}
	if in.NotifyEmail != "" {
		addr, err := mail.ParseAddress(in.NotifyEmail)
		if err != nil || len(addr.Address) > 254 {
			return Schedule{}, invalid("通知邮箱格式不正确")
		}
		in.NotifyEmail = addr.Address
	}
	return in, nil
}

func normalizeTimeOfDay(v string) (string, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("15:04:05"), nil
		}
	}
	return "", invalid("定时时间格式应为 HH:MM 或 HH:MM:SS")
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func toView(a Account) AccountView {
	rememberValid := a.RememberCookiesEnc != "" && a.RememberExpiresAt != nil && a.RememberExpiresAt.After(time.Now())
	return AccountView{
		ID:                a.ID,
		StudentID:         a.StudentID,
		Name:              a.Name,
		LoginType:         normalizeLoginType(a.LoginType),
		Campus:            a.Campus,
		Scheduled:         a.Scheduled,
		HasPassword:       a.PasswordEnc != "",
		HasRememberMe:     a.RememberCookiesEnc != "",
		RememberValid:     rememberValid,
		RememberExpiresAt: copyTimePtr(a.RememberExpiresAt),
		LastCheckinAt:     copyTimePtr(a.LastCheckinAt),
		LastStatus:        a.LastStatus,
		CreatedAt:         a.CreatedAt,
		UpdatedAt:         a.UpdatedAt,
	}
}

func copySchedule(in Schedule) Schedule {
	out := in
	out.AccountIDs = append([]int64(nil), in.AccountIDs...)
	out.LastRunAt = copyTimePtr(in.LastRunAt)
	return out
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func cloneResults(in []AccountResult) []AccountResult {
	return append([]AccountResult(nil), in...)
}

func copyRun(in Run) Run {
	out := in
	out.Results = cloneResults(in.Results)
	return out
}

func truncate(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func parseTimePtr(raw sql.NullString) *time.Time {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	t := parseTime(raw.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

func loadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != 32 {
			return nil, fmt.Errorf("密钥文件 %s 长度异常（%d 字节）", path, len(raw))
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取密钥: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成密钥: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateKey(path)
		}
		return nil, fmt.Errorf("创建密钥: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, fmt.Errorf("写入密钥: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("同步密钥: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("关闭密钥: %w", err)
	}
	return key, nil
}
