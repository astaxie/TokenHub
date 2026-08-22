package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Service struct {
	store         Store
	adapters      map[string]Adapter
	mu            sync.Mutex
	active        map[string]bool
	schedulerMu   sync.Mutex
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

type syncCheckpoint struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Cursor string    `json:"cursor"`
}

// NewService creates a billing synchronizer from a persistence port and the
// concrete upstream adapters supplied by the composition root.
func NewService(store Store, adapters map[string]Adapter) *Service {
	if adapters == nil {
		adapters = map[string]Adapter{}
	}
	return &Service{store: store, adapters: adapters, active: map[string]bool{}}
}

func (s *Service) RunDue(ctx context.Context, now time.Time) []SyncRun {
	connectors := s.store.ListDueBillingConnectors(now, 25)
	runs := make([]SyncRun, 0, len(connectors))
	for _, connector := range connectors {
		run, err := s.Sync(ctx, connector.ID, SyncRequest{}, "scheduled")
		if run.ID == "" && err != nil {
			_, code, message := errorDetails(err)
			run = SyncRun{ConnectorID: connector.ID, Trigger: "scheduled", Status: SyncFailed, ErrorCode: code, ErrorMessage: message}
		}
		s.store.RecordScheduledBillingAudit(run)
		runs = append(runs, run)
		if ctx.Err() != nil {
			break
		}
	}
	return runs
}

