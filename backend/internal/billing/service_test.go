package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

type fakeBillingStore struct {
	connector Connector
	started   SyncRun
	finished  SyncRun
	pages     []Record
	check     string
	due       []Connector
	audits    []SyncRun
	startErr  error
	saveErr   error
	finishErr error
	getErr    error
}

func (f *fakeBillingStore) CreateBillingConnector(connector Connector) (Connector, error) {
	f.connector = connector
	return connector, nil
}
func (f *fakeBillingStore) ListBillingConnectors() []Connector { return []Connector{f.connector} }
func (f *fakeBillingStore) GetBillingConnector(id string, _ bool) (Connector, error) {
	if f.getErr != nil {
		return Connector{}, f.getErr
	}
	if id != f.connector.ID {
		return Connector{}, errors.New("connector not found")
	}
	return f.connector, nil
}
func (f *fakeBillingStore) UpdateBillingConnector(_ string, connector Connector) (Connector, error) {
	f.connector = connector
	return connector, nil
}
func (f *fakeBillingStore) DeleteBillingConnector(_ string) error { return nil }
func (f *fakeBillingStore) StartBillingSyncRun(run SyncRun) (SyncRun, error) {
	if f.startErr != nil {
		return SyncRun{}, f.startErr
	}
	run.ID = "run-1"
	run.StartedAt = time.Now().UTC()
	f.started = run
	return run, nil
}
func (f *fakeBillingStore) SaveBillingPage(_ string, checkpoint string, records []Record) (int, int, error) {
	if f.saveErr != nil {
		return 0, 0, f.saveErr
	}
	f.check = checkpoint
	f.pages = append(f.pages, records...)
	return len(records), 0, nil
}
func (f *fakeBillingStore) FinishBillingSyncRun(run SyncRun) (SyncRun, error) {
	f.finished = run
	if f.finishErr != nil {
		return SyncRun{}, f.finishErr
	}
	return run, nil
}
func (f *fakeBillingStore) ListBillingRecords(_ string, _ int) []Record { return f.pages }
func (f *fakeBillingStore) ListBillingSyncRuns(_ string, _ int) []SyncRun {
	return []SyncRun{f.finished}
}
func (f *fakeBillingStore) ListDueBillingConnectors(_ time.Time, _ int) []Connector { return f.due }
func (f *fakeBillingStore) RecordScheduledBillingAudit(run SyncRun)                 { f.audits = append(f.audits, run) }

type fakeBillingAdapter struct {
	pages  []FetchPage
	calls  int
	errors []error
	fetch  func(context.Context, Connector, FetchRequest) (FetchPage, error)
}

func (a *fakeBillingAdapter) Fetch(ctx context.Context, connector Connector, request FetchRequest) (FetchPage, error) {
	if a.fetch != nil {
		return a.fetch(ctx, connector, request)
	}
	index := a.calls
	a.calls++
	if index < len(a.errors) && a.errors[index] != nil {
		return FetchPage{}, a.errors[index]
	}
	if index >= len(a.pages) {
		return FetchPage{}, errors.New("unexpected page")
	}
	return a.pages[index], nil
}

type retryableBillingError struct{}

func (retryableBillingError) Error() string             { return "temporary upstream failure" }
func (retryableBillingError) Retryable() bool           { return true }
func (retryableBillingError) RetryAfter() time.Duration { return 0 }
func (retryableBillingError) StatusCode() int           { return http.StatusBadGateway }
func (retryableBillingError) ErrorCode() string         { return "temporary_failure" }
func (retryableBillingError) ErrorMessage() string      { return "temporary upstream failure" }
func (retryableBillingError) ErrorKind() ErrorKind      { return ErrorUpstream }

func TestServiceSyncPersistsAllPages(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	adapter := &fakeBillingAdapter{pages: []FetchPage{
		{Records: []Record{{ExternalID: "record-1"}}, NextCursor: "next"},
		{Records: []Record{{ExternalID: "record-2"}}},
	}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if run.Status != SyncSucceeded || run.PagesFetched != 2 || run.RecordsInserted != 2 {
		t.Fatalf("unexpected completed run: %+v", run)
	}
	if len(store.pages) != 2 || store.check == "" {
		t.Fatalf("expected persisted pages and checkpoint, pages=%d checkpoint=%q", len(store.pages), store.check)
	}
}

func TestServiceSyncRetriesRetryableAdapterErrors(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive, Config: map[string]string{"max_retries": "2", "retry_base_ms": "1"}}}
	adapter := &fakeBillingAdapter{
		errors: []error{retryableBillingError{}},
		pages:  []FetchPage{{}, {Records: []Record{{ExternalID: "record-1"}}}},
	}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	if err != nil {
		t.Fatalf("sync failed after retry: %v", err)
	}
	if adapter.calls != 2 || run.Attempts != 2 || run.Status != SyncSucceeded {
		t.Fatalf("expected one retry and successful run, calls=%d attempts=%d run=%+v", adapter.calls, run.Attempts, run)
	}
}

