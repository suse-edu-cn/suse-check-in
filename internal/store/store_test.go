package store

import (
	"bytes"
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

func TestStoreEncryptsAndPersistsAccount(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	view, err := st.AddAccount(input("20240001", "secret-pass"))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if view.ID == 0 || !view.HasPassword || view.HasRememberMe {
		t.Fatalf("unexpected view: %+v", view)
	}

	stateRaw, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stateRaw, []byte("secret-pass")) {
		t.Fatal("state.json 不应包含明文密码")
	}
	if !bytes.Contains(stateRaw, []byte(`"passwordEnc"`)) {
		t.Fatal("state.json 应包含加密后的密码字段")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{stateFileName, keyFileName} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("%s 权限 = %o, want 600", name, got)
			}
		}
	}

	acc, ok := st.Account(view.ID)
	if !ok {
		t.Fatal("account not found")
	}
	password, err := st.Password(acc)
	if err != nil || password != "secret-pass" {
		t.Fatalf("Password = %q, %v", password, err)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	acc, ok = reopened.Account(view.ID)
	if !ok {
		t.Fatal("account missing after reopen")
	}
	password, err = reopened.Password(acc)
	if err != nil || password != "secret-pass" {
		t.Fatalf("password after reopen = %q, %v", password, err)
	}

	if _, err := st.AddAccount(input("20240001", "other")); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error = %v, want ErrConflict", err)
	}
}

func TestStoreUpdateKeepsPasswordWhenBlank(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	view, err := st.AddAccount(input("20240002", "first-pass"))
	if err != nil {
		t.Fatal(err)
	}

	upd := input("20240002", "")
	upd.Name = "新名字"
	updated, err := st.UpdateAccount(view.ID, upd)
	if err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	if updated.Name != "新名字" {
		t.Fatalf("name = %q", updated.Name)
	}
	acc, _ := st.Account(view.ID)
	if got, _ := st.Password(acc); got != "first-pass" {
		t.Fatalf("password = %q, want first-pass", got)
	}

	upd.Password = "second-pass"
	if _, err := st.UpdateAccount(view.ID, upd); err != nil {
		t.Fatal(err)
	}
	acc, _ = st.Account(view.ID)
	if got, _ := st.Password(acc); got != "second-pass" {
		t.Fatalf("password = %q, want second-pass", got)
	}

	other, err := st.AddAccount(input("20240003", "x"))
	if err != nil {
		t.Fatal(err)
	}
	upd.StudentID = "20240003"
	if _, err := st.UpdateAccount(view.ID, upd); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	_ = other
}

func TestStoreRememberCookiesAndStatus(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	view, err := st.AddAccount(input("20240004", "pass"))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(24 * time.Hour).UTC()
	if err := st.SaveRememberCookies(view.ID, "CASTGC=abc; JSESSIONID=def", expires); err != nil {
		t.Fatal(err)
	}
	acc, _ := st.Account(view.ID)
	cookies, err := st.RememberCookies(acc)
	if err != nil || cookies != "CASTGC=abc; JSESSIONID=def" {
		t.Fatalf("cookies = %q, %v", cookies, err)
	}
	if err := st.UpdateCheckinStatus(view.ID, "成功：签到成功", time.Now()); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot(10)
	if len(snap.Accounts) != 1 {
		t.Fatalf("accounts = %d", len(snap.Accounts))
	}
	if !snap.Accounts[0].HasRememberMe || !snap.Accounts[0].RememberValid {
		t.Fatalf("remember state = %+v", snap.Accounts[0])
	}
	if snap.Accounts[0].LastStatus != "成功：签到成功" || snap.Accounts[0].LastCheckinAt == nil {
		t.Fatalf("last status = %+v", snap.Accounts[0])
	}
	raw, _ := os.ReadFile(filepath.Join(filepath.Dir(st.path), stateFileName))
	if bytes.Contains(raw, []byte("CASTGC=abc")) {
		t.Fatal("remember cookie 不应明文落盘")
	}
}

func TestScheduleValidationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := st.AddAccount(input("20240010", "a"))
	a2, _ := st.AddAccount(input("20240011", "b"))

	saved, err := st.SaveSchedule(Schedule{
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

	if _, err := st.SaveSchedule(Schedule{TimeOfDay: "25:00", MaxRetries: 1, RetryIntervalMinutes: 1}); err == nil {
		t.Fatal("invalid time accepted")
	}
	if _, err := st.SaveSchedule(Schedule{TimeOfDay: "07:00", MaxRetries: 11, RetryIntervalMinutes: 1}); err == nil {
		t.Fatal("invalid retries accepted")
	}
	if _, err := st.SaveSchedule(Schedule{TimeOfDay: "07:00", MaxRetries: 1, RetryIntervalMinutes: 0}); err == nil {
		t.Fatal("invalid interval accepted")
	}
	if _, err := st.SaveSchedule(Schedule{TimeOfDay: "07:00", AccountIDs: []int64{999}, MaxRetries: 1, RetryIntervalMinutes: 1}); err == nil {
		t.Fatal("unknown account accepted")
	}

	at := time.Now().UTC()
	if err := st.MarkScheduledRun("2026-10-26", at, "成功 2，失败 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSchedule(Schedule{Enabled: true, TimeOfDay: "08:00", AccountIDs: []int64{a1.ID}, MaxRetries: 1, RetryIntervalMinutes: 2}); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sch := reopened.Schedule()
	if sch.LastRunDate != "2026-10-26" || sch.LastResult != "成功 2，失败 0" || sch.LastRunAt == nil {
		t.Fatalf("last run not preserved: %+v", sch)
	}
	if sch.TimeOfDay != "08:00:00" || len(sch.AccountIDs) != 1 || sch.AccountIDs[0] != a1.ID {
		t.Fatalf("schedule = %+v", sch)
	}
}

func TestWechatAccountSaveAndUpdate(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	view, err := st.AddWechatAccount(WechatAccountInput{
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
	acc, ok := st.Account(view.ID)
	if !ok || acc.LoginType != LoginWechat {
		t.Fatalf("account = %+v", acc)
	}
	cookies, err := st.RememberCookies(acc)
	if err != nil || cookies != "_sop_session_=abc; SESSION=def" {
		t.Fatalf("cookies = %q, %v", cookies, err)
	}

	updated, err := st.UpdateAccount(view.ID, AccountInput{
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
	if _, err := st.UpdateAccount(view.ID, AccountInput{
		StudentID: "20240030",
		Password:  "not-allowed",
		LoginType: LoginWechat,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("password on wechat account error = %v", err)
	}
	if _, err := st.UpdateAccount(view.ID, AccountInput{
		StudentID: "20240030",
		LoginType: LoginPassword,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("login type change error = %v", err)
	}

	newExpires := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	if _, err := st.UpdateWechatSession(view.ID, "扫码用户", "宜宾", "_sop_session_=new; SESSION=new", newExpires); err != nil {
		t.Fatalf("UpdateWechatSession: %v", err)
	}
	acc, _ = st.Account(view.ID)
	cookies, err = st.RememberCookies(acc)
	if err != nil || cookies != "_sop_session_=new; SESSION=new" {
		t.Fatalf("updated cookies = %q, %v", cookies, err)
	}

	if _, err := st.AddAccount(input("20240031", "pass")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddWechatAccount(WechatAccountInput{
		StudentID: "20240031",
		Cookies:   "_sop_session_=x",
		ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("password conflict error = %v", err)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc, ok = reopened.Account(view.ID)
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
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.SaveSchedule(Schedule{
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

	if _, err := st.SaveSchedule(Schedule{
		TimeOfDay: "07:00", MaxRetries: 1, RetryIntervalMinutes: 1, NotifyEnabled: true,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing email error = %v", err)
	}
	if _, err := st.SaveSchedule(Schedule{
		TimeOfDay: "07:00", MaxRetries: 1, RetryIntervalMinutes: 1, NotifyEnabled: true, NotifyEmail: "bad",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid email error = %v", err)
	}

	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	sch := reopened.Schedule()
	if !sch.NotifyEnabled || sch.NotifyEmail != "notify@example.com" {
		t.Fatalf("reopened notification = %+v", sch)
	}
}

func TestDeleteRemovesFromScheduleAndRunsOrdering(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := st.AddAccount(input("20240020", "a"))
	a2, _ := st.AddAccount(input("20240021", "b"))
	if _, err := st.SaveSchedule(Schedule{TimeOfDay: "07:00", AccountIDs: []int64{a1.ID, a2.ID}, MaxRetries: 1, RetryIntervalMinutes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAccount(a1.ID); err != nil {
		t.Fatal(err)
	}
	sch := st.Schedule()
	if len(sch.AccountIDs) != 1 || sch.AccountIDs[0] != a2.ID {
		t.Fatalf("schedule after delete = %+v", sch)
	}
	if _, ok := st.Account(a1.ID); ok {
		t.Fatal("deleted account still exists")
	}

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		run, err := st.AppendRun(Run{
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
	snap := st.Snapshot(2)
	if len(snap.Runs) != 2 || snap.Runs[0].ID != 3 || snap.Runs[1].ID != 2 {
		t.Fatalf("runs order = %+v", snap.Runs)
	}
}

func TestConcurrentAdds(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.AddAccount(input("2024"+strings.Repeat("0", 3)+string(rune('A'+i)), "pass"))
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
	snap := st.Snapshot(0)
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
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	emptyCampus := input("20240030", "pass")
	emptyCampus.Campus = ""
	view, err := st.AddAccount(emptyCampus)
	if err != nil {
		t.Fatalf("empty campus: %v", err)
	}
	if view.Campus != "宜宾" {
		t.Fatalf("default campus = %q", view.Campus)
	}

	badCampus := input("20240031", "pass")
	badCampus.Campus = "北京"
	if _, err := st.AddAccount(badCampus); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad campus error = %v", err)
	}

	update := input("20240030", "")
	update.Campus = "不存在"
	if _, err := st.UpdateAccount(view.ID, update); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad update campus error = %v", err)
	}
}

func TestNewRejectsInvalidStateAndKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); err == nil {
		t.Fatal("corrupt state accepted")
	}

	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, keyFileName), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir2); err == nil {
		t.Fatal("short key accepted")
	}
}
