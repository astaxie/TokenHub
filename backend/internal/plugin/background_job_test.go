package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackgroundJobRunnerExecutesRegisteredHandler(t *testing.T) {
	broker := NewBackgroundJobBroker()
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
			},
		},
	}, BackgroundJobHandlerFunc(func(_ context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
		var payload map[string]string
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return BackgroundJobResult{Data: map[string]string{"resource_id": payload["resource_id"]}}, nil
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}

	record, err := NewBackgroundJobRunner(broker).Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
		Payload:  json.RawMessage(`{"resource_id":"res_1"}`),
	})
	if err != nil {
		t.Fatalf("run background job: %v", err)
	}
	if record.Status != BackgroundJobRunSucceeded || record.Attempts != 1 || record.Trigger != "manual" {
		t.Fatalf("run record = %+v", record)
	}
	data := record.Result.Data.(map[string]string)
	if data["resource_id"] != "res_1" {
		t.Fatalf("result data = %+v, want res_1", data)
	}
}

func TestBackgroundJobRunnerValidatesInputBeforeHandler(t *testing.T) {
	broker := NewBackgroundJobBroker()
	calls := 0
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
			},
		},
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		calls++
		return BackgroundJobResult{}, nil
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}

	record, err := NewBackgroundJobRunner(broker).Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
		Payload:  json.RawMessage(`{"resource_id":7}`),
	})
	if !errors.Is(err, ErrPluginBackgroundJobInvalidPayload) {
		t.Fatalf("error = %v, want ErrPluginBackgroundJobInvalidPayload", err)
	}
	if record.Status != BackgroundJobRunFailed || calls != 0 {
		t.Fatalf("run record=%+v calls=%d, want failed without handler call", record, calls)
	}
}

func TestBackgroundJobRunnerEnforcesPermissionGrantsBeforeCommandHandler(t *testing.T) {
	broker := NewBackgroundJobBroker()
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "credentials.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		Permissions: []PermissionDescriptor{
			{Kind: PermissionKindData, Name: string(DataProviderCredentials), Access: PermissionAccessRead},
		},
	}, NewBackgroundCommandRunner(t.TempDir(), "missing.sh", PermissionGrant{Enforced: true})); err != nil {
		t.Fatalf("register background job: %v", err)
	}

	record, err := NewBackgroundJobRunner(broker).Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "credentials.refresh",
	})
	if err == nil {
		t.Fatal("background command executed without its required permission grant")
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorPermissionRequired {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorPermissionRequired, err)
	}
	if record.Status != BackgroundJobRunFailed || record.Attempts != 1 {
		t.Fatalf("record = %+v, want failed first attempt", record)
	}
}

func TestBackgroundJobRunnerRetriesFailures(t *testing.T) {
	broker := NewBackgroundJobBroker()
	calls := 0
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		Retry:          BackgroundJobRetryPolicy{MaxAttempts: 3},
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		calls++
		if calls < 3 {
			return BackgroundJobResult{}, errors.New("temporary failure")
		}
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}

	record, err := NewBackgroundJobRunner(broker).Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
	})
	if err != nil {
		t.Fatalf("run background job: %v", err)
	}
	if record.Status != BackgroundJobRunSucceeded || record.Attempts != 3 || calls != 3 {
		t.Fatalf("run record=%+v calls=%d, want success after three attempts", record, calls)
	}
}

func TestBackgroundJobRunnerAppliesExponentialBackoffAndJitter(t *testing.T) {
	broker := NewBackgroundJobBroker()
	calls := 0
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		Retry: BackgroundJobRetryPolicy{
			MaxAttempts:       4,
			BackoffMillis:     100,
			BackoffMultiplier: 2,
			JitterMillis:      25,
		},
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		calls++
		if calls < 4 {
			return BackgroundJobResult{}, errors.New("temporary failure")
		}
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}
	var delays []time.Duration
	runner := NewBackgroundJobRunner(broker)
	runner.configureRetryForTest(func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}, func(max time.Duration) time.Duration {
		return max / 5
	})

	record, err := runner.Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
	})
	if err != nil {
		t.Fatalf("run background job: %v", err)
	}
	if record.Status != BackgroundJobRunSucceeded || record.Attempts != 4 || calls != 4 {
		t.Fatalf("run record=%+v calls=%d, want success after four attempts", record, calls)
	}
	want := []time.Duration{105 * time.Millisecond, 205 * time.Millisecond, 405 * time.Millisecond}
	if !equalBackgroundJobDurations(delays, want) {
		t.Fatalf("retry delays = %v, want %v", delays, want)
	}
}