func TestServiceSyncRecordsRetryExhaustion(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive, Config: map[string]string{"max_retries": "3", "retry_base_ms": "1"}}}
	adapter := &fakeBillingAdapter{errors: []error{retryableBillingError{}, retryableBillingError{}, retryableBillingError{}}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	assertBillingError(t, err, ErrorUpstream, "temporary_failure")
	if adapter.calls != 3 || run.Attempts != 3 || run.Status != SyncFailed || store.finished.ErrorCode != "temporary_failure" {
		t.Fatalf("retry exhaustion was not recorded: calls=%d run=%+v finished=%+v", adapter.calls, run, store.finished)
	}
}

func TestServiceSyncRejectsConcurrentRun(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	entered := make(chan struct{})
	release := make(chan struct{})
	adapter := &fakeBillingAdapter{fetch: func(context.Context, Connector, FetchRequest) (FetchPage, error) {
		close(entered)
		<-release
		return FetchPage{}, nil
	}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})
	result := make(chan error, 1)
	go func() {
		_, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
		result <- err
	}()
	<-entered
	_, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	assertBillingError(t, err, ErrorConflict, "billing_sync_in_progress")
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
}

func TestServiceSyncRecordsStartAndFinishFailures(t *testing.T) {
	request := SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}
	t.Run("start", func(t *testing.T) {
		failure := errors.New("cannot start run")
		store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}, startErr: failure}
		service := NewService(store, map[string]Adapter{ConnectorOneAPI: &fakeBillingAdapter{}})
		_, err := service.Sync(context.Background(), "conn-1", request, "manual")
		if !errors.Is(err, failure) || store.finished.ID != "" {
			t.Fatalf("unexpected start failure: err=%v finished=%+v", err, store.finished)
		}
	})
	t.Run("finish", func(t *testing.T) {
		failure := errors.New("cannot finish run")
		store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}, finishErr: failure}
		service := NewService(store, map[string]Adapter{ConnectorOneAPI: &fakeBillingAdapter{pages: []FetchPage{{}}}})
		_, err := service.Sync(context.Background(), "conn-1", request, "manual")
		if !errors.Is(err, failure) || store.finished.Status != SyncSucceeded {
			t.Fatalf("unexpected finish failure: err=%v finished=%+v", err, store.finished)
		}
	})
}

func TestServiceSyncStopsAtPageLimit(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	adapter := &fakeBillingAdapter{fetch: func(_ context.Context, _ Connector, request FetchRequest) (FetchPage, error) {
		return FetchPage{NextCursor: request.Cursor + "x"}, nil
	}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	assertBillingError(t, err, ErrorUpstream, "billing_page_limit_exceeded")
	if run.Status != SyncFailed || run.PagesFetched != 10000 || store.finished.ErrorCode != "billing_page_limit_exceeded" {
		t.Fatalf("page limit was not recorded: run=%+v finished=%+v", run, store.finished)
	}
}

func TestServiceTestProbesConnectorThroughAdapter(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	var request FetchRequest
	adapter := &fakeBillingAdapter{fetch: func(_ context.Context, _ Connector, got FetchRequest) (FetchPage, error) {
		request = got
		return FetchPage{Records: []Record{{ExternalID: "sample"}}}, nil
	}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	result, err := service.Test(context.Background(), "conn-1")
	if err != nil {
		t.Fatalf("connector probe failed: %v", err)
	}
	if result["ok"] != true || result["sample_records"] != 1 || request.PageSize != 1 {
		t.Fatalf("unexpected probe result=%#v request=%+v", result, request)
	}
}

func TestServiceRejectsDisabledAndUnknownConnectors(t *testing.T) {
	tests := []struct {
		name      string
		connector Connector
		adapters  map[string]Adapter
		wantKind  ErrorKind
		wantCode  string
	}{
		{
			name:      "disabled",
			connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: "disabled"},
			adapters:  map[string]Adapter{ConnectorOneAPI: &fakeBillingAdapter{}},
			wantKind:  ErrorConflict,
			wantCode:  "billing_connector_disabled",
		},
		{
			name:      "unknown adapter",
			connector: Connector{ID: "conn-1", Type: "missing", Status: StatusActive},
			adapters:  map[string]Adapter{},
			wantKind:  ErrorInvalidInput,
			wantCode:  "billing_adapter_missing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&fakeBillingStore{connector: test.connector}, test.adapters)
			_, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
			assertBillingError(t, err, test.wantKind, test.wantCode)
		})
	}
}

func TestServiceRejectsInvalidRanges(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: &fakeBillingAdapter{}})
	now := time.Now().UTC()
	for _, requested := range []SyncRequest{
		{From: now, To: now.Add(-time.Minute)},
		{From: now.Add(-time.Hour), To: now.Add(10 * time.Minute)},
	} {
		_, err := service.Sync(context.Background(), "conn-1", requested, "manual")
		assertBillingError(t, err, ErrorInvalidInput, "invalid_billing_range")
	}
	if store.started.ID != "" {
		t.Fatalf("invalid range started a sync run: %+v", store.started)
	}
}

