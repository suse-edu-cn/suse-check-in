package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"suse-checkin/internal/locations"
)

const (
	stateFileName = "state.json"
	keyFileName   = "key"
	dataVersion   = 1
	maxRuns       = 200
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
	LoginType          LoginType  `json:"loginType,omitempty"`
	PasswordEnc        string     `json:"passwordEnc,omitempty"`
	RememberCookiesEnc string     `json:"rememberCookiesEnc,omitempty"`
	RememberExpiresAt  *time.Time `json:"rememberExpiresAt,omitempty"`
	Campus             string     `json:"campus"`
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

type dataFile struct {
	Version       int       `json:"version"`
	Accounts      []Account `json:"accounts"`
	Schedule      Schedule  `json:"schedule"`
	Runs          []Run     `json:"runs"`
	NextAccountID int64     `json:"nextAccountId"`
	NextRunID     int64     `json:"nextRunId"`
}

type Store struct {
	mu   sync.Mutex
	dir  string
	path string
	aead cipher.AEAD
	data dataFile
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

	s := &Store{
		dir:  dir,
		path: filepath.Join(dir, stateFileName),
		aead: aead,
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.data = dataFile{
			Version:       dataVersion,
			Schedule:      defaultSchedule(),
			NextAccountID: 1,
			NextRunID:     1,
		}
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", stateFileName, err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", stateFileName, err)
	}
	if s.data.Version == 0 {
		s.data.Version = dataVersion
	}
	if s.data.Version > dataVersion {
		return nil, fmt.Errorf("状态文件版本 %d 高于程序支持的 %d", s.data.Version, dataVersion)
	}
	s.normalizeLocked()
	return s, nil
}

func defaultSchedule() Schedule {
	return Schedule{
		Enabled:              false,
		TimeOfDay:            "07:00:00",
		MaxRetries:           3,
		RetryIntervalMinutes: 5,
		NotifyEnabled:        false,
	}
}

