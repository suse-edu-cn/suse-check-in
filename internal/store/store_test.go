package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func input(studentID, password string) AccountInput {
	return AccountInput{
		StudentID: studentID,
		Password:  password,
		Name:      "测试账号",
		Campus:    "宜宾",
	}
}

func newStore(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestStoreEncryptsAndPersistsAccount(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	view, err := st.AddAccount(1, input("20240001", "secret-pass"))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if view.ID == 0 || !view.HasPassword || view.HasRememberMe {
		t.Fatalf("unexpected view: %+v", view)
	}

	for _, name := range []string{dbFileName, dbFileName + "-wal"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && bytes.Contains(raw, []byte("secret-pass")) {
			t.Fatalf("%s 不应包含明文密码", name)
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, keyFileName))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("key 权限 = %o, want 600", got)
		}
	}

	acc, ok := st.Account(1, view.ID)
	if !ok {
		t.Fatal("account not found")
	}
	password, err := st.Password(acc)
	if err != nil || password != "secret-pass" {
		t.Fatalf("Password = %q, %v", password, err)
	}

	reopened := newStore(t, dir)
	acc, ok = reopened.Account(1, view.ID)
	if !ok {
		t.Fatal("account missing after reopen")
	}
	password, err = reopened.Password(acc)
	if err != nil || password != "secret-pass" {
		t.Fatalf("password after reopen = %q, %v", password, err)
	}

	if _, err := st.AddAccount(1, input("20240001", "other")); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error = %v, want ErrConflict", err)
	}
	// 同一个学号可以被另一个 OAA 用户各自添加。
	if _, err := st.AddAccount(2, input("20240001", "other")); err != nil {
		t.Fatalf("same student for another owner: %v", err)
	}
}

