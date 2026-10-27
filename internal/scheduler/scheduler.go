package scheduler

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"suse-checkin/internal/store"
)

const (
	tickInterval  = 30 * time.Second
	catchupWindow = 2 * time.Minute
)

type Runner interface {
	RunAccounts(ctx context.Context, ownerID uint64, accountIDs []int64, trigger string) (store.Run, error)
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

	mu           sync.Mutex
	started      bool
	cancel       context.CancelFunc
	runningOwner uint64
}

func New(st *store.Store, runner Runner, notifier Notifier, loc *time.Location, logger *log.Logger) *Scheduler {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Scheduler{
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

// Wake 让调度器立即检查一次（保存定时设置、删除账号后调用）。
func (s *Scheduler) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Status 返回某个 OAA 用户视角的调度状态。
func (s *Scheduler) Status(ownerID uint64) Status {
	sch := s.store.Schedule(ownerID)
	now := s.now().In(s.loc)
	st := Status{
		Enabled:    sch.Enabled,
		LastRunAt:  copyTimePtr(sch.LastRunAt),
		LastResult: sch.LastResult,
	}
	switch {
	case !sch.Enabled:
		st.Message = "定时打卡未启用"
	case len(sch.AccountIDs) == 0:
		st.Message = "定时打卡已启用，但未选择账号"
	default:
		if next, err := nextRunAt(now, sch.TimeOfDay, s.loc); err == nil {
			st.NextRun = &next
			st.NextRunText = next.Format("2006-01-02 15:04:05")
		}
		if sch.LastRunDate == now.Format("2006-01-02") {
			st.Message = "今日定时打卡已执行"
		} else {
			st.Message = "等待执行"
		}
	}
	if s.currentOwner() == ownerID && s.runner.IsRunning() {
		st.Message = "正在执行定时打卡"
	}
	return st
}

func (s *Scheduler) currentOwner() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runningOwner
}

func (s *Scheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-s.wake:
			s.check(ctx)
		case <-ticker.C:
			s.check(ctx)
		}
	}
}

// check 遍历所有启用定时的用户，到点且今日未执行时串行执行一次。
func (s *Scheduler) check(ctx context.Context) {
	if s.runner.IsRunning() {
		return
	}
	plans, err := s.store.ScheduledPlans()
	if err != nil {
		s.logger.Printf("[scheduler] 读取定时计划失败: %v", err)
		return
	}
	now := s.now().In(s.loc)
	today := now.Format("2006-01-02")
	for _, plan := range plans {
		if len(plan.AccountIDs) == 0 || plan.Schedule.LastRunDate == today {
			continue
		}
		target, err := timeOfDayAt(now, plan.Schedule.TimeOfDay, s.loc)
		if err != nil {
			s.logger.Printf("[scheduler] 用户 %d 定时时间无效: %v", plan.OwnerID, err)
			continue
		}
		if now.Before(target) || now.Sub(target) > catchupWindow {
			continue
		}
		s.runPlan(ctx, plan, today)
		return
	}
}

func (s *Scheduler) runPlan(ctx context.Context, plan store.ScheduledPlan, today string) {
	s.setRunningOwner(plan.OwnerID)
	defer s.setRunningOwner(0)

	run, err := s.runner.RunAccounts(ctx, plan.OwnerID, plan.AccountIDs, "scheduled")
	finished := s.now().In(s.loc)
	if err != nil {
		s.logger.Printf("[scheduler] 用户 %d 定时打卡失败: %v", plan.OwnerID, err)
		return
	}
	summary := run.Summary
	if summary == "" {
		summary = "定时打卡完成"
	}
	if shouldNotify(plan.Schedule, run) {
		summary = s.sendFailureNotice(plan.Schedule, run, summary)
	}
	if err := s.store.MarkScheduledRun(plan.OwnerID, today, finished, summary); err != nil {
		s.logger.Printf("[scheduler] 用户 %d 保存定时结果失败: %v", plan.OwnerID, err)
	}
}

func (s *Scheduler) setRunningOwner(ownerID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runningOwner = ownerID
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

func parseTimeOfDay(value string) (int, int, int, error) {
	for _, layout := range []string{"15:04:05", "15:04"} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t.Hour(), t.Minute(), t.Second(), nil
		}
	}
	return 0, 0, 0, fmt.Errorf("时间格式应为 HH:MM 或 HH:MM:SS")
}

// timeOfDayAt 返回 now 当天的定时时刻。
func timeOfDayAt(now time.Time, timeOfDay string, loc *time.Location) (time.Time, error) {
	hour, minute, second, err := parseTimeOfDay(timeOfDay)
	if err != nil {
		return time.Time{}, err
	}
	current := now.In(loc)
	return time.Date(current.Year(), current.Month(), current.Day(), hour, minute, second, 0, loc), nil
}

// nextRunAt 返回严格晚于 now 的下一次定时时刻。
func nextRunAt(now time.Time, timeOfDay string, loc *time.Location) (time.Time, error) {
	target, err := timeOfDayAt(now, timeOfDay, loc)
	if err != nil {
		return time.Time{}, err
	}
	if !target.After(now.In(loc)) {
		target = target.AddDate(0, 0, 1)
	}
	return target, nil
}

func copyTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