func TestServiceResumesFromCheckpoint(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	checkpoint, err := json.Marshal(syncCheckpoint{From: from, To: to, Cursor: "resume-2"})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive, Checkpoint: string(checkpoint)}}
	var request FetchRequest
	adapter := &fakeBillingAdapter{fetch: func(_ context.Context, _ Connector, got FetchRequest) (FetchPage, error) {
		request = got
		return FetchPage{}, nil
	}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{}, "manual")
	if err != nil || run.Status != SyncSucceeded {
		t.Fatalf("checkpoint sync failed: run=%+v err=%v", run, err)
	}
	if request.Cursor != "resume-2" || !request.From.Equal(from) || !request.To.Equal(to) || store.started.CursorStart != "resume-2" {
		t.Fatalf("checkpoint was not forwarded: request=%+v started=%+v", request, store.started)
	}
}

func TestServiceFailsOnStalledCursor(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	adapter := &fakeBillingAdapter{pages: []FetchPage{{NextCursor: "next"}, {NextCursor: "next"}}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	assertBillingError(t, err, ErrorUpstream, "billing_cursor_stalled")
	if run.Status != SyncFailed || run.ErrorCode != "billing_cursor_stalled" || run.PagesFetched != 2 {
		t.Fatalf("unexpected stalled run: %+v", run)
	}
}

func TestServiceMarksPersistenceFailure(t *testing.T) {
	store := &fakeBillingStore{
		connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive},
		saveErr:   errors.New("database unavailable"),
	}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: &fakeBillingAdapter{pages: []FetchPage{{}}}})

	run, err := service.Sync(context.Background(), "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	if err == nil || err.Error() != "database unavailable" {
		t.Fatalf("expected persistence error, got %v", err)
	}
	if run.Status != SyncFailed || run.ErrorCode != "internal_error" || store.finished.Status != SyncFailed {
		t.Fatalf("persistence failure was not recorded: run=%+v finished=%+v", run, store.finished)
	}
}

func TestServiceMapsCancellationToTimeout(t *testing.T) {
	store := &fakeBillingStore{connector: Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}}
	adapter := &fakeBillingAdapter{fetch: func(ctx context.Context, _ Connector, _ FetchRequest) (FetchPage, error) {
		return FetchPage{}, ctx.Err()
	}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: adapter})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	run, err := service.Sync(ctx, "conn-1", SyncRequest{From: time.Now().Add(-time.Hour), To: time.Now()}, "manual")
	assertBillingError(t, err, ErrorTimeout, "billing_sync_timeout")
	if run.Status != SyncFailed || run.ErrorCode != "billing_sync_timeout" {
		t.Fatalf("cancellation was not recorded: %+v", run)
	}
}

func TestServiceRunDueRecordsScheduledAudit(t *testing.T) {
	connector := Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}
	store := &fakeBillingStore{connector: connector, due: []Connector{connector}}
	service := NewService(store, map[string]Adapter{ConnectorOneAPI: &fakeBillingAdapter{pages: []FetchPage{{}}}})

	runs := service.RunDue(context.Background(), time.Now().UTC())
	if len(runs) != 1 || runs[0].Trigger != "scheduled" || runs[0].Status != SyncSucceeded {
		t.Fatalf("unexpected due runs: %+v", runs)
	}
	if len(store.audits) != 1 || store.audits[0].Trigger != "scheduled" || store.audits[0].Status != SyncSucceeded {
		t.Fatalf("scheduled audit was not recorded: %+v", store.audits)
	}
}

func TestServiceRunDuePreservesLookupErrorCode(t *testing.T) {
	connector := Connector{ID: "conn-1", Type: ConnectorOneAPI, Status: StatusActive}
	store := &fakeBillingStore{
		connector: connector,
		due:       []Connector{connector},
		getErr:    NewError(ErrorNotFound, "billing_connector_not_found", "Billing connector not found"),
	}
	service := NewService(store, nil)

	runs := service.RunDue(context.Background(), time.Now().UTC())
	if len(runs) != 1 || runs[0].ErrorCode != "billing_connector_not_found" || store.audits[0].ErrorCode != "billing_connector_not_found" {
		t.Fatalf("lookup error code was not preserved: runs=%+v audits=%+v", runs, store.audits)
	}
}

func TestServiceSchedulerCanRestartAfterShutdown(t *testing.T) {
	service := NewService(&fakeBillingStore{}, nil)
	service.StartScheduler(time.Hour)
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	service.StartScheduler(time.Hour)
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

func assertBillingError(t *testing.T, err error, wantKind ErrorKind, wantCode string) {
	t.Helper()
	kind, code, _, ok := ErrorInfo(err)
	if !ok || kind != wantKind || code != wantCode {
		t.Fatalf("error=%v kind=%q code=%q ok=%v, want kind=%q code=%q", err, kind, code, ok, wantKind, wantCode)
	}
}
