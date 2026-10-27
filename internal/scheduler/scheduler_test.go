package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"suse-checkin/internal/store"
)

type runCall struct {
	ownerID uint64
	ids     []int64
}

type fakeRunner struct {
	calls   chan runCall
	result  store.Run
	running bool
}

func (f *fakeRunner) RunAccounts(_ context.Context, ownerID uint64, accountIDs []int64, _ string) (store.Run, error) {
	select {
	case f.calls <- runCall{ownerID: ownerID, ids: append([]int64(nil), accountIDs...)}:
	default:
	}
	if f.result.Results != nil {
		return f.result, nil
	}
	return store.Run{Summary: "ok", StartedAt: time.Now(), FinishedAt: time.Now()}, nil
}

func (f *fakeRunner) IsRunning() bool { return f.running }

type notifyCall struct {
	to         string
	run        store.Run
	maxRetries int
}

type fakeNotifier struct {
	calls chan notifyCall
}

func (f *fakeNotifier) NotifyFailure(_ context.Context, to string, run store.Run, maxRetries int) error {
	f.calls <- notifyCall{to: to, run: run, maxRetries: maxRetries}
	return nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func addAccount(t *testing.T, st *store.Store, ownerID uint64, studentID string) store.AccountView {
	t.Helper()
	acc, err := st.AddAccount(ownerID, store.AccountInput{
		StudentID: studentID,
		Password:  "pass",
		Campus:    "宜宾",
	})
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	return acc
}

func TestParseTimeOfDay(t *testing.T) {
	for _, tc := range []struct {
		in      string
		h, m, s int
	}{
		{"07:00", 7, 0, 0},
		{"07:00:30", 7, 0, 30},
		{"23:59:59", 23, 59, 59},
	} {
		h, m, s, err := parseTimeOfDay(tc.in)
		if err != nil || h != tc.h || m != tc.m || s != tc.s {
			t.Fatalf("%q -> %d:%d:%d, %v", tc.in, h, m, s, err)
		}
	}
	for _, in := range []string{"", "24:00", "07:60", "abc"} {
		if _, _, _, err := parseTimeOfDay(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestNextRunAt(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 10, 26, 6, 30, 0, 0, loc)
	got, err := nextRunAt(before, "07:00:00", loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 26, 7, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	after := time.Date(2026, 10, 26, 7, 0, 1, 0, loc)
	got, err = nextRunAt(after, "07:00:00", loc)
	if err != nil {
		t.Fatal(err)
	}
	want = time.Date(2026, 10, 27, 7, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("after: got %v, want %v", got, want)
	}
}

func TestStatusReflectsSchedule(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st := newStore(t)
	sched := New(st, &fakeRunner{calls: make(chan runCall, 1)}, nil, loc, nil)

	if got := sched.Status(1); got.Enabled || got.Message != "定时打卡未启用" {
		t.Fatalf("disabled status = %+v", got)
	}
	acc := addAccount(t, st, 1, "20240001")
	if _, err := st.SaveSchedule(1, store.Schedule{
		Enabled: true, TimeOfDay: "07:30:00", AccountIDs: []int64{acc.ID},
		MaxRetries: 1, RetryIntervalMinutes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	got := sched.Status(1)
	if !got.Enabled || got.NextRun == nil || got.NextRunText == "" {
		t.Fatalf("enabled status = %+v", got)
	}
	if got.Message != "等待执行" && got.Message != "今日定时打卡已执行" {
		t.Fatalf("message = %q", got.Message)
	}
	if other := sched.Status(2); other.Enabled {
		t.Fatalf("other owner status = %+v", other)
	}
}

func TestCheckRunsDueSchedule(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st := newStore(t)
	acc := addAccount(t, st, 7, "20240002")
	now := time.Now().In(loc)
	if _, err := st.SaveSchedule(7, store.Schedule{
		Enabled:              true,
		TimeOfDay:            now.Add(-30 * time.Second).Format("15:04:05"),
		AccountIDs:           []int64{acc.ID},
		MaxRetries:           0,
		RetryIntervalMinutes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{calls: make(chan runCall, 1)}
	sched := New(st, runner, nil, loc, nil)
	sched.check(context.Background())

	select {
	case call := <-runner.calls:
		if call.ownerID != 7 || len(call.ids) != 1 || call.ids[0] != acc.ID {
			t.Fatalf("call = %+v", call)
		}
	default:
		t.Fatal("scheduler did not run due schedule")
	}
	if got := st.Schedule(7); got.LastRunDate != now.Format("2006-01-02") {
		t.Fatalf("last run date not marked: %+v", got)
	}
}

func TestCheckSkipsNotDueAlreadyRunAndStale(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)

	cases := []struct {
		name      string
		timeOfDay string
		lastDate  string
	}{
		{name: "before time", timeOfDay: now.Add(time.Hour).Format("15:04:05")},
		{name: "already ran", timeOfDay: now.Add(-30 * time.Second).Format("15:04:05"), lastDate: now.Format("2006-01-02")},
		{name: "missed window", timeOfDay: now.Add(-10 * time.Minute).Format("15:04:05")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			acc := addAccount(t, st, 1, "20240003")
			if _, err := st.SaveSchedule(1, store.Schedule{
				Enabled: true, TimeOfDay: tc.timeOfDay, AccountIDs: []int64{acc.ID},
				MaxRetries: 0, RetryIntervalMinutes: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if tc.lastDate != "" {
				if err := st.MarkScheduledRun(1, tc.lastDate, now, "ok"); err != nil {
					t.Fatal(err)
				}
			}
			runner := &fakeRunner{calls: make(chan runCall, 1)}
			sched := New(st, runner, nil, loc, nil)
			sched.check(context.Background())
			select {
			case <-runner.calls:
				t.Fatal("scheduler should not run")
			default:
			}
		})
	}
}

func TestCheckSkipsWhileRunning(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st := newStore(t)
	acc := addAccount(t, st, 1, "20240004")
	now := time.Now().In(loc)
	if _, err := st.SaveSchedule(1, store.Schedule{
		Enabled: true, TimeOfDay: now.Add(-30 * time.Second).Format("15:04:05"),
		AccountIDs: []int64{acc.ID}, MaxRetries: 0, RetryIntervalMinutes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{calls: make(chan runCall, 1), running: true}
	sched := New(st, runner, nil, loc, nil)
	sched.check(context.Background())
	select {
	case <-runner.calls:
		t.Fatal("scheduler ran while another run is active")
	default:
	}
}

func TestCheckHandlesMultipleOwnersSerially(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st := newStore(t)
	now := time.Now().In(loc)
	for _, owner := range []uint64{11, 22} {
		acc := addAccount(t, st, owner, "2024001"+string(rune('0'+owner%10)))
		if _, err := st.SaveSchedule(owner, store.Schedule{
			Enabled: true, TimeOfDay: now.Add(-30 * time.Second).Format("15:04:05"),
			AccountIDs: []int64{acc.ID}, MaxRetries: 0, RetryIntervalMinutes: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	runner := &fakeRunner{calls: make(chan runCall, 4)}
	sched := New(st, runner, nil, loc, nil)

	sched.check(context.Background())
	if len(runner.calls) != 1 {
		t.Fatalf("first check ran %d owners, want 1", len(runner.calls))
	}
	first := <-runner.calls
	if first.ownerID != 11 {
		t.Fatalf("first owner = %d, want 11", first.ownerID)
	}

	sched.check(context.Background())
	if len(runner.calls) != 1 {
		t.Fatalf("second check ran %d owners, want 1", len(runner.calls))
	}
	second := <-runner.calls
	if second.ownerID != 22 {
		t.Fatalf("second owner = %d, want 22", second.ownerID)
	}

	sched.check(context.Background())
	select {
	case call := <-runner.calls:
		t.Fatalf("third check ran %+v, both owners should be done", call)
	default:
	}
}

func TestFailedResults(t *testing.T) {
	results := []store.AccountResult{
		{Status: "success"},
		{Status: "already_checked"},
		{Status: "no_task"},
		{Status: "login_failed"},
		{Status: "error"},
	}
	failed := failedResults(results)
	if len(failed) != 2 || failed[0].Status != "login_failed" || failed[1].Status != "error" {
		t.Fatalf("failed = %+v", failed)
	}
}

func TestShouldNotify(t *testing.T) {
	failed := store.Run{Results: []store.AccountResult{{Status: "login_failed"}}}
	succeeded := store.Run{Results: []store.AccountResult{{Status: "success"}, {Status: "no_task"}}}
	base := store.Schedule{NotifyEnabled: true, NotifyEmail: "notify@example.com"}

	if !shouldNotify(base, failed) {
		t.Fatal("expected notification")
	}
	if shouldNotify(store.Schedule{NotifyEmail: "notify@example.com"}, failed) {
		t.Fatal("notified while disabled")
	}
	if shouldNotify(store.Schedule{NotifyEnabled: true}, failed) {
		t.Fatal("notified without recipient")
	}
	if shouldNotify(base, succeeded) {
		t.Fatal("notified without failures")
	}
	if shouldNotify(base, store.Run{}) {
		t.Fatal("notified without results")
	}
}

func TestCheckSendsFailureNotification(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st := newStore(t)
	acc := addAccount(t, st, 5, "20240005")
	now := time.Now().In(loc)
	if _, err := st.SaveSchedule(5, store.Schedule{
		Enabled:              true,
		TimeOfDay:            now.Add(-30 * time.Second).Format("15:04:05"),
		AccountIDs:           []int64{acc.ID},
		MaxRetries:           2,
		RetryIntervalMinutes: 1,
		NotifyEnabled:        true,
		NotifyEmail:          "notify@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		calls: make(chan runCall, 1),
		result: store.Run{
			Summary:    "成功 0，失败 1，共 1 个账号",
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
			Results: []store.AccountResult{
				{AccountID: acc.ID, StudentID: "20240005", Status: "login_failed", Message: "密码错误"},
			},
		},
	}
	notifier := &fakeNotifier{calls: make(chan notifyCall, 1)}
	sched := New(st, runner, notifier, loc, nil)
	sched.check(context.Background())

	select {
	case call := <-notifier.calls:
		if call.to != "notify@example.com" || call.maxRetries != 2 {
			t.Fatalf("notify call = %+v", call)
		}
		if len(call.run.Results) != 1 || call.run.Results[0].Status != "login_failed" {
			t.Fatalf("notify run = %+v", call.run)
		}
	default:
		t.Fatal("scheduler did not send failure notification")
	}
	if got := st.Schedule(5); !strings.Contains(got.LastResult, "失败通知已发送") {
		t.Fatalf("notification status missing: %+v", got)
	}
}

func TestStopBeforeStartDoesNotBlock(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st := newStore(t)
	sched := New(st, &fakeRunner{calls: make(chan runCall, 1)}, nil, loc, nil)
	done := make(chan struct{})
	go func() {
		sched.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop before Start blocked")
	}
}
