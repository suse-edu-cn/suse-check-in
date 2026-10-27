package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"suse-checkin/internal/store"
)

type fakeRunner struct {
	calls  chan []int64
	result store.Run
}

func (f *fakeRunner) RunAccounts(_ context.Context, accountIDs []int64, _ string) (store.Run, error) {
	select {
	case f.calls <- append([]int64(nil), accountIDs...):
	default:
	}
	if f.result.Results != nil {
		return f.result, nil
	}
	return store.Run{Summary: "ok", StartedAt: time.Now(), FinishedAt: time.Now()}, nil
}

func (f *fakeRunner) IsRunning() bool { return false }

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

func TestNextRun(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Date(2026, 10, 26, 6, 30, 0, 0, loc)
	got := nextRun(before, 7, 0, 0, loc)
	want := time.Date(2026, 10, 26, 7, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	after := time.Date(2026, 10, 26, 7, 0, 1, 0, loc)
	got = nextRun(after, 7, 0, 0, loc)
	want = time.Date(2026, 10, 27, 7, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("after: got %v, want %v", got, want)
	}
}

func TestSchedulerRunsAtScheduledTime(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acc, err := st.AddAccount(store.AccountInput{
		StudentID: "20240001",
		Password:  "pass",
		Campus:    "宜宾",
	})
	if err != nil {
		t.Fatal(err)
	}
	target := time.Now().In(loc).Add(3 * time.Second)
	if _, err := st.SaveSchedule(store.Schedule{
		Enabled:              true,
		TimeOfDay:            target.Format("15:04:05"),
		AccountIDs:           []int64{acc.ID},
		MaxRetries:           0,
		RetryIntervalMinutes: 1,
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{calls: make(chan []int64, 1)}
	sched := New(st, runner, nil, loc, nil)
	sched.Start()
	defer sched.Stop()

	select {
	case ids := <-runner.calls:
		if len(ids) != 1 || ids[0] != acc.ID {
			t.Fatalf("ids = %v", ids)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("scheduler did not run")
	}

	today := time.Now().In(loc).Format("2006-01-02")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st.Schedule().LastRunDate == today {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("last run date not marked, schedule = %+v", st.Schedule())
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

func TestSchedulerSendsFailureNotification(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acc, err := st.AddAccount(store.AccountInput{
		StudentID: "20240002",
		Password:  "pass",
		Campus:    "宜宾",
	})
	if err != nil {
		t.Fatal(err)
	}
	target := time.Now().In(loc).Add(3 * time.Second)
	if _, err := st.SaveSchedule(store.Schedule{
		Enabled:              true,
		TimeOfDay:            target.Format("15:04:05"),
		AccountIDs:           []int64{acc.ID},
		MaxRetries:           2,
		RetryIntervalMinutes: 1,
		NotifyEnabled:        true,
		NotifyEmail:          "notify@example.com",
	}); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		calls: make(chan []int64, 1),
		result: store.Run{
			Summary:    "成功 0，失败 1，共 1 个账号",
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
			Results: []store.AccountResult{
				{AccountID: acc.ID, StudentID: "20240002", Status: "login_failed", Message: "密码错误"},
			},
		},
	}
	notifier := &fakeNotifier{calls: make(chan notifyCall, 1)}
	sched := New(st, runner, notifier, loc, nil)
	sched.Start()
	defer sched.Stop()

	select {
	case call := <-notifier.calls:
		if call.to != "notify@example.com" || call.maxRetries != 2 {
			t.Fatalf("notify call = %+v", call)
		}
		if len(call.run.Results) != 1 || call.run.Results[0].Status != "login_failed" {
			t.Fatalf("notify run = %+v", call.run)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("scheduler did not send failure notification")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(st.Schedule().LastResult, "失败通知已发送") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("notification status missing: %+v", st.Schedule())
}

func TestStopBeforeStartDoesNotBlock(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sched := New(st, &fakeRunner{calls: make(chan []int64, 1)}, nil, loc, nil)
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
