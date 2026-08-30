package plugin

import (
	"context"
	"strings"
	"sync"
	"time"
)

type BackgroundSchedulerStatus string

const (
	BackgroundSchedulerStopped  BackgroundSchedulerStatus = "stopped"
	BackgroundSchedulerRunning  BackgroundSchedulerStatus = "running"
	BackgroundSchedulerStopping BackgroundSchedulerStatus = "stopping"
)

type BackgroundSchedulerState struct {
	Status     BackgroundSchedulerStatus `json:"status"`
	Generation int64                     `json:"generation"`
	StartedAt  time.Time                 `json:"started_at,omitempty"`
	StoppedAt  time.Time                 `json:"stopped_at,omitempty"`
	LastTickAt time.Time                 `json:"last_tick_at,omitempty"`
	StartupRan bool                      `json:"startup_ran"`
}

type backgroundSchedulerTicker interface {
	C() <-chan time.Time
	Stop()
}

type backgroundScheduler struct {
	runner *BackgroundJobRunner

	mu        sync.Mutex
	state     BackgroundSchedulerState
	stop      context.CancelFunc
	done      chan struct{}
	now       func() time.Time
	newTicker func(time.Duration) backgroundSchedulerTicker
}

type realBackgroundSchedulerTicker struct {
	ticker *time.Ticker
}

func (t realBackgroundSchedulerTicker) C() <-chan time.Time {
	return t.ticker.C
}

func (t realBackgroundSchedulerTicker) Stop() {
	t.ticker.Stop()
}

func newBackgroundScheduler(runner *BackgroundJobRunner) *backgroundScheduler {
	return &backgroundScheduler{
		runner: runner,
		now:    func() time.Time { return time.Now().UTC() },
		newTicker: func(interval time.Duration) backgroundSchedulerTicker {
			return realBackgroundSchedulerTicker{ticker: time.NewTicker(interval)}
		},
	}
}

func (r *BackgroundJobRunner) SchedulerState() BackgroundSchedulerState {
	if r == nil || r.scheduler == nil {
		return BackgroundSchedulerState{Status: BackgroundSchedulerStopped}
	}
	return r.scheduler.State()
}

func (r *BackgroundJobRunner) StartScheduler(interval time.Duration) {
	if r == nil || r.broker == nil {
		return
	}
	if r.scheduler == nil {
		r.scheduler = newBackgroundScheduler(r)
	}
	r.scheduler.Start(interval)
}

func (r *BackgroundJobRunner) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	dones := r.beginShutdown()
	var schedulerErr error
	if r.scheduler != nil {
		schedulerErr = r.scheduler.Shutdown(ctx)
	}
	waitErr := waitBackgroundJobRuns(ctx, dones)
	r.finishShutdown()
	if schedulerErr != nil {
		return schedulerErr
	}
	return waitErr
}

func (s *backgroundScheduler) State() BackgroundSchedulerState {
	if s == nil {
		return BackgroundSchedulerState{Status: BackgroundSchedulerStopped}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	if state.Status == "" {
		state.Status = BackgroundSchedulerStopped
	}
	return state
}

func (s *backgroundScheduler) Start(interval time.Duration) {
	if s == nil || s.runner == nil || s.runner.broker == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		cancel()
		return
	}
	generation := s.state.Generation + 1
	s.stop = cancel
	s.done = done
	s.state = BackgroundSchedulerState{
		Status:     BackgroundSchedulerRunning,
		Generation: generation,
		StartedAt:  s.now().UTC(),
	}
	s.mu.Unlock()

	go s.loop(ctx, done, interval)
}

func (s *backgroundScheduler) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	stop := s.stop
	done := s.done
	if stop == nil {
		if s.state.Status == "" {
			s.state.Status = BackgroundSchedulerStopped
		}
		s.mu.Unlock()
		return nil
	}
	s.state.Status = BackgroundSchedulerStopping
	s.mu.Unlock()

	stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *backgroundScheduler) loop(ctx context.Context, done chan struct{}, interval time.Duration) {
	defer func() {
		s.markStopped(done)
		close(done)
	}()
	s.runner.runStartupJobs(ctx)
	s.markStartupRan()
	ticker := s.newTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C():
			tick := now.UTC()
			s.markTick(tick)
			s.runner.RunDue(ctx, tick, "schedule")
		}
	}
}

func (s *backgroundScheduler) markStartupRan() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.StartupRan = true
}

func (s *backgroundScheduler) markTick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.LastTickAt = now.UTC()
}

func (s *backgroundScheduler) markStopped(done chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != done {
		return
	}
	s.stop = nil
	s.done = nil
	s.state.Status = BackgroundSchedulerStopped
	s.state.StoppedAt = s.now().UTC()
}

func (r *BackgroundJobRunner) runStartupJobs(ctx context.Context) []BackgroundJobRunRecord {
	if r == nil || r.broker == nil {
		return nil
	}
	jobs := r.broker.List()
	records := make([]BackgroundJobRunRecord, 0, len(jobs))
	for _, job := range jobs {
		if ctx.Err() != nil {
			return records
		}
		if strings.TrimSpace(job.Schedule) != "@startup" {
			continue
		}
		record, _ := r.Run(ctx, BackgroundJobInvocation{
			PluginID: job.PluginID,
			JobID:    job.JobID,
			Trigger:  "startup",
		})
		records = append(records, record)
	}
	return records
}

func (r *BackgroundJobRunner) configureSchedulerForTest(now func() time.Time, newTicker func(time.Duration) backgroundSchedulerTicker) {
	if r == nil {
		return
	}
	r.scheduler = newBackgroundScheduler(r)
	if now != nil {
		r.scheduler.now = now
	}
	if newTicker != nil {
		r.scheduler.newTicker = newTicker
	}
}
