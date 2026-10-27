package store

import (
	"database/sql"
	"fmt"
	"strings"
)

const runResultColumns = `run_id, account_id, student_id, account_name, status, message,
	ocr_text, ocr_millis, task_id, task_name, location_address, location_latitude,
	location_longitude, captcha_sample, started_at, finished_at`

func (s *Store) AppendRun(ownerID uint64, in Run) (Run, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, fmt.Errorf("保存运行记录: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO runs (owner_id, trigger, started_at, finished_at, summary) VALUES (?, ?, ?, ?, ?)`,
		ownerID, in.Trigger, formatTime(in.StartedAt), formatTime(in.FinishedAt), in.Summary,
	)
	if err != nil {
		return Run{}, fmt.Errorf("保存运行记录: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return Run{}, fmt.Errorf("保存运行记录: %w", err)
	}
	in.ID = runID
	for _, r := range in.Results {
		if _, err := tx.Exec(
			`INSERT INTO run_results (run_id, account_id, student_id, account_name, status, message,
				ocr_text, ocr_millis, task_id, task_name, location_address, location_latitude,
				location_longitude, captcha_sample, started_at, finished_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, r.AccountID, r.StudentID, r.AccountName, r.Status, r.Message,
			r.OCRText, r.OCRMillis, r.TaskID, r.TaskName, r.LocationAddress,
			r.LocationLatitude, r.LocationLongitude, r.CaptchaSample,
			formatTime(r.StartedAt), formatTime(r.FinishedAt),
		); err != nil {
			return Run{}, fmt.Errorf("保存运行结果: %w", err)
		}
	}

	// 每个用户只保留最近 maxRuns 条运行记录，级联删除对应结果。
	var cutoff sql.NullInt64
	if err := tx.QueryRow(
		`SELECT id FROM runs WHERE owner_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?`,
		ownerID, maxRuns-1,
	).Scan(&cutoff); err != nil && err != sql.ErrNoRows {
		return Run{}, fmt.Errorf("清理运行记录: %w", err)
	}
	if cutoff.Valid {
		if _, err := tx.Exec(`DELETE FROM runs WHERE owner_id = ? AND id < ?`, ownerID, cutoff.Int64); err != nil {
			return Run{}, fmt.Errorf("清理运行记录: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("保存运行记录: %w", err)
	}
	return copyRun(in), nil
}

func (s *Store) runs(ownerID uint64, limit int) ([]Run, error) {
	if limit <= 0 || limit > maxRuns {
		limit = maxRuns
	}
	rows, err := s.db.Query(
		`SELECT id, trigger, started_at, finished_at, summary FROM runs
		 WHERE owner_id = ? ORDER BY id DESC LIMIT ?`,
		ownerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("读取运行记录: %w", err)
	}
	defer rows.Close()

	runs := make([]Run, 0, limit)
	index := make(map[int64]int, limit)
	for rows.Next() {
		var (
			run               Run
			started, finished string
		)
		if err := rows.Scan(&run.ID, &run.Trigger, &started, &finished, &run.Summary); err != nil {
			return nil, fmt.Errorf("读取运行记录: %w", err)
		}
		run.StartedAt = parseTime(started)
		run.FinishedAt = parseTime(finished)
		run.Results = []AccountResult{}
		index[run.ID] = len(runs)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取运行记录: %w", err)
	}
	// 连接池只有一个连接，必须先释放 rows 才能发下一条查询。
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("读取运行记录: %w", err)
	}
	if len(runs) == 0 {
		return runs, nil
	}

	ids := make([]any, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	resultRows, err := s.db.Query(
		`SELECT `+runResultColumns+` FROM run_results WHERE run_id IN (`+placeholders+`) ORDER BY id`,
		ids...,
	)
	if err != nil {
		return nil, fmt.Errorf("读取运行结果: %w", err)
	}
	defer resultRows.Close()
	for resultRows.Next() {
		var (
			r                 AccountResult
			runID             int64
			started, finished string
		)
		if err := resultRows.Scan(
			&runID, &r.AccountID, &r.StudentID, &r.AccountName, &r.Status, &r.Message,
			&r.OCRText, &r.OCRMillis, &r.TaskID, &r.TaskName, &r.LocationAddress,
			&r.LocationLatitude, &r.LocationLongitude, &r.CaptchaSample, &started, &finished,
		); err != nil {
			return nil, fmt.Errorf("读取运行结果: %w", err)
		}
		r.StartedAt = parseTime(started)
		r.FinishedAt = parseTime(finished)
		if i, ok := index[runID]; ok {
			runs[i].Results = append(runs[i].Results, r)
		}
	}
	return runs, resultRows.Err()
}

func (s *Store) Snapshot(ownerID uint64, runLimit int) Snapshot {
	accounts, err := s.accounts(ownerID)
	if err != nil {
		accounts = nil
	}
	views := make([]AccountView, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, toView(a))
	}
	runs, err := s.runs(ownerID, runLimit)
	if err != nil {
		runs = nil
	}
	return Snapshot{
		Accounts: views,
		Schedule: s.Schedule(ownerID),
		Runs:     runs,
	}
}
