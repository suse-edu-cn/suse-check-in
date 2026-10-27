package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// legacyData 是旧版单文件存储（state.json）的结构，只用于一次性迁移。
type legacyData struct {
	Version  int             `json:"version"`
	Accounts []legacyAccount `json:"accounts"`
	Schedule legacySchedule  `json:"schedule"`
	Runs     []legacyRun     `json:"runs"`
}

type legacyAccount struct {
	ID                 int64      `json:"id"`
	StudentID          string     `json:"studentId"`
	Name               string     `json:"name"`
	LoginType          LoginType  `json:"loginType"`
	PasswordEnc        string     `json:"passwordEnc"`
	RememberCookiesEnc string     `json:"rememberCookiesEnc"`
	RememberExpiresAt  *time.Time `json:"rememberExpiresAt"`
	Campus             string     `json:"campus"`
	LastCheckinAt      *time.Time `json:"lastCheckinAt"`
	LastStatus         string     `json:"lastStatus"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type legacySchedule struct {
	Enabled              bool       `json:"enabled"`
	TimeOfDay            string     `json:"timeOfDay"`
	AccountIDs           []int64    `json:"accountIds"`
	MaxRetries           int        `json:"maxRetries"`
	RetryIntervalMinutes int        `json:"retryIntervalMinutes"`
	NotifyEnabled        bool       `json:"notifyEnabled"`
	NotifyEmail          string     `json:"notifyEmail"`
	LastRunDate          string     `json:"lastRunDate"`
	LastRunAt            *time.Time `json:"lastRunAt"`
	LastResult           string     `json:"lastResult"`
}

type legacyRun struct {
	ID         int64           `json:"id"`
	Trigger    string          `json:"trigger"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt time.Time       `json:"finishedAt"`
	Summary    string          `json:"summary"`
	Results    []AccountResult `json:"results"`
}

// LoadLegacyError 返回启动时读取旧存储的错误，供 main 在日志里提示。
func (s *Store) LoadLegacyError() error { return s.legacyError }

