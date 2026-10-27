package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"suse-checkin/internal/store"
)

type Runner interface {
	RunAccounts(ctx context.Context, accountIDs []int64, trigger string) (store.Run, error)
	IsRunning() bool
}

type Notifier interface {
	NotifyFailure(ctx context.Context, to string, run store.Run, maxRetries int) error
}

type Status struct {
	Enabled     bool       `json:"enabled"`
	NextRun     *time.Time `json:"nextRun,omitempty"`
	NextRunText string     `json:"nextRunText,omitempty"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
	LastResult  string     `json:"lastResult,omitempty"`
	Message     string     `json:"message,omitempty"`
}

type Scheduler struct {
	store    *store.Store
	runner   Runner
	notifier Notifier
	loc      *time.Location
	logger   *log.Logger
	now      func() time.Time

	wake chan struct{}
	stop chan struct{}
	done chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	status  Status
}

func New(st *store.Store, runner Runner, notifier Notifier, loc *time.Location, logger *log.Logger) *Scheduler {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	s := &Scheduler{
		store:    st,
		runner:   runner,
		notifier: notifier,
		loc:      loc,
		logger:   logger,
		now:      time.Now,
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	sch := st.Schedule()
	s.status = Status{
		Enabled:    sch.Enabled,
		LastRunAt:  copyTimePtr(sch.LastRunAt),
		LastResult: sch.LastResult,
		Message:    "调度器未启动",
	}
	return s
}

func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.started = true
		s.cancel = cancel
		s.mu.Unlock()
		go func() {
			defer close(s.done)
			s.loop(ctx)
		}()
	})
}

func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.mu.Lock()
		cancel := s.cancel
		started := s.started
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if started {
			<-s.done
		}
	})
}

func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	out.NextRun = copyTimePtr(s.status.NextRun)
	out.LastRunAt = copyTimePtr(s.status.LastRunAt)
	return out
}

func (s *Scheduler) loop(ctx context.Context) {
	for {
		schedule := s.store.Schedule()
		s.syncFromSchedule(schedule)
		if !schedule.Enabled {
			s.setMessage("定时打卡未启用")
			s.clearNextRun()
			if !s.waitForWake(ctx) {
				return
			}
			continue
		}
		if len(schedule.AccountIDs) == 0 {
			s.setMessage("定时打卡已启用，但未选择账号")
			s.clearNextRun()
			if !s.waitForWake(ctx) {
				return
			}
			continue
		}
		hour, minute, second, err := parseTimeOfDay(schedule.TimeOfDay)
		if err != nil {
			s.setMessage("定时时间无效: " + err.Error())
			s.clearNextRun()
			if !s.waitForWake(ctx) {
				return
			}
			continue
		}

		next := nextRun(s.now().In(s.loc), hour, minute, second, s.loc)
		s.setNextRun(next)
		s.setMessage("等待执行")
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.stop:
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}

		schedule = s.store.Schedule()
		if !schedule.Enabled || len(schedule.AccountIDs) == 0 {
			continue
		}
		today := s.now().In(s.loc).Format("2006-01-02")
		if schedule.LastRunDate == today {
			s.setMessage("今日定时打卡已执行")
			s.clearNextRun()
			continue
		}

		ready := s.waitForRunner(ctx, 10*time.Minute)
		if !ready {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			default:
			}
			s.setMessage("另一个打卡任务仍在运行，跳过本次定时打卡")
			continue
		}

		s.setMessage("正在执行定时打卡")
		run, err := s.runner.RunAccounts(ctx, schedule.AccountIDs, "scheduled")
		finished := s.now().In(s.loc)
		if err != nil {
			s.logger.Printf("[scheduler] 定时打卡失败: %v", err)
			if errors.Is(err, context.Canceled) {
				return
			}
			s.setMessage("执行失败: " + err.Error())
			continue
		}
		summary := run.Summary
		if summary == "" {
			summary = "定时打卡完成"
		}
		if shouldNotify(schedule, run) {
			summary = s.sendFailureNotice(schedule, run, summary)
		}
		if err := s.store.MarkScheduledRun(today, finished, summary); err != nil {
			s.logger.Printf("[scheduler] 保存定时结果失败: %v", err)
		}
		s.mu.Lock()
		s.status.LastRunAt = &finished
		s.status.LastResult = summary
		s.status.Message = "定时打卡完成"
		s.status.NextRun = nil
		s.status.NextRunText = ""
		s.mu.Unlock()
	}
}

func (s *Scheduler) sendFailureNotice(schedule store.Schedule, run store.Run, summary string) string {
	if s.notifier == nil {
		s.logger.Printf("[scheduler] 已开启失败通知，但邮件服务未配置")
		return summary + "（失败通知未发送：邮件服务未配置）"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.notifier.NotifyFailure(ctx, schedule.NotifyEmail, run, schedule.MaxRetries); err != nil {
		s.logger.Printf("[scheduler] 发送失败通知失败: %v", err)
		return summary + "（失败通知发送失败）"
	}
	return summary + "（失败通知已发送）"
}

func shouldNotify(schedule store.Schedule, run store.Run) bool {
	return schedule.NotifyEnabled && strings.TrimSpace(schedule.NotifyEmail) != "" && len(failedResults(run.Results)) > 0
}

func failedResults(results []store.AccountResult) []store.AccountResult {
	out := make([]store.AccountResult, 0, len(results))
	for _, result := range results {
		switch result.Status {
		case "success", "already_checked", "no_task":
			continue
		default:
			out = append(out, result)
		}
	}
	return out
}

func (s *Scheduler) waitForWake(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.stop:
		return false
	case <-s.wake:
		return true
	}
}

func (s *Scheduler) waitForRunner(ctx context.Context, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for s.runner.IsRunning() {
		if time.Now().After(deadline) {
			return false
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-s.stop:
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return true
}

func (s *Scheduler) syncFromSchedule(schedule store.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Enabled = schedule.Enabled
	s.status.LastRunAt = copyTimePtr(schedule.LastRunAt)
	s.status.LastResult = schedule.LastResult
}

func (s *Scheduler) setNextRun(next time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.NextRun = &next
	s.status.NextRunText = next.Format("2006-01-02 15:04:05")
}

func (s *Scheduler) clearNextRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.NextRun = nil
	s.status.NextRunText = ""
}

func (s *Scheduler) setMessage(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Message = message
}

func parseTimeOfDay(value string) (int, int, int, error) {
	for _, layout := range []string{"15:04:05", "15:04"} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t.Hour(), t.Minute(), t.Second(), nil
		}
	}
	return 0, 0, 0, fmt.Errorf("时间格式应为 HH:MM 或 HH:MM:SS")
}

func nextRun(now time.Time, hour, minute, second int, loc *time.Location) time.Time {
	current := now.In(loc)
	target := time.Date(current.Year(), current.Month(), current.Day(), hour, minute, second, 0, loc)
	if !target.After(current) {
		target = target.AddDate(0, 0, 1)
	}
	return target
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
