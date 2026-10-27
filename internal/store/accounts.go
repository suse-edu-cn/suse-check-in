package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlite "modernc.org/sqlite"
)

const accountColumns = `id, student_id, name, login_type, password_enc, cookies_enc,
	cookies_expires_at, campus, scheduled, last_checkin_at, last_status, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(sc rowScanner) (Account, error) {
	var (
		a                           Account
		loginType, created, updated string
		cookiesExpires, lastCheckin sql.NullString
		scheduled                   int
	)
	err := sc.Scan(
		&a.ID, &a.StudentID, &a.Name, &loginType, &a.PasswordEnc, &a.RememberCookiesEnc,
		&cookiesExpires, &a.Campus, &scheduled, &lastCheckin, &a.LastStatus, &created, &updated,
	)
	if err != nil {
		return Account{}, err
	}
	a.LoginType = normalizeLoginType(LoginType(loginType))
	a.RememberExpiresAt = parseTimePtr(cookiesExpires)
	a.LastCheckinAt = parseTimePtr(lastCheckin)
	a.Scheduled = scheduled != 0
	a.CreatedAt = parseTime(created)
	a.UpdatedAt = parseTime(updated)
	return a, nil
}

func isUniqueViolation(err error) bool {
	var apiErr *sqlite.Error
	if errors.As(err, &apiErr) {
		code := apiErr.Code()
		return code == 2067 || code == 1555
	}
	return false
}

func (s *Store) accountTx(tx *sql.Tx, ownerID, id int64) (Account, error) {
	row := tx.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ? AND owner_id = ?`, id, ownerID)
	acc, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	if err != nil {
		return Account{}, err
	}
	return acc, nil
}

func (s *Store) accountView(ownerID, id int64) (AccountView, error) {
	row := s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ? AND owner_id = ?`, id, ownerID)
	acc, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountView{}, fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	if err != nil {
		return AccountView{}, err
	}
	return toView(acc), nil
}

func (s *Store) AddAccount(ownerID uint64, in AccountInput) (AccountView, error) {
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
	now := time.Now().UTC()
	res, err := s.db.Exec(
		`INSERT INTO accounts (owner_id, student_id, name, login_type, password_enc, campus, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ownerID, norm.StudentID, norm.Name, string(LoginPassword), enc, norm.Campus, formatTime(now), formatTime(now),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在", ErrConflict, norm.StudentID)
		}
		return AccountView{}, fmt.Errorf("保存账号: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return AccountView{}, fmt.Errorf("读取账号 ID: %w", err)
	}
	return s.accountView(int64(ownerID), id)
}