func TestStoreUpdateKeepsPasswordWhenBlank(t *testing.T) {
	st := newStore(t, t.TempDir())
	view, err := st.AddAccount(1, input("20240002", "first-pass"))
	if err != nil {
		t.Fatal(err)
	}

	upd := input("20240002", "")
	upd.Name = "新名字"
	updated, err := st.UpdateAccount(1, view.ID, upd)
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Name != "新名字" {
		t.Fatalf("name = %q", updated.Name)
	}
	acc, _ := st.Account(1, view.ID)
	if got, _ := st.Password(acc); got != "first-pass" {
		t.Fatalf("password = %q, want first-pass", got)
	}

	upd.Password = "second-pass"
	if _, err := st.UpdateAccount(1, view.ID, upd); err != nil {
		t.Fatal(err)
	}
	acc, _ = st.Account(1, view.ID)
	if got, _ := st.Password(acc); got != "second-pass" {
		t.Fatalf("password = %q, want second-pass", got)
	}

	if _, err := st.AddAccount(1, input("20240003", "x")); err != nil {
		t.Fatal(err)
	}
	upd.StudentID = "20240003"
	if _, err := st.UpdateAccount(1, view.ID, upd); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestStoreRememberCookiesAndStatus(t *testing.T) {
	st := newStore(t, t.TempDir())
	view, err := st.AddAccount(1, input("20240004", "pass"))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(24 * time.Hour).UTC()
	if err := st.SaveRememberCookies(1, view.ID, "CASTGC=abc; JSESSIONID=def", expires); err != nil {
		t.Fatal(err)
	}
	acc, _ := st.Account(1, view.ID)
	cookies, err := st.RememberCookies(acc)
	if err != nil || cookies != "CASTGC=abc; JSESSIONID=def" {
		t.Fatalf("cookies = %q, %v", cookies, err)
	}
	if err := st.UpdateCheckinStatus(1, view.ID, "成功：签到成功", time.Now()); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot(1, 10)
	if len(snap.Accounts) != 1 {
		t.Fatalf("accounts = %d", len(snap.Accounts))
	}
	if !snap.Accounts[0].HasRememberMe || !snap.Accounts[0].RememberValid {
		t.Fatalf("remember state = %+v", snap.Accounts[0])
	}
	if snap.Accounts[0].LastStatus != "成功：签到成功" || snap.Accounts[0].LastCheckinAt == nil {
		t.Fatalf("last status = %+v", snap.Accounts[0])
	}
	raw, _ := os.ReadFile(filepath.Join(st.Dir(), dbFileName))
	if bytes.Contains(raw, []byte("CASTGC=abc")) {
		t.Fatal("remember cookie 不应明文落盘")
	}
}

func TestScheduleValidationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	a1, _ := st.AddAccount(1, input("20240010", "a"))
	a2, _ := st.AddAccount(1, input("20240011", "b"))

	saved, err := st.SaveSchedule(1, Schedule{
		Enabled:              true,
		TimeOfDay:            "07:30",
		AccountIDs:           []int64{a1.ID, a2.ID, a1.ID},
		MaxRetries:           2,
		RetryIntervalMinutes: 3,
	})
	if err != nil {
		t.Fatalf("SaveSchedule: %v", err)
	}
	if saved.TimeOfDay != "07:30:00" || len(saved.AccountIDs) != 2 {
		t.Fatalf("saved = %+v", saved)
	}

	if _, err := st.SaveSchedule(1, Schedule{TimeOfDay: "25:00", MaxRetries: 1, RetryIntervalMinutes: 1}); err == nil {
		t.Fatal("invalid time accepted")
	}
	if _, err := st.SaveSchedule(1, Schedule{TimeOfDay: "07:00", MaxRetries: 11, RetryIntervalMinutes: 1}); err == nil {
		t.Fatal("invalid retries accepted")
	}
	if _, err := st.SaveSchedule(1, Schedule{TimeOfDay: "07:00", MaxRetries: 1, RetryIntervalMinutes: 0}); err == nil {
		t.Fatal("invalid interval accepted")
	}
	if _, err := st.SaveSchedule(1, Schedule{TimeOfDay: "07:00", AccountIDs: []int64{999}, MaxRetries: 1, RetryIntervalMinutes: 1}); err == nil {
		t.Fatal("unknown account accepted")
	}

	at := time.Now().UTC()
	if err := st.MarkScheduledRun(1, "2026-10-26", at, "成功 2，失败 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSchedule(1, Schedule{Enabled: true, TimeOfDay: "08:00", AccountIDs: []int64{a1.ID}, MaxRetries: 1, RetryIntervalMinutes: 2}); err != nil {
		t.Fatal(err)
	}
	reopened := newStore(t, dir)
	sch := reopened.Schedule(1)
	if sch.LastRunDate != "2026-10-26" || sch.LastResult != "成功 2，失败 0" || sch.LastRunAt == nil {
		t.Fatalf("last run not preserved: %+v", sch)
	}
	if sch.TimeOfDay != "08:00:00" || len(sch.AccountIDs) != 1 || sch.AccountIDs[0] != a1.ID {
		t.Fatalf("schedule = %+v", sch)
	}
	// 第二个用户看到的是自己的默认配置，不受影响。
	other := reopened.Schedule(2)
	if other.Enabled || len(other.AccountIDs) != 0 || other.TimeOfDay != "07:00:00" {
		t.Fatalf("other owner schedule = %+v", other)
	}
}