func (s *Service) StartScheduler(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	s.schedulerMu.Lock()
	defer s.schedulerMu.Unlock()
	if s.schedulerStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.schedulerStop = cancel
	s.schedulerDone = make(chan struct{})
	go func() {
		defer close(s.schedulerDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.RunDue(ctx, now.UTC())
			}
		}
	}()
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.schedulerMu.Lock()
	stop := s.schedulerStop
	done := s.schedulerDone
	s.schedulerMu.Unlock()
	if stop == nil {
		return nil
	}
	stop()
	select {
	case <-done:
		s.schedulerMu.Lock()
		s.schedulerStop = nil
		s.schedulerDone = nil
		s.schedulerMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Test(ctx context.Context, connectorID string) (map[string]any, error) {
	connector, err := s.store.GetBillingConnector(connectorID, true)
	if err != nil {
		return nil, err
	}
	adapter, ok := s.adapters[connector.Type]
	if !ok {
		return nil, NewError(ErrorInvalidInput, "billing_adapter_missing", "Billing connector adapter is not registered")
	}
	now := time.Now().UTC()
	page, _, err := fetchPageWithRetry(ctx, configInt(connector.Config, "max_retries", 3, 1, 8), time.Duration(configInt(connector.Config, "retry_base_ms", 250, 1, 5000))*time.Millisecond, func() (FetchPage, error) {
		return adapter.Fetch(ctx, connector, FetchRequest{From: now.Add(-time.Hour), To: now, PageSize: 1})
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return map[string]any{"ok": true, "connector_id": connector.ID, "connector_type": connector.Type, "sample_records": len(page.Records)}, nil
}

func (s *Service) Sync(ctx context.Context, connectorID string, requested SyncRequest, trigger string) (SyncRun, error) {
	if !s.begin(connectorID) {
		return SyncRun{}, NewError(ErrorConflict, "billing_sync_in_progress", "A billing sync is already running for this connector")
	}
	defer s.end(connectorID)
	connector, err := s.store.GetBillingConnector(connectorID, true)
	if err != nil {
		return SyncRun{}, err
	}
	if connector.Status != StatusActive {
		return SyncRun{}, NewError(ErrorConflict, "billing_connector_disabled", "Disabled billing connectors cannot be synchronized")
	}
	adapter, ok := s.adapters[connector.Type]
	if !ok {
		return SyncRun{}, NewError(ErrorInvalidInput, "billing_adapter_missing", "Billing connector adapter is not registered")
	}
	from, to, cursor, err := resolveSyncRange(connector, requested, time.Now().UTC())
	if err != nil {
		return SyncRun{}, err
	}
	run, err := s.store.StartBillingSyncRun(SyncRun{ConnectorID: connector.ID, Trigger: defaultString(trigger, "manual"), Status: SyncRunning, RangeStart: from, RangeEnd: to, CursorStart: cursor})
	if err != nil {
		return SyncRun{}, err
	}
	pageSize := configInt(connector.Config, "page_size", 100, 1, 1000)
	maxRetries := configInt(connector.Config, "max_retries", 3, 1, 8)
	retryBase := time.Duration(configInt(connector.Config, "retry_base_ms", 250, 1, 5000)) * time.Millisecond
	requestsPerSecond := configInt(connector.Config, "rate_limit_per_second", 0, 0, 1000)
	var lastRequest time.Time
	for pageIndex := 0; pageIndex < 10000; pageIndex++ {
		page, attempts, fetchErr := fetchPageWithRetry(ctx, maxRetries, retryBase, func() (FetchPage, error) {
			if requestsPerSecond > 0 && !lastRequest.IsZero() {
				minimumGap := time.Second / time.Duration(requestsPerSecond)
				if wait := minimumGap - time.Since(lastRequest); wait > 0 {
					timer := time.NewTimer(wait)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return FetchPage{}, ctx.Err()
					case <-timer.C:
					}
				}
			}
			lastRequest = time.Now()
			return adapter.Fetch(ctx, connector, FetchRequest{From: from, To: to, Cursor: cursor, PageSize: pageSize})
		})
		run.Attempts += attempts
		if fetchErr != nil {
			return s.failRun(run, fetchErr)
		}
		checkpoint, marshalErr := json.Marshal(syncCheckpoint{From: from, To: to, Cursor: page.NextCursor})
		if marshalErr != nil {
			return s.failRun(run, marshalErr)
		}
		inserted, updated, persistErr := s.store.SaveBillingPage(connector.ID, string(checkpoint), page.Records)
		if persistErr != nil {
			return s.failRun(run, persistErr)
		}
		run.PagesFetched++
		run.RecordsSeen += len(page.Records)
		run.RecordsInserted += inserted
		run.RecordsUpdated += updated
		run.CursorEnd = page.NextCursor
		if page.NextCursor == "" {
			run.Status = SyncSucceeded
			run.ErrorCode = ""
			run.ErrorMessage = ""
			return s.store.FinishBillingSyncRun(run)
		}
		if page.NextCursor == cursor {
			return s.failRun(run, NewError(ErrorUpstream, "billing_cursor_stalled", "Billing source returned a cursor that did not advance"))
		}
		cursor = page.NextCursor
	}
	return s.failRun(run, NewError(ErrorUpstream, "billing_page_limit_exceeded", "Billing sync exceeded the maximum page count"))
}

func (s *Service) failRun(run SyncRun, err error) (SyncRun, error) {
	run.Status = SyncFailed
	err = normalizeError(err)
	_, code, message := errorDetails(err)
	run.ErrorCode = code
	run.ErrorMessage = message
	finished, finishErr := s.store.FinishBillingSyncRun(run)
	if finishErr != nil {
		return run, finishErr
	}
	return finished, err
}

func (s *Service) begin(connectorID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[connectorID] {
		return false
	}
	s.active[connectorID] = true
	return true
}

func (s *Service) end(connectorID string) { s.mu.Lock(); delete(s.active, connectorID); s.mu.Unlock() }

func resolveSyncRange(connector Connector, requested SyncRequest, now time.Time) (time.Time, time.Time, string, error) {
	if requested.From.IsZero() && requested.To.IsZero() && strings.TrimSpace(connector.Checkpoint) != "" {
		var checkpoint syncCheckpoint
		if err := json.Unmarshal([]byte(connector.Checkpoint), &checkpoint); err == nil && !checkpoint.From.IsZero() && !checkpoint.To.IsZero() {
			return checkpoint.From.UTC(), checkpoint.To.UTC(), checkpoint.Cursor, nil
		}
	}
	to := requested.To.UTC()
	if requested.To.IsZero() {
		to = now.UTC()
	}
	from := requested.From.UTC()
	if requested.From.IsZero() {
		if connector.LastSyncedThrough != nil {
			from = connector.LastSyncedThrough.UTC()
		} else {
			from = to.Add(-24 * time.Hour)
		}
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, "", NewError(ErrorInvalidInput, "invalid_billing_range", "from must be earlier than to")
	}
	if to.After(now.Add(5 * time.Minute)) {
		return time.Time{}, time.Time{}, "", NewError(ErrorInvalidInput, "invalid_billing_range", "to cannot be in the future")
	}
	return from, to, "", nil
}

type retryInfo interface {
	Retryable() bool
	RetryAfter() time.Duration
}

func fetchPageWithRetry(ctx context.Context, maxAttempts int, baseDelay time.Duration, fetch func() (FetchPage, error)) (FetchPage, int, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		page, err := fetch()
		if err == nil {
			return page, attempt, nil
		}
		lastErr = err
		if attempt == maxAttempts || !retryable(err) {
			return FetchPage{}, attempt, err
		}
		delay := baseDelay * time.Duration(1<<(attempt-1))
		var upstream retryInfo
		if errors.As(err, &upstream) && upstream.RetryAfter() > delay {
			delay = upstream.RetryAfter()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return FetchPage{}, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return FetchPage{}, maxAttempts, lastErr
}

func retryable(err error) bool {
	var info retryInfo
	if errors.As(err, &info) {
		return info.Retryable()
	}
	if kind, _, _, ok := ErrorInfo(err); ok {
		return kind == ErrorUpstream
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func normalizeError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewError(ErrorTimeout, "billing_sync_timeout", "Billing sync timed out")
	}
	return err
}

func errorDetails(err error) (int, string, string) {
	if _, code, message, ok := ErrorInfo(err); ok {
		return 0, code, message
	}
	return 0, "internal_error", err.Error()
}
func configInt(config map[string]string, key string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(config[key]))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}
func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func (run SyncRun) String() string { return fmt.Sprintf("%s:%s", run.ConnectorID, run.Status) }
