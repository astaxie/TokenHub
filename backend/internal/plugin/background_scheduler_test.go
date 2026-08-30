package plugin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testBackgroundSchedulerTicker struct {
	c       chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newTestBackgroundSchedulerTicker() *testBackgroundSchedulerTicker {
	return &testBackgroundSchedulerTicker{
		c:       make(chan time.Time, 4),
		stopped: make(chan struct{}),
	}
}

func (t *testBackgroundSchedulerTicker) C() <-chan time.Time {
	return t.c
}

func (t *testBackgroundSchedulerTicker) Stop() {
	t.once.Do(func() {
		close(t.stopped)
	})
}

func TestBackgroundSchedulerTracksStateAndRestarts(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	runner := NewBackgroundJobRunner(NewBackgroundJobBroker())
	runner.configureSchedulerForTest(func() time.Time { return now }, nil)

	if state := runner.SchedulerState(); state.Status != BackgroundSchedulerStopped || state.Generation != 0 {
		t.Fatalf("initial scheduler state = %+v, want stopped generation 0", state)
	}

	runner.StartScheduler(time.Hour)
	state := runner.SchedulerState()
	if state.Status != BackgroundSchedulerRunning || state.Generation != 1 || !state.StartedAt.Equal(now) {
		t.Fatalf("started scheduler state = %+v, want running generation 1", state)
	}

	runner.StartScheduler(time.Hour)
	if state := runner.SchedulerState(); state.Generation != 1 {
		t.Fatalf("second start scheduler state = %+v, want same generation", state)
	}

	now = now.Add(time.Minute)
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown scheduler: %v", err)
	}
	state = runner.SchedulerState()
	if state.Status != BackgroundSchedulerStopped || state.Generation != 1 || !state.StoppedAt.Equal(now) {
		t.Fatalf("stopped scheduler state = %+v, want stopped generation 1", state)
	}

	now = now.Add(time.Minute)
	runner.StartScheduler(time.Hour)
	state = runner.SchedulerState()
	if state.Status != BackgroundSchedulerRunning || state.Generation != 2 || !state.StartedAt.Equal(now) {
		t.Fatalf("restarted scheduler state = %+v, want running generation 2", state)
	}
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown scheduler: %v", err)
	}
}

func TestBackgroundSchedulerRunsStartupJobsOncePerGeneration(t *testing.T) {
	broker := NewBackgroundJobBroker()
	startupRuns := make(chan string, 4)
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "startup",
		Schedule:       "@startup",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(_ context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
		startupRuns <- invocation.Trigger
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register startup job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)

	runner.StartScheduler(time.Hour)
	if trigger := waitForBackgroundSchedulerValue(t, startupRuns); trigger != "startup" {
		t.Fatalf("startup trigger = %q, want startup", trigger)
	}
	waitForBackgroundSchedulerState(t, runner, func(state BackgroundSchedulerState) bool {
		return state.Status == BackgroundSchedulerRunning && state.StartupRan && state.Generation == 1
	})

	runner.StartScheduler(time.Hour)
	assertNoBackgroundSchedulerValue(t, startupRuns)

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown scheduler: %v", err)
	}
	runner.StartScheduler(time.Hour)
	if trigger := waitForBackgroundSchedulerValue(t, startupRuns); trigger != "startup" {
		t.Fatalf("restart startup trigger = %q, want startup", trigger)
	}
	waitForBackgroundSchedulerState(t, runner, func(state BackgroundSchedulerState) bool {
		return state.Status == BackgroundSchedulerRunning && state.StartupRan && state.Generation == 2
	})
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown scheduler: %v", err)
	}
}

func TestBackgroundSchedulerTicksRunDueJobs(t *testing.T) {
	broker := NewBackgroundJobBroker()
	scheduledRuns := make(chan string, 4)
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(_ context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
		scheduledRuns <- invocation.Trigger
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register scheduled job: %v", err)
	}

	ticker := newTestBackgroundSchedulerTicker()
	tickers := make(chan *testBackgroundSchedulerTicker, 1)
	now := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	runner := NewBackgroundJobRunner(broker)
	runner.configureSchedulerForTest(func() time.Time { return now }, func(time.Duration) backgroundSchedulerTicker {
		tickers <- ticker
		return ticker
	})

	runner.StartScheduler(time.Hour)
	<-tickers

	tickAt := now.Add(10 * time.Minute)
	ticker.c <- tickAt
	if trigger := waitForBackgroundSchedulerValue(t, scheduledRuns); trigger != "schedule" {
		t.Fatalf("scheduled trigger = %q, want schedule", trigger)
	}
	waitForBackgroundSchedulerState(t, runner, func(state BackgroundSchedulerState) bool {
		return state.LastTickAt.Equal(tickAt)
	})
	if lastRuns := runner.LastRuns(); len(lastRuns) != 1 || lastRuns[0].JobID != "quota.refresh" {
		t.Fatalf("last runs = %+v, want scheduled quota.refresh", lastRuns)
	}

	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown scheduler: %v", err)
	}
	select {
	case <-ticker.stopped:
	case <-time.After(time.Second):
		t.Fatal("ticker was not stopped")
	}
}

func TestBackgroundSchedulerShutdownTimeoutEventuallyResumes(t *testing.T) {
	broker := NewBackgroundJobBroker()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var block atomic.Bool
	block.Store(true)
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "startup",
		Schedule:       "@startup",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		started <- struct{}{}
		if block.Load() {
			<-release
		}
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register startup job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)

	runner.StartScheduler(time.Hour)
	waitForBackgroundSchedulerSignal(t, started)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runner.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if state := runner.SchedulerState(); state.Status != BackgroundSchedulerStopping {
		t.Fatalf("scheduler state after timed out shutdown = %+v, want stopping", state)
	}

	block.Store(false)
	close(release)
	waitForBackgroundSchedulerState(t, runner, func(state BackgroundSchedulerState) bool {
		return state.Status == BackgroundSchedulerStopped
	})

	runner.StartScheduler(time.Hour)
	waitForBackgroundSchedulerSignal(t, started)
	if state := runner.SchedulerState(); state.Status != BackgroundSchedulerRunning || state.Generation != 2 {
		t.Fatalf("restarted scheduler state = %+v, want running generation 2", state)
	}
	if err := runner.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown scheduler: %v", err)
	}
}

func waitForBackgroundSchedulerValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatal("timed out waiting for scheduler value")
		return zero
	}
}

func assertNoBackgroundSchedulerValue[T any](t *testing.T, values <-chan T) {
	t.Helper()
	select {
	case value := <-values:
		t.Fatalf("unexpected scheduler value: %+v", value)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForBackgroundSchedulerSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduler signal")
	}
}

func waitForBackgroundSchedulerState(t *testing.T, runner *BackgroundJobRunner, done func(BackgroundSchedulerState) bool) BackgroundSchedulerState {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := runner.SchedulerState()
		if done(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	state := runner.SchedulerState()
	t.Fatalf("scheduler state = %+v did not satisfy condition", state)
	return state
}