func TestWechatAccountSaveAndUpdate(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	expires := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	view, err := st.AddWechatAccount(1, WechatAccountInput{
		StudentID: "20240030",
		Name:      "扫码用户",
		Campus:    "宜宾",
		Cookies:   "_sop_session_=abc; SESSION=def",
		ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("AddWechatAccount: %v", err)
	}
	if view.LoginType != LoginWechat || view.HasPassword || !view.HasRememberMe || !view.RememberValid {
		t.Fatalf("view = %+v", view)
	}
	acc, ok := st.Account(1, view.ID)
	if !ok || acc.LoginType != LoginWechat {
		t.Fatalf("account = %+v", acc)
	}
	cookies, err := st.RememberCookies(acc)
	if err != nil || cookies != "_sop_session_=abc; SESSION=def" {
		t.Fatalf("cookies = %q, %v", cookies, err)
	}

	updated, err := st.UpdateAccount(1, view.ID, AccountInput{
		StudentID: "20240030",
		Name:      "新名字",
		Campus:    "汇东",
		LoginType: LoginWechat,
	})
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Name != "新名字" || updated.Campus != "汇东" {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := st.UpdateAccount(1, view.ID, AccountInput{
		StudentID: "20240030",
		Password:  "not-allowed",
		LoginType: LoginWechat,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("password on wechat account error = %v", err)
	}
	if _, err := st.UpdateAccount(1, view.ID, AccountInput{
		StudentID: "20240030",
		LoginType: LoginPassword,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("login type change error = %v", err)
	}

	newExpires := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	if _, err := st.UpdateWechatSession(1, view.ID, "扫码用户", "宜宾", "_sop_session_=new; SESSION=new", newExpires); err != nil {
		t.Fatalf("UpdateWechatSession: %v", err)
	}
	acc, _ = st.Account(1, view.ID)
	cookies, err = st.RememberCookies(acc)
	if err != nil || cookies != "_sop_session_=new; SESSION=new" {
		t.Fatalf("updated cookies = %q, %v", cookies, err)
	}

	if _, err := st.AddAccount(1, input("20240031", "pass")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddWechatAccount(1, WechatAccountInput{
		StudentID: "20240031",
		Cookies:   "_sop_session_=x",
		ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("password conflict error = %v", err)
	}

	reopened := newStore(t, dir)
	acc, ok = reopened.Account(1, view.ID)
	if !ok || acc.LoginType != LoginWechat {
		t.Fatalf("reopened account = %+v", acc)
	}
	cookies, err = reopened.RememberCookies(acc)
	if err != nil || cookies != "_sop_session_=new; SESSION=new" {
		t.Fatalf("reopened cookies = %q, %v", cookies, err)
	}
}

func TestScheduleNotificationSettings(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	saved, err := st.SaveSchedule(1, Schedule{
		TimeOfDay:            "07:00",
		MaxRetries:           1,
		RetryIntervalMinutes: 1,
		NotifyEnabled:        true,
		NotifyEmail:          " notify@example.com ",
	})
	if err != nil {
		t.Fatalf("SaveSchedule: %v", err)
	}
	if !saved.NotifyEnabled || saved.NotifyEmail != "notify@example.com" {
		t.Fatalf("saved = %+v", saved)
	}

	if _, err := st.SaveSchedule(1, Schedule{
		TimeOfDay: "07:00", MaxRetries: 1, RetryIntervalMinutes: 1, NotifyEnabled: true,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing email error = %v", err)
	}
	if _, err := st.SaveSchedule(1, Schedule{
		TimeOfDay: "07:00", MaxRetries: 1, RetryIntervalMinutes: 1, NotifyEnabled: true, NotifyEmail: "bad",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid email error = %v", err)
	}

	reopened := newStore(t, dir)
	sch := reopened.Schedule(1)
	if !sch.NotifyEnabled || sch.NotifyEmail != "notify@example.com" {
		t.Fatalf("reopened notification = %+v", sch)
	}
}

func TestDeleteRemovesFromScheduleAndRunsOrdering(t *testing.T) {
	st := newStore(t, t.TempDir())
	a1, _ := st.AddAccount(1, input("20240020", "a"))
	a2, _ := st.AddAccount(1, input("20240021", "b"))
	if _, err := st.SaveSchedule(1, Schedule{TimeOfDay: "07:00", AccountIDs: []int64{a1.ID, a2.ID}, MaxRetries: 1, RetryIntervalMinutes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAccount(1, a1.ID); err != nil {
		t.Fatal(err)
	}
	sch := st.Schedule(1)
	if len(sch.AccountIDs) != 1 || sch.AccountIDs[0] != a2.ID {
		t.Fatalf("schedule after delete = %+v", sch)
	}
	if _, ok := st.Account(1, a1.ID); ok {
		t.Fatal("deleted account still exists")
	}

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		run, err := st.AppendRun(1, Run{
			Trigger:    "manual",
			StartedAt:  now.Add(time.Duration(i) * time.Minute),
			FinishedAt: now.Add(time.Duration(i)*time.Minute + time.Second),
			Summary:    "ok",
			Results:    []AccountResult{{AccountID: a2.ID, Status: "success"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if run.ID != int64(i+1) {
			t.Fatalf("run id = %d", run.ID)
		}
	}
	snap := st.Snapshot(1, 2)
	if len(snap.Runs) != 2 || snap.Runs[0].ID != 3 || snap.Runs[1].ID != 2 {
		t.Fatalf("runs order = %+v", snap.Runs)
	}
	if len(snap.Runs[0].Results) != 1 || snap.Runs[0].Results[0].Status != "success" {
		t.Fatalf("run results = %+v", snap.Runs[0].Results)
	}
}

func TestOwnerIsolation(t *testing.T) {
	st := newStore(t, t.TempDir())
	mine, err := st.AddAccount(1, input("20240040", "mine"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.AddAccount(2, input("20240040", "other"))
	if err != nil {
		t.Fatalf("same student id for second owner: %v", err)
	}
	if mine.ID == other.ID {
		t.Fatal("accounts must be distinct rows")
	}

	if _, ok := st.Account(2, mine.ID); ok {
		t.Fatal("owner 2 can read owner 1's account")
	}
	if _, err := st.UpdateAccount(2, mine.ID, input("20240040", "hijack")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update cross-owner error = %v", err)
	}
	if err := st.DeleteAccount(2, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete cross-owner error = %v", err)
	}
	if err := st.SaveRememberCookies(2, mine.ID, "CASTGC=x", time.Now().Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cookies cross-owner error = %v", err)
	}
	if err := st.UpdateCheckinStatus(2, mine.ID, "x", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status cross-owner error = %v", err)
	}
	if _, err := st.SaveSchedule(2, Schedule{TimeOfDay: "07:00", AccountIDs: []int64{mine.ID}, MaxRetries: 1, RetryIntervalMinutes: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-owner schedule error = %v", err)
	}

	if _, err := st.SaveSchedule(1, Schedule{Enabled: true, TimeOfDay: "07:00", AccountIDs: []int64{mine.ID}, MaxRetries: 1, RetryIntervalMinutes: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendRun(1, Run{Trigger: "manual", StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Summary: "mine"}); err != nil {
		t.Fatal(err)
	}

	snap1 := st.Snapshot(1, 10)
	snap2 := st.Snapshot(2, 10)
	if len(snap1.Accounts) != 1 || len(snap1.Runs) != 1 {
		t.Fatalf("owner 1 snapshot = %d accounts, %d runs", len(snap1.Accounts), len(snap1.Runs))
	}
	if len(snap2.Accounts) != 1 || len(snap2.Runs) != 0 {
		t.Fatalf("owner 2 snapshot = %d accounts, %d runs", len(snap2.Accounts), len(snap2.Runs))
	}
	if snap1.Accounts[0].Scheduled != true || snap2.Accounts[0].Scheduled != false {
		t.Fatalf("scheduled flags = %v / %v", snap1.Accounts[0].Scheduled, snap2.Accounts[0].Scheduled)
	}

	plans, err := st.ScheduledPlans()
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].OwnerID != 1 || len(plans[0].AccountIDs) != 1 {
		t.Fatalf("plans = %+v", plans)
	}
}

func TestLegacyClaim(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	_ = st.Close()

	legacy := legacyData{
		Version: 1,
		Accounts: []legacyAccount{
			{ID: 1, StudentID: "24100001", Name: "旧账号", LoginType: LoginPassword, PasswordEnc: "ZW5j", Campus: "宜宾", LastStatus: "成功：签到成功"},
			{ID: 2, StudentID: "24100002", Name: "旧扫码", LoginType: LoginWechat, RememberCookiesEnc: "ZW5j", Campus: "李白河"},
		},
		Schedule: legacySchedule{
			Enabled: true, TimeOfDay: "19:01:00", AccountIDs: []int64{1},
			MaxRetries: 3, RetryIntervalMinutes: 5, LastRunDate: "2026-10-26", LastResult: "成功 1",
		},
		Runs: []legacyRun{{
			ID: 1, Trigger: "scheduled",
			StartedAt:  time.Date(2026, 10, 26, 19, 1, 0, 0, time.UTC),
			FinishedAt: time.Date(2026, 10, 26, 19, 1, 30, 0, time.UTC),
			Summary:    "成功 1，失败 0，共 1 个账号",
			Results:    []AccountResult{{AccountID: 1, StudentID: "24100001", Status: "success"}},
		}},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, legacyFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	st2 := newStore(t, dir)
	if !st2.LegacyPending() {
		t.Fatal("legacy data not detected")
	}
	imported, err := st2.ClaimLegacy(42)
	if err != nil || !imported {
		t.Fatalf("ClaimLegacy = %v, %v", imported, err)
	}
	snap := st2.Snapshot(42, 10)
	if len(snap.Accounts) != 2 || len(snap.Runs) != 1 {
		t.Fatalf("snapshot = %d accounts, %d runs", len(snap.Accounts), len(snap.Runs))
	}
	if snap.Schedule.TimeOfDay != "19:01:00" || !snap.Schedule.Enabled || len(snap.Schedule.AccountIDs) != 1 {
		t.Fatalf("schedule = %+v", snap.Schedule)
	}
	if snap.Schedule.LastRunDate != "2026-10-26" {
		t.Fatalf("last run date = %q", snap.Schedule.LastRunDate)
	}
	if len(snap.Runs[0].Results) != 1 || snap.Runs[0].Results[0].StudentID != "24100001" {
		t.Fatalf("run results = %+v", snap.Runs[0].Results)
	}
	if again, err := st2.ClaimLegacy(42); err != nil || again {
		t.Fatalf("second claim = %v, %v", again, err)
	}
	if got := st2.Snapshot(7, 10); len(got.Accounts) != 0 {
		t.Fatalf("other owner got %d accounts", len(got.Accounts))
	}
	_ = st2.Close()

	st3 := newStore(t, dir)
	if st3.LegacyPending() {
		t.Fatal("legacy should be marked claimed after restart")
	}
	if got := st3.Snapshot(42, 10); len(got.Accounts) != 2 {
		t.Fatalf("migrated data lost after restart: %d", len(got.Accounts))
	}
}

func TestLegacySkipsOwnerWithExistingData(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	if _, err := st.AddAccount(42, input("24100009", "pass")); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if err := os.WriteFile(filepath.Join(dir, legacyFileName), []byte(`{"accounts":[{"id":1,"studentId":"24100001","name":"旧","campus":"宜宾"}],"runs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st2 := newStore(t, dir)
	imported, err := st2.ClaimLegacy(42)
	if err != nil || imported {
		t.Fatalf("claim with existing data = %v, %v", imported, err)
	}
	if got := st2.Snapshot(42, 10); len(got.Accounts) != 1 {
		t.Fatalf("accounts = %d, want only the existing one", len(got.Accounts))
	}
}

func TestConcurrentAdds(t *testing.T) {
	st := newStore(t, t.TempDir())
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.AddAccount(1, input("2024"+strings.Repeat("0", 3)+string(rune('A'+i)), "pass"))
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent add: %v", err)
	}
	snap := st.Snapshot(1, 0)
	if len(snap.Accounts) != n {
		t.Fatalf("accounts = %d, want %d", len(snap.Accounts), n)
	}
	seen := map[int64]bool{}
	for _, a := range snap.Accounts {
		if seen[a.ID] {
			t.Fatalf("duplicate id %d", a.ID)
		}
		seen[a.ID] = true
	}
}

func TestCampusValidation(t *testing.T) {
	st := newStore(t, t.TempDir())
	emptyCampus := input("20240030", "pass")
	emptyCampus.Campus = ""
	view, err := st.AddAccount(1, emptyCampus)
	if err != nil {
		t.Fatalf("empty campus: %v", err)
	}
	if view.Campus != "宜宾" {
		t.Fatalf("default campus = %q", view.Campus)
	}

	badCampus := input("20240031", "pass")
	badCampus.Campus = "北京"
	if _, err := st.AddAccount(1, badCampus); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad campus error = %v", err)
	}

	update := input("20240030", "")
	update.Campus = "不存在"
	if _, err := st.UpdateAccount(1, view.ID, update); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad update campus error = %v", err)
	}
}

func TestNewRejectsShortKeyAndToleratesCorruptLegacy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keyFileName), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil {
		t.Fatal("short key accepted")
	}

	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, legacyFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newStore(t, dir2)
	if st.LoadLegacyError() == nil {
		t.Fatal("corrupt legacy should report an error")
	}
	if st.LegacyPending() {
		t.Fatal("corrupt legacy should not be pending")
	}
}