// loadLegacy 读取旧版 state.json；已经迁移过或文件不存在时保持 legacy 为空。
// 迁移成功前不会删除原文件，失败也能从备份恢复。
func (s *Store) loadLegacy() {
	var claimed string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'legacy_claimed'`).Scan(&claimed)
	if err == nil {
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		s.legacyError = fmt.Errorf("读取迁移标记: %w", err)
		return
	}
	path := filepath.Join(s.dir, legacyFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.legacyError = fmt.Errorf("读取旧版 %s: %w", legacyFileName, err)
		return
	}
	var data legacyData
	if err := json.Unmarshal(raw, &data); err != nil {
		s.legacyError = fmt.Errorf("解析旧版 %s: %w", legacyFileName, err)
		return
	}
	s.legacy = &data
}

// LegacyPending 表示还有旧数据等待第一次登录时认领。
func (s *Store) LegacyPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.legacy != nil && len(s.legacy.Accounts) > 0
}

// ClaimLegacy 把旧版单文件数据整体迁移给 ownerID（通常是迁移后第一个登录的
// OAA 用户）。返回 true 表示本次确实写入了数据。重复调用是幂等的。
func (s *Store) ClaimLegacy(ownerID uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.legacy == nil || s.legacyTried[ownerID] {
		return false, nil
	}
	s.legacyTried[ownerID] = true

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("迁移旧数据: %w", err)
	}
	defer tx.Rollback()

	var claimed string
	err = tx.QueryRow(`SELECT value FROM meta WHERE key = 'legacy_claimed'`).Scan(&claimed)
	if err == nil {
		s.legacy = nil
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("读取迁移标记: %w", err)
	}

	var existing int
	if err := tx.QueryRow(
		`SELECT (SELECT COUNT(*) FROM accounts WHERE owner_id = ?) + (SELECT COUNT(*) FROM runs WHERE owner_id = ?)`,
		ownerID, ownerID,
	).Scan(&existing); err != nil {
		return false, fmt.Errorf("检查现有数据: %w", err)
	}
	if existing > 0 {
		return false, nil
	}

	scheduled := make(map[int64]bool, len(s.legacy.Schedule.AccountIDs))
	for _, id := range s.legacy.Schedule.AccountIDs {
		scheduled[id] = true
	}
	for _, a := range s.legacy.Accounts {
		created, updated := a.CreatedAt, a.UpdatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		if updated.IsZero() {
			updated = created
		}
		if _, err := tx.Exec(
			`INSERT INTO accounts (id, owner_id, student_id, name, login_type, password_enc, cookies_enc,
				cookies_expires_at, campus, scheduled, last_checkin_at, last_status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, ownerID, a.StudentID, a.Name, string(normalizeLoginType(a.LoginType)),
			a.PasswordEnc, a.RememberCookiesEnc, formatTimePtr(a.RememberExpiresAt), a.Campus,
			boolInt(scheduled[a.ID]), formatTimePtr(a.LastCheckinAt), a.LastStatus,
			formatTime(created), formatTime(updated),
		); err != nil {
			return false, fmt.Errorf("迁移账号 %s: %w", a.StudentID, err)
		}
	}

	sch := s.legacy.Schedule
	if sch.TimeOfDay == "" {
		sch.TimeOfDay = "07:00:00"
	}
	if sch.RetryIntervalMinutes <= 0 {
		sch.RetryIntervalMinutes = 5
	}
	if _, err := tx.Exec(
		`INSERT INTO schedules (owner_id, enabled, time_of_day, max_retries, retry_interval_minutes,
			notify_enabled, notify_email, last_run_date, last_run_at, last_result, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ownerID, boolInt(sch.Enabled), sch.TimeOfDay, sch.MaxRetries, sch.RetryIntervalMinutes,
		boolInt(sch.NotifyEnabled), sch.NotifyEmail, sch.LastRunDate, formatTimePtr(sch.LastRunAt),
		sch.LastResult, formatTime(time.Now().UTC()),
	); err != nil {
		return false, fmt.Errorf("迁移定时设置: %w", err)
	}

	for _, r := range s.legacy.Runs {
		if _, err := tx.Exec(
			`INSERT INTO runs (id, owner_id, trigger, started_at, finished_at, summary) VALUES (?, ?, ?, ?, ?, ?)`,
			r.ID, ownerID, r.Trigger, formatTime(r.StartedAt), formatTime(r.FinishedAt), r.Summary,
		); err != nil {
			return false, fmt.Errorf("迁移运行记录: %w", err)
		}
		for _, item := range r.Results {
			if _, err := tx.Exec(
				`INSERT INTO run_results (run_id, account_id, student_id, account_name, status, message,
					ocr_text, ocr_millis, task_id, task_name, location_address, location_latitude,
					location_longitude, captcha_sample, started_at, finished_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				r.ID, item.AccountID, item.StudentID, item.AccountName, item.Status, item.Message,
				item.OCRText, item.OCRMillis, item.TaskID, item.TaskName, item.LocationAddress,
				item.LocationLatitude, item.LocationLongitude, item.CaptchaSample,
				formatTime(item.StartedAt), formatTime(item.FinishedAt),
			); err != nil {
				return false, fmt.Errorf("迁移运行结果: %w", err)
			}
		}
	}

	markers := map[string]string{
		"legacy_claimed":    "1",
		"legacy_claimed_by": fmt.Sprintf("%d", ownerID),
		"legacy_claimed_at": time.Now().UTC().Format(time.RFC3339),
	}
	for key, value := range markers {
		if _, err := tx.Exec(
			`INSERT INTO meta (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			key, value,
		); err != nil {
			return false, fmt.Errorf("写入迁移标记: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("提交迁移: %w", err)
	}
	s.legacy = nil
	return true, nil
}
