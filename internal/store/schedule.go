package store

import (
	"database/sql"
	"fmt"
	"time"
)

const scheduleColumns = `enabled, time_of_day, max_retries, retry_interval_minutes,
	notify_enabled, notify_email, last_run_date, last_run_at, last_result`

func scanSchedule(sc rowScanner) (Schedule, error) {
	var (
		sch          Schedule
		enabled      int
		notify       int
		lastRunAtRaw sql.NullString
	)
	err := sc.Scan(
		&enabled, &sch.TimeOfDay, &sch.MaxRetries, &sch.RetryIntervalMinutes,
		&notify, &sch.NotifyEmail, &sch.LastRunDate, &lastRunAtRaw, &sch.LastResult,
	)
	if err != nil {
		return Schedule{}, err
	}
	sch.Enabled = enabled != 0
	sch.NotifyEnabled = notify != 0
	sch.LastRunAt = parseTimePtr(lastRunAtRaw)
	sch.AccountIDs = []int64{}
	return sch, nil
}

func (s *Store) scheduledAccountIDs(ownerID uint64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM accounts WHERE owner_id = ? AND scheduled = 1 ORDER BY id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("读取定时账号: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0, 4)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取定时账号: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Schedule 返回某个 OAA 用户的定时配置，未配置过时返回默认值。
func (s *Store) Schedule(ownerID uint64) Schedule {
	sch := defaultSchedule()
	row := s.db.QueryRow(`SELECT `+scheduleColumns+` FROM schedules WHERE owner_id = ?`, ownerID)
	if got, err := scanSchedule(row); err == nil {
		sch = got
	}
	if ids, err := s.scheduledAccountIDs(ownerID); err == nil {
		sch.AccountIDs = ids
	}
	return sch
}

func (s *Store) SaveSchedule(ownerID uint64, in Schedule) (Schedule, error) {
	norm, err := normalizeSchedule(in)
	if err != nil {
		return Schedule{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Schedule{}, fmt.Errorf("保存定时设置: %w", err)
	}
	defer tx.Rollback()

	seen := make(map[int64]bool, len(in.AccountIDs))
	ids := make([]int64, 0, len(in.AccountIDs))
	for _, id := range in.AccountIDs {
		if seen[id] {
			continue
		}
		var exists int
		err := tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ? AND owner_id = ?`, id, ownerID).Scan(&exists)
		if err != nil {
			return Schedule{}, fmt.Errorf("校验定时账号: %w", err)
		}
		if exists == 0 {
			return Schedule{}, invalid("账号 %d 不存在", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}

	if _, err := tx.Exec(
		`INSERT INTO schedules (owner_id, enabled, time_of_day, max_retries, retry_interval_minutes,
			notify_enabled, notify_email, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(owner_id) DO UPDATE SET
			enabled = excluded.enabled,
			time_of_day = excluded.time_of_day,
			max_retries = excluded.max_retries,
			retry_interval_minutes = excluded.retry_interval_minutes,
			notify_enabled = excluded.notify_enabled,
			notify_email = excluded.notify_email,
			updated_at = excluded.updated_at`,
		ownerID, boolInt(norm.Enabled), norm.TimeOfDay, norm.MaxRetries, norm.RetryIntervalMinutes,
		boolInt(norm.NotifyEnabled), norm.NotifyEmail, formatTime(time.Now().UTC()),
	); err != nil {
		return Schedule{}, fmt.Errorf("保存定时设置: %w", err)
	}

	if _, err := tx.Exec(`UPDATE accounts SET scheduled = 0 WHERE owner_id = ?`, ownerID); err != nil {
		return Schedule{}, fmt.Errorf("更新定时账号: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE accounts SET scheduled = 1 WHERE id = ? AND owner_id = ?`, id, ownerID); err != nil {
			return Schedule{}, fmt.Errorf("更新定时账号: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Schedule{}, fmt.Errorf("保存定时设置: %w", err)
	}
	return s.Schedule(ownerID), nil
}

func (s *Store) MarkScheduledRun(ownerID uint64, date string, at time.Time, result string) error {
	if _, err := s.db.Exec(
		`INSERT INTO schedules (owner_id, last_run_date, last_run_at, last_result, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(owner_id) DO UPDATE SET
			last_run_date = excluded.last_run_date,
			last_run_at = excluded.last_run_at,
			last_result = excluded.last_result,
			updated_at = excluded.updated_at`,
		ownerID, date, formatTime(at), truncate(result, 1000), formatTime(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("保存定时结果: %w", err)
	}
	return nil
}

// ScheduledPlans 返回所有启用定时的用户及其账号，供调度器每个 tick 遍历。
// 单条 JOIN 查询完成：连接池只有一个写连接，遍历 rows 时不能再发新查询。
func (s *Store) ScheduledPlans() ([]ScheduledPlan, error) {
	rows, err := s.db.Query(`SELECT s.owner_id, s.enabled, s.time_of_day, s.max_retries, s.retry_interval_minutes,
			s.notify_enabled, s.notify_email, s.last_run_date, s.last_run_at, s.last_result, a.id
		FROM schedules s
		LEFT JOIN accounts a ON a.owner_id = s.owner_id AND a.scheduled = 1
		WHERE s.enabled = 1
		ORDER BY s.owner_id, a.id`)
	if err != nil {
		return nil, fmt.Errorf("读取定时计划: %w", err)
	}
	defer rows.Close()

	plans := make([]ScheduledPlan, 0, 4)
	index := make(map[uint64]int, 4)
	for rows.Next() {
		var (
			ownerID      uint64
			sch          Schedule
			enabled      int
			notify       int
			lastRunAtRaw sql.NullString
			accountID    sql.NullInt64
		)
		if err := rows.Scan(
			&ownerID, &enabled, &sch.TimeOfDay, &sch.MaxRetries, &sch.RetryIntervalMinutes,
			&notify, &sch.NotifyEmail, &sch.LastRunDate, &lastRunAtRaw, &sch.LastResult, &accountID,
		); err != nil {
			return nil, fmt.Errorf("读取定时计划: %w", err)
		}
		sch.Enabled = enabled != 0
		sch.NotifyEnabled = notify != 0
		sch.LastRunAt = parseTimePtr(lastRunAtRaw)

		i, ok := index[ownerID]
		if !ok {
			sch.AccountIDs = []int64{}
			plans = append(plans, ScheduledPlan{OwnerID: ownerID, Schedule: sch})
			i = len(plans) - 1
			index[ownerID] = i
		}
		if accountID.Valid {
			plans[i].AccountIDs = append(plans[i].AccountIDs, accountID.Int64)
			plans[i].Schedule.AccountIDs = plans[i].AccountIDs
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取定时计划: %w", err)
	}
	return plans, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