func TestBackgroundJobRunnerStopsRetryWhenBackoffContextCancels(t *testing.T) {
	broker := NewBackgroundJobBroker()
	calls := 0
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		Retry:          BackgroundJobRetryPolicy{MaxAttempts: 3, BackoffMillis: 100},
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		calls++
		return BackgroundJobResult{}, errors.New("temporary failure")
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)
	runner.configureRetryForTest(func(context.Context, time.Duration) error {
		return context.Canceled
	}, nil)

	record, err := runner.Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if record.Status != BackgroundJobRunFailed || record.Attempts != 1 || calls != 1 {
		t.Fatalf("run record=%+v calls=%d, want one failed attempt", record, calls)
	}
}

func TestBackgroundJobRunnerRecordsDeadLettersAfterRetryExhaustion(t *testing.T) {
	broker := NewBackgroundJobBroker()
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
		Retry:          BackgroundJobRetryPolicy{MaxAttempts: 2, DeadLetter: true},
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		return BackgroundJobResult{}, errors.New("permanent failure")
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}

	runner := NewBackgroundJobRunner(broker)
	record, err := runner.Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
	})
	if err == nil {
		t.Fatal("run background job succeeded unexpectedly")
	}
	if record.Status != BackgroundJobRunFailed || record.Attempts != 2 {
		t.Fatalf("run record = %+v, want failed after two attempts", record)
	}
	letters := runner.DeadLetters()
	if len(letters) != 1 || letters[0].PluginID != "tokenhub.jobs" || letters[0].JobID != "quota.refresh" {
		t.Fatalf("dead letters = %+v, want failed quota.refresh record", letters)
	}
	if letters[0].Error != "permanent failure" || letters[0].Attempts != 2 {
		t.Fatalf("dead letter = %+v, want exhausted failure", letters[0])
	}
}

func TestBackgroundJobRunnerAppliesAttemptTimeout(t *testing.T) {
	broker := NewBackgroundJobBroker()
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		TimeoutMillis:  10,
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(ctx context.Context, _ BackgroundJobInvocation) (BackgroundJobResult, error) {
		<-ctx.Done()
		return BackgroundJobResult{}, ctx.Err()
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}

	record, err := NewBackgroundJobRunner(broker).Run(context.Background(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if record.Status != BackgroundJobRunFailed || record.Attempts != 1 {
		t.Fatalf("run record = %+v, want failed first attempt", record)
	}
}

func TestBackgroundJobRunnerShutdownCancelsInFlightRunsAndResumes(t *testing.T) {
	broker := NewBackgroundJobBroker()
	started := make(chan struct{}, 2)
	canceled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var immediate atomic.Bool
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(ctx context.Context, _ BackgroundJobInvocation) (BackgroundJobResult, error) {
		started <- struct{}{}
		if immediate.Load() {
			return BackgroundJobResult{Data: "ok"}, nil
		}
		<-ctx.Done()
		close(canceled)
		<-release
		return BackgroundJobResult{}, ctx.Err()
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)
	runDone := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
		runDone <- err
	}()
	waitForBackgroundJobSignal(t, started)

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- runner.Shutdown(context.Background())
	}()
	waitForBackgroundJobSignal(t, canceled)

	record, err := runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
	if !errors.Is(err, ErrPluginBackgroundJobShuttingDown) {
		t.Fatalf("error = %v, want ErrPluginBackgroundJobShuttingDown", err)
	}
	if record.Status != BackgroundJobRunSkipped {
		t.Fatalf("run record = %+v, want skipped while shutting down", record)
	}

	releaseOnce.Do(func() { close(release) })
	if err := waitForBackgroundJobError(t, runDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight error = %v, want context canceled", err)
	}
	if err := waitForBackgroundJobError(t, shutdownDone); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	immediate.Store(true)
	record, err = runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
	if err != nil {
		t.Fatalf("run after shutdown: %v", err)
	}
	if record.Status != BackgroundJobRunSucceeded {
		t.Fatalf("run after shutdown record = %+v, want succeeded", record)
	}
}