func (s *Store) AddAccount(in AccountInput) (AccountView, error) {
	norm, err := normalizeInput(in, true)
	if err != nil {
		return AccountView{}, err
	}
	if norm.LoginType != LoginPassword {
		return AccountView{}, invalid("新增密码账号时登录方式必须是密码登录")
	}
	enc, err := s.seal(norm.Password)
	if err != nil {
		return AccountView{}, fmt.Errorf("加密密码: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Accounts {
		if a.StudentID == norm.StudentID {
			return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在", ErrConflict, norm.StudentID)
		}
	}
	now := time.Now().UTC()
	acc := Account{
		ID:          s.data.NextAccountID,
		StudentID:   norm.StudentID,
		Name:        norm.Name,
		LoginType:   LoginPassword,
		PasswordEnc: enc,
		Campus:      norm.Campus,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.data.NextAccountID++
	s.data.Accounts = append(s.data.Accounts, acc)
	if err := s.persistLocked(); err != nil {
		return AccountView{}, err
	}
	return toView(acc), nil
}

func (s *Store) AddWechatAccount(in WechatAccountInput) (AccountView, error) {
	norm, err := normalizeWechatInput(in)
	if err != nil {
		return AccountView{}, err
	}
	enc, err := s.seal(norm.Cookies)
	if err != nil {
		return AccountView{}, fmt.Errorf("加密扫码会话: %w", err)
	}
	expires := norm.ExpiresAt.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		a := &s.data.Accounts[i]
		if a.StudentID != norm.StudentID {
			continue
		}
		if normalizeLoginType(a.LoginType) != LoginWechat {
			return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在密码登录账号", ErrConflict, norm.StudentID)
		}
		a.Name = norm.Name
		a.Campus = norm.Campus
		a.RememberCookiesEnc = enc
		a.RememberExpiresAt = &expires
		a.UpdatedAt = time.Now().UTC()
		if err := s.persistLocked(); err != nil {
			return AccountView{}, err
		}
		return toView(*a), nil
	}
	now := time.Now().UTC()
	acc := Account{
		ID:                 s.data.NextAccountID,
		StudentID:          norm.StudentID,
		Name:               norm.Name,
		LoginType:          LoginWechat,
		RememberCookiesEnc: enc,
		RememberExpiresAt:  &expires,
		Campus:             norm.Campus,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	s.data.NextAccountID++
	s.data.Accounts = append(s.data.Accounts, acc)
	if err := s.persistLocked(); err != nil {
		return AccountView{}, err
	}
	return toView(acc), nil
}

func (s *Store) UpdateWechatSession(id int64, name, campus, cookies string, expires time.Time) (AccountView, error) {
	in := WechatAccountInput{
		Name:      name,
		Campus:    campus,
		Cookies:   cookies,
		ExpiresAt: expires,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.accountIndexLocked(id)
	if idx < 0 {
		return AccountView{}, fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	cur := &s.data.Accounts[idx]
	if normalizeLoginType(cur.LoginType) != LoginWechat {
		return AccountView{}, invalid("该账号不是扫码登录账号")
	}
	in.StudentID = cur.StudentID
	norm, err := normalizeWechatInput(in)
	if err != nil {
		return AccountView{}, err
	}
	enc, err := s.seal(norm.Cookies)
	if err != nil {
		return AccountView{}, fmt.Errorf("加密扫码会话: %w", err)
	}
	exp := norm.ExpiresAt.UTC()
	cur.Name = norm.Name
	cur.Campus = norm.Campus
	cur.RememberCookiesEnc = enc
	cur.RememberExpiresAt = &exp
	cur.UpdatedAt = time.Now().UTC()
	if err := s.persistLocked(); err != nil {
		return AccountView{}, err
	}
	return toView(*cur), nil
}

func (s *Store) UpdateAccount(id int64, in AccountInput) (AccountView, error) {
	norm, err := normalizeInput(in, false)
	if err != nil {
		return AccountView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.accountIndexLocked(id)
	if idx < 0 {
		return AccountView{}, fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	cur := &s.data.Accounts[idx]
	curType := normalizeLoginType(cur.LoginType)
	if curType == LoginWechat {
		if norm.Password != "" {
			return AccountView{}, invalid("扫码登录账号不能设置密码")
		}
		if norm.LoginType != LoginWechat {
			return AccountView{}, invalid("不能修改扫码账号的登录方式")
		}
		if norm.StudentID != cur.StudentID {
			return AccountView{}, invalid("扫码登录账号不能修改学号")
		}
		cur.Name = norm.Name
		cur.Campus = norm.Campus
		cur.UpdatedAt = time.Now().UTC()
		if err := s.persistLocked(); err != nil {
			return AccountView{}, err
		}
		return toView(*cur), nil
	}
	if norm.LoginType != LoginPassword {
		return AccountView{}, invalid("不能修改密码账号的登录方式")
	}
	for i, a := range s.data.Accounts {
		if i != idx && a.StudentID == norm.StudentID {
			return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在", ErrConflict, norm.StudentID)
		}
	}
	studentChanged := cur.StudentID != norm.StudentID
	cur.StudentID = norm.StudentID
	cur.Name = norm.Name
	cur.Campus = norm.Campus
	cur.UpdatedAt = time.Now().UTC()
	if norm.Password != "" {
		enc, err := s.seal(norm.Password)
		if err != nil {
			return AccountView{}, fmt.Errorf("加密密码: %w", err)
		}
		cur.PasswordEnc = enc
		cur.RememberCookiesEnc = ""
		cur.RememberExpiresAt = nil
	}
	if studentChanged {
		cur.RememberCookiesEnc = ""
		cur.RememberExpiresAt = nil
		cur.LastCheckinAt = nil
		cur.LastStatus = ""
	}
	if err := s.persistLocked(); err != nil {
		return AccountView{}, err
	}
	return toView(*cur), nil
}

func (s *Store) DeleteAccount(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.accountIndexLocked(id)
	if idx < 0 {
		return fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	s.data.Accounts = append(s.data.Accounts[:idx], s.data.Accounts[idx+1:]...)
	kept := make([]int64, 0, len(s.data.Schedule.AccountIDs))
	for _, accountID := range s.data.Schedule.AccountIDs {
		if accountID != id {
			kept = append(kept, accountID)
		}
	}
	s.data.Schedule.AccountIDs = kept
	return s.persistLocked()
}

func (s *Store) Account(id int64) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.accountIndexLocked(id)
	if idx < 0 {
		return Account{}, false
	}
	return s.data.Accounts[idx], true
}

func (s *Store) Password(acc Account) (string, error) {
	return s.open(acc.PasswordEnc)
}

func (s *Store) RememberCookies(acc Account) (string, error) {
	return s.open(acc.RememberCookiesEnc)
}

func (s *Store) SaveRememberCookies(id int64, cookies string, expires time.Time) error {
	if strings.TrimSpace(cookies) == "" {
		return nil
	}
	enc, err := s.seal(cookies)
	if err != nil {
		return fmt.Errorf("加密登录会话: %w", err)
	}
	exp := expires.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.accountIndexLocked(id)
	if idx < 0 {
		return fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	s.data.Accounts[idx].RememberCookiesEnc = enc
	s.data.Accounts[idx].RememberExpiresAt = &exp
	s.data.Accounts[idx].UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Store) UpdateCheckinStatus(id int64, status string, at time.Time) error {
	at = at.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.accountIndexLocked(id)
	if idx < 0 {
		return fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	s.data.Accounts[idx].LastCheckinAt = &at
	s.data.Accounts[idx].LastStatus = truncate(status, 500)
	s.data.Accounts[idx].UpdatedAt = at
	return s.persistLocked()
}

func (s *Store) Schedule() Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copySchedule(s.data.Schedule)
}

func (s *Store) SaveSchedule(in Schedule) (Schedule, error) {
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

	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[int64]bool, len(in.AccountIDs))
	ids := make([]int64, 0, len(in.AccountIDs))
	for _, id := range in.AccountIDs {
		if s.accountIndexLocked(id) < 0 {
			return Schedule{}, invalid("账号 %d 不存在", id)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	in.TimeOfDay = timeOfDay
	in.AccountIDs = ids
	in.LastRunDate = s.data.Schedule.LastRunDate
	in.LastRunAt = copyTimePtr(s.data.Schedule.LastRunAt)
	in.LastResult = s.data.Schedule.LastResult
	s.data.Schedule = copySchedule(in)
	if err := s.persistLocked(); err != nil {
		return Schedule{}, err
	}
	return copySchedule(in), nil
}

func (s *Store) MarkScheduledRun(date string, at time.Time, result string) error {
	at = at.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Schedule.LastRunDate = date
	s.data.Schedule.LastRunAt = &at
	s.data.Schedule.LastResult = truncate(result, 1000)
	return s.persistLocked()
}

func (s *Store) AppendRun(in Run) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in.ID = s.data.NextRunID
	s.data.NextRunID++
	in.Results = cloneResults(in.Results)
	s.data.Runs = append(s.data.Runs, in)
	if len(s.data.Runs) > maxRuns {
		s.data.Runs = append([]Run(nil), s.data.Runs[len(s.data.Runs)-maxRuns:]...)
	}
	if err := s.persistLocked(); err != nil {
		return in, err
	}
	return copyRun(in), nil
}

func (s *Store) Snapshot(runLimit int) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	accounts := make([]AccountView, 0, len(s.data.Accounts))
	for _, a := range s.data.Accounts {
		accounts = append(accounts, toView(a))
	}
	if runLimit <= 0 || runLimit > len(s.data.Runs) {
		runLimit = len(s.data.Runs)
	}
	runs := make([]Run, 0, runLimit)
	for i := len(s.data.Runs) - 1; i >= 0 && len(runs) < runLimit; i-- {
		runs = append(runs, copyRun(s.data.Runs[i]))
	}
	return Snapshot{
		Accounts: accounts,
		Schedule: copySchedule(s.data.Schedule),
		Runs:     runs,
	}
}

func (s *Store) accountIndexLocked(id int64) int {
	for i, a := range s.data.Accounts {
		if a.ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) normalizeLocked() {
	if s.data.Schedule.TimeOfDay == "" {
		s.data.Schedule.TimeOfDay = "07:00:00"
	}
	if s.data.Schedule.RetryIntervalMinutes <= 0 {
		s.data.Schedule.RetryIntervalMinutes = 5
	}
	if s.data.Schedule.MaxRetries < 0 {
		s.data.Schedule.MaxRetries = 0
	}
	if s.data.Schedule.MaxRetries > 10 {
		s.data.Schedule.MaxRetries = 10
	}
	if s.data.Schedule.AccountIDs == nil {
		s.data.Schedule.AccountIDs = []int64{}
	}
	var maxAccountID, maxRunID int64
	for i := range s.data.Accounts {
		a := &s.data.Accounts[i]
		a.LoginType = normalizeLoginType(a.LoginType)
		if a.ID > maxAccountID {
			maxAccountID = a.ID
		}
	}
	for _, r := range s.data.Runs {
		if r.ID > maxRunID {
			maxRunID = r.ID
		}
	}
	if s.data.NextAccountID <= maxAccountID {
		s.data.NextAccountID = maxAccountID + 1
	}
	if s.data.NextRunID <= maxRunID {
		s.data.NextRunID = maxRunID + 1
	}
	if len(s.data.Runs) > maxRuns {
		s.data.Runs = append([]Run(nil), s.data.Runs[len(s.data.Runs)-maxRuns:]...)
	}
}

func (s *Store) persistLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时状态文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("设置临时文件权限: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("写入临时状态文件: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步临时状态文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时状态文件: %w", err)
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(s.path)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("替换状态文件: %w", err)
	}
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
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