func (s *Store) AddWechatAccount(ownerID uint64, in WechatAccountInput) (AccountView, error) {
	norm, err := normalizeWechatInput(in)
	if err != nil {
		return AccountView{}, err
	}
	enc, err := s.seal(norm.Cookies)
	if err != nil {
		return AccountView{}, fmt.Errorf("加密扫码会话: %w", err)
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return AccountView{}, fmt.Errorf("保存扫码账号: %w", err)
	}
	defer tx.Rollback()

	var id int64
	var loginType string
	err = tx.QueryRow(`SELECT id, login_type FROM accounts WHERE owner_id = ? AND student_id = ?`, ownerID, norm.StudentID).
		Scan(&id, &loginType)
	switch {
	case err == nil:
		if normalizeLoginType(LoginType(loginType)) != LoginWechat {
			return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在密码登录账号", ErrConflict, norm.StudentID)
		}
		if _, err := tx.Exec(
			`UPDATE accounts SET name = ?, campus = ?, cookies_enc = ?, cookies_expires_at = ?, updated_at = ?
			 WHERE id = ? AND owner_id = ?`,
			norm.Name, norm.Campus, enc, formatTime(norm.ExpiresAt), formatTime(now), id, ownerID,
		); err != nil {
			return AccountView{}, fmt.Errorf("更新扫码账号: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		res, err := tx.Exec(
			`INSERT INTO accounts (owner_id, student_id, name, login_type, cookies_enc, cookies_expires_at, campus, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ownerID, norm.StudentID, norm.Name, string(LoginWechat), enc, formatTime(norm.ExpiresAt),
			norm.Campus, formatTime(now), formatTime(now),
		)
		if err != nil {
			if isUniqueViolation(err) {
				return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在", ErrConflict, norm.StudentID)
			}
			return AccountView{}, fmt.Errorf("保存扫码账号: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return AccountView{}, fmt.Errorf("读取账号 ID: %w", err)
		}
	default:
		return AccountView{}, fmt.Errorf("查询扫码账号: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AccountView{}, fmt.Errorf("保存扫码账号: %w", err)
	}
	return s.accountView(int64(ownerID), id)
}

func (s *Store) UpdateWechatSession(ownerID uint64, id int64, name, campus, cookies string, expires time.Time) (AccountView, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AccountView{}, fmt.Errorf("更新扫码会话: %w", err)
	}
	defer tx.Rollback()

	cur, err := s.accountTx(tx, int64(ownerID), id)
	if err != nil {
		return AccountView{}, err
	}
	if normalizeLoginType(cur.LoginType) != LoginWechat {
		return AccountView{}, invalid("该账号不是扫码登录账号")
	}
	norm, err := normalizeWechatInput(WechatAccountInput{
		StudentID: cur.StudentID,
		Name:      name,
		Campus:    campus,
		Cookies:   cookies,
		ExpiresAt: expires,
	})
	if err != nil {
		return AccountView{}, err
	}
	enc, err := s.seal(norm.Cookies)
	if err != nil {
		return AccountView{}, fmt.Errorf("加密扫码会话: %w", err)
	}
	if _, err := tx.Exec(
		`UPDATE accounts SET name = ?, campus = ?, cookies_enc = ?, cookies_expires_at = ?, updated_at = ?
		 WHERE id = ? AND owner_id = ?`,
		norm.Name, norm.Campus, enc, formatTime(norm.ExpiresAt), formatTime(time.Now().UTC()), id, ownerID,
	); err != nil {
		return AccountView{}, fmt.Errorf("更新扫码会话: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AccountView{}, fmt.Errorf("更新扫码会话: %w", err)
	}
	return s.accountView(int64(ownerID), id)
}

func (s *Store) UpdateAccount(ownerID uint64, id int64, in AccountInput) (AccountView, error) {
	norm, err := normalizeInput(in, false)
	if err != nil {
		return AccountView{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AccountView{}, fmt.Errorf("更新账号: %w", err)
	}
	defer tx.Rollback()

	cur, err := s.accountTx(tx, int64(ownerID), id)
	if err != nil {
		return AccountView{}, err
	}
	now := time.Now().UTC()
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
		if _, err := tx.Exec(
			`UPDATE accounts SET name = ?, campus = ?, updated_at = ? WHERE id = ? AND owner_id = ?`,
			norm.Name, norm.Campus, formatTime(now), id, ownerID,
		); err != nil {
			return AccountView{}, fmt.Errorf("更新账号: %w", err)
		}
	} else {
		if norm.LoginType != LoginPassword {
			return AccountView{}, invalid("不能修改密码账号的登录方式")
		}
		var dup int
		if err := tx.QueryRow(
			`SELECT COUNT(*) FROM accounts WHERE owner_id = ? AND student_id = ? AND id <> ?`,
			ownerID, norm.StudentID, id,
		).Scan(&dup); err != nil {
			return AccountView{}, fmt.Errorf("检查学号: %w", err)
		}
		if dup > 0 {
			return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在", ErrConflict, norm.StudentID)
		}

		studentChanged := cur.StudentID != norm.StudentID
		sets := `student_id = ?, name = ?, campus = ?, updated_at = ?`
		args := []any{norm.StudentID, norm.Name, norm.Campus, formatTime(now)}
		if norm.Password != "" {
			enc, err := s.seal(norm.Password)
			if err != nil {
				return AccountView{}, fmt.Errorf("加密密码: %w", err)
			}
			sets += `, password_enc = ?, cookies_enc = '', cookies_expires_at = NULL`
			args = append(args, enc)
		}
		if studentChanged {
			sets += `, cookies_enc = '', cookies_expires_at = NULL, last_checkin_at = NULL, last_status = ''`
		}
		args = append(args, id, ownerID)
		if _, err := tx.Exec(`UPDATE accounts SET `+sets+` WHERE id = ? AND owner_id = ?`, args...); err != nil {
			if isUniqueViolation(err) {
				return AccountView{}, fmt.Errorf("%w: 学号 %s 已存在", ErrConflict, norm.StudentID)
			}
			return AccountView{}, fmt.Errorf("更新账号: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return AccountView{}, fmt.Errorf("更新账号: %w", err)
	}
	return s.accountView(int64(ownerID), id)
}

func (s *Store) DeleteAccount(ownerID uint64, id int64) error {
	res, err := s.db.Exec(`DELETE FROM accounts WHERE id = ? AND owner_id = ?`, id, ownerID)
	if err != nil {
		return fmt.Errorf("删除账号: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("删除账号: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	return nil
}

func (s *Store) Account(ownerID uint64, id int64) (Account, bool) {
	row := s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ? AND owner_id = ?`, id, ownerID)
	acc, err := scanAccount(row)
	if err != nil {
		return Account{}, false
	}
	return acc, true
}

func (s *Store) accounts(ownerID uint64) ([]Account, error) {
	rows, err := s.db.Query(`SELECT `+accountColumns+` FROM accounts WHERE owner_id = ? ORDER BY id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("读取账号: %w", err)
	}
	defer rows.Close()
	out := make([]Account, 0, 8)
	for rows.Next() {
		acc, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("读取账号: %w", err)
		}
		out = append(out, acc)
	}
	return out, rows.Err()
}

func (s *Store) Password(acc Account) (string, error) {
	return s.open(acc.PasswordEnc)
}

func (s *Store) RememberCookies(acc Account) (string, error) {
	return s.open(acc.RememberCookiesEnc)
}

func (s *Store) SaveRememberCookies(ownerID uint64, id int64, cookies string, expires time.Time) error {
	if cookies == "" {
		return nil
	}
	enc, err := s.seal(cookies)
	if err != nil {
		return fmt.Errorf("加密登录会话: %w", err)
	}
	res, err := s.db.Exec(
		`UPDATE accounts SET cookies_enc = ?, cookies_expires_at = ?, updated_at = ?
		 WHERE id = ? AND owner_id = ?`,
		enc, formatTime(expires), formatTime(time.Now().UTC()), id, ownerID,
	)
	if err != nil {
		return fmt.Errorf("保存登录会话: %w", err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	return nil
}

func (s *Store) UpdateCheckinStatus(ownerID uint64, id int64, status string, at time.Time) error {
	res, err := s.db.Exec(
		`UPDATE accounts SET last_checkin_at = ?, last_status = ?, updated_at = ?
		 WHERE id = ? AND owner_id = ?`,
		formatTime(at), truncate(status, 500), formatTime(at), id, ownerID,
	)
	if err != nil {
		return fmt.Errorf("更新打卡状态: %w", err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("%w: 账号 %d", ErrNotFound, id)
	}
	return nil
}