func TestBackgroundJobRunnerShutdownTimeoutRejectsUntilRunsFinish(t *testing.T) {
	broker := NewBackgroundJobBroker()
	started := make(chan struct{}, 2)
	canceled := make(chan struct{})
	release := make(chan struct{})
	var immediate atomic.Bool
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(ctx context.Context, _ BackgroundJobInvocation) (BackgroundJobResult, error) {
		started <- struct{}{}
		if immediate.Load() {
			return BackgroundJobResult{Data: "ok"}, nil
		}
		<-ctx.Done()
		close(canceled)
		<-release
		return BackgroundJobResult{}, ctx.Err()
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)
	runDone := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
		runDone <- err
	}()
	waitForBackgroundJobSignal(t, started)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runner.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	waitForBackgroundJobSignal(t, canceled)
	record, err := runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
	if !errors.Is(err, ErrPluginBackgroundJobShuttingDown) {
		t.Fatalf("error = %v, want ErrPluginBackgroundJobShuttingDown", err)
	}
	if record.Status != BackgroundJobRunSkipped {
		t.Fatalf("run record = %+v, want skipped while timed-out shutdown is draining", record)
	}

	close(release)
	if err := waitForBackgroundJobError(t, runDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight error = %v, want context canceled", err)
	}
	immediate.Store(true)
	record, err = runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
	if err != nil {
		t.Fatalf("run after timed-out shutdown drain: %v", err)
	}
	if record.Status != BackgroundJobRunSucceeded {
		t.Fatalf("run after timed-out shutdown drain record = %+v, want succeeded", record)
	}
}

func TestBackgroundJobRunnerEnforcesConcurrencyLimit(t *testing.T) {
	broker := NewBackgroundJobBroker()
	started := make(chan struct{})
	release := make(chan struct{})
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "10m",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(ctx context.Context, _ BackgroundJobInvocation) (BackgroundJobResult, error) {
		close(started)
		select {
		case <-release:
			return BackgroundJobResult{Data: "ok"}, nil
		case <-ctx.Done():
			return BackgroundJobResult{}, ctx.Err()
		}
	})); err != nil {
		t.Fatalf("register background job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
	}()
	<-started

	record, err := runner.Run(context.Background(), BackgroundJobInvocation{PluginID: "tokenhub.jobs", JobID: "quota.refresh"})
	if !errors.Is(err, ErrPluginBackgroundJobBusy) {
		t.Fatalf("error = %v, want ErrPluginBackgroundJobBusy", err)
	}
	if record.Status != BackgroundJobRunSkipped {
		t.Fatalf("run record = %+v, want skipped", record)
	}
	close(release)
	wg.Wait()
}

func TestBackgroundJobRunnerRunsDueJobsAndTracksLastRuns(t *testing.T) {
	broker := NewBackgroundJobBroker()
	calls := 0
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "startup",
		Schedule:       "@startup",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		calls++
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register startup job: %v", err)
	}
	if err := broker.Register(BackgroundJobDescriptor{
		PluginID:       "tokenhub.jobs",
		JobID:          "quota.refresh",
		Schedule:       "*/10 * * * *",
		MaxConcurrency: 1,
	}, BackgroundJobHandlerFunc(func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		calls++
		return BackgroundJobResult{Data: "ok"}, nil
	})); err != nil {
		t.Fatalf("register scheduled job: %v", err)
	}
	runner := NewBackgroundJobRunner(broker)
	now := time.Now().UTC()

	records := runner.RunDue(context.Background(), now, "schedule")
	if len(records) != 2 || calls != 2 {
		t.Fatalf("first due records=%+v calls=%d, want both jobs", records, calls)
	}
	records = runner.RunDue(context.Background(), now.Add(5*time.Minute), "schedule")
	if len(records) != 0 || calls != 2 {
		t.Fatalf("early due records=%+v calls=%d, want none", records, calls)
	}
	records = runner.RunDue(context.Background(), now.Add(11*time.Minute), "schedule")
	if len(records) != 1 || records[0].JobID != "quota.refresh" || calls != 3 {
		t.Fatalf("later due records=%+v calls=%d, want only scheduled job", records, calls)
	}
	last := runner.LastRuns()
	if len(last) != 2 {
		t.Fatalf("last runs = %+v, want two entries", last)
	}
}

func waitForBackgroundJobSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background job signal")
	}
}

func waitForBackgroundJobError(t *testing.T, errors <-chan error) error {
	t.Helper()
	select {
	case err := <-errors:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background job error")
		return nil
	}
}

func equalBackgroundJobDurations(left []time.Duration, right []time.Duration) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestBackgroundJobScheduleInterval(t *testing.T) {
	for _, testCase := range []struct {
		schedule string
		want     time.Duration
	}{
		{schedule: "30s", want: 30 * time.Second},
		{schedule: "*/15 * * * *", want: 15 * time.Minute},
	} {
		got, ok := backgroundJobScheduleInterval(testCase.schedule)
		if !ok || got != testCase.want {
			t.Fatalf("interval %q = %v, %v, want %v true", testCase.schedule, got, ok, testCase.want)
		}
	}
	for _, schedule := range []string{"0 * * * *", "*/0 * * * *", "*/x * * * *", "* * *", ""} {
		if got, ok := backgroundJobScheduleInterval(schedule); ok {
			t.Fatalf("interval %q = %v, true; want false", schedule, got)
		}
	}
}
