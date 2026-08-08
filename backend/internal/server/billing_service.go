package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type BillingService struct {
	store         BillingStore
	adapters      map[string]BillingAdapter
	mu            sync.Mutex
	active        map[string]bool
	schedulerMu   sync.Mutex
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
}

type billingCheckpoint struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Cursor string    `json:"cursor"`
}

func (s *BillingService) RunDue(ctx context.Context, now time.Time) []BillingSyncRun {
	connectors := s.store.ListDueBillingConnectors(now, 25)
	runs := make([]BillingSyncRun, 0, len(connectors))
	for _, connector := range connectors {
		run, err := s.Sync(ctx, connector.ID, BillingSyncRequest{}, "scheduled")
		if run.ID == "" && err != nil {
			run = BillingSyncRun{ConnectorID: connector.ID, Trigger: "scheduled", Status: BillingSyncFailed, ErrorCode: AsHTTPError(err).Code, ErrorMessage: AsHTTPError(err).Message}
		}
		s.store.RecordScheduledBillingAudit(run)
		runs = append(runs, run)
		if ctx.Err() != nil {
			break
		}
	}
	return runs
}

func (s *BillingService) StartScheduler(interval time.Duration) {
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

func (s *BillingService) Shutdown(ctx context.Context) error {
	s.schedulerMu.Lock()
	stop := s.schedulerStop
	s.schedulerMu.Unlock()
	if stop == nil {
		return nil
	}
	stop()
	select {
	case <-s.schedulerDone:
		s.schedulerMu.Lock()
		s.schedulerStop = nil
		s.schedulerDone = nil
		s.schedulerMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type billingUpstreamError struct {
	status     int
	code       string
	message    string
	retryable  bool
	retryAfter time.Duration
}

func (e *billingUpstreamError) Error() string { return e.message }

func newBillingService(store BillingStore) *BillingService {
	client := &http.Client{Timeout: 30 * time.Second}
	return &BillingService{
		store: store,
		adapters: map[string]BillingAdapter{
			BillingConnectorAliyun: AliyunBillingAdapter{Client: client},
			BillingConnectorNewAPI: NewAPIBillingAdapter{Client: client},
			BillingConnectorOneAPI: OneAPIBillingAdapter{Client: client},
		},
		active: map[string]bool{},
	}
}

func (s *BillingService) Test(ctx context.Context, connectorID string) (map[string]any, error) {
	connector, err := s.store.GetBillingConnector(connectorID, true)
	if err != nil {
		return nil, err
	}
	adapter, ok := s.adapters[connector.Type]
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "billing_adapter_missing", "Billing connector adapter is not registered")
	}
	now := time.Now().UTC()
	page, _, err := fetchBillingPageWithRetry(ctx, billingConfigInt(connector.Config, "max_retries", 3, 1, 8), time.Duration(billingConfigInt(connector.Config, "retry_base_ms", 250, 1, 5000))*time.Millisecond, func() (BillingFetchPage, error) {
		return adapter.Fetch(ctx, connector, BillingFetchRequest{From: now.Add(-time.Hour), To: now, PageSize: 1})
	})
	if err != nil {
		return nil, billingHTTPError(err)
	}
	return map[string]any{
		"ok":             true,
		"connector_id":   connector.ID,
		"connector_type": connector.Type,
		"sample_records": len(page.Records),
	}, nil
}

func (s *BillingService) Sync(ctx context.Context, connectorID string, requested BillingSyncRequest, trigger string) (BillingSyncRun, error) {
	if !s.begin(connectorID) {
		return BillingSyncRun{}, NewHTTPError(http.StatusConflict, "billing_sync_in_progress", "A billing sync is already running for this connector")
	}
	defer s.end(connectorID)

	connector, err := s.store.GetBillingConnector(connectorID, true)
	if err != nil {
		return BillingSyncRun{}, err
	}
	if connector.Status != StatusActive {
		return BillingSyncRun{}, NewHTTPError(http.StatusConflict, "billing_connector_disabled", "Disabled billing connectors cannot be synchronized")
	}
	adapter, ok := s.adapters[connector.Type]
	if !ok {
		return BillingSyncRun{}, NewHTTPError(http.StatusBadRequest, "billing_adapter_missing", "Billing connector adapter is not registered")
	}

	from, to, cursor, err := resolveBillingSyncRange(connector, requested, time.Now().UTC())
	if err != nil {
		return BillingSyncRun{}, err
	}
	run, err := s.store.StartBillingSyncRun(BillingSyncRun{
		ConnectorID: connector.ID,
		Trigger:     defaultString(trigger, "manual"),
		Status:      BillingSyncRunning,
		RangeStart:  from,
		RangeEnd:    to,
		CursorStart: cursor,
	})
	if err != nil {
		return BillingSyncRun{}, err
	}

	pageSize := billingConfigInt(connector.Config, "page_size", 100, 1, 1000)
	maxRetries := billingConfigInt(connector.Config, "max_retries", 3, 1, 8)
	retryBase := time.Duration(billingConfigInt(connector.Config, "retry_base_ms", 250, 1, 5000)) * time.Millisecond
	requestsPerSecond := billingConfigInt(connector.Config, "rate_limit_per_second", 0, 0, 1000)
	var lastRequest time.Time

	for pageIndex := 0; pageIndex < 10000; pageIndex++ {
		page, attempts, fetchErr := fetchBillingPageWithRetry(ctx, maxRetries, retryBase, func() (BillingFetchPage, error) {
			if requestsPerSecond > 0 && !lastRequest.IsZero() {
				minimumGap := time.Second / time.Duration(requestsPerSecond)
				if wait := minimumGap - time.Since(lastRequest); wait > 0 {
					timer := time.NewTimer(wait)
					defer timer.Stop()
					select {
					case <-ctx.Done():
						return BillingFetchPage{}, ctx.Err()
					case <-timer.C:
					}
				}
			}
			lastRequest = time.Now()
			return adapter.Fetch(ctx, connector, BillingFetchRequest{From: from, To: to, Cursor: cursor, PageSize: pageSize})
		})
		run.Attempts += attempts
		if fetchErr != nil {
			return s.failRun(run, fetchErr)
		}

		checkpoint, marshalErr := json.Marshal(billingCheckpoint{From: from, To: to, Cursor: page.NextCursor})
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
			run.Status = BillingSyncSucceeded
			run.ErrorCode = ""
			run.ErrorMessage = ""
			return s.store.FinishBillingSyncRun(run)
		}
		if page.NextCursor == cursor {
			return s.failRun(run, NewHTTPError(http.StatusBadGateway, "billing_cursor_stalled", "Billing source returned a cursor that did not advance"))
		}
		cursor = page.NextCursor
	}
	return s.failRun(run, NewHTTPError(http.StatusBadGateway, "billing_page_limit_exceeded", "Billing sync exceeded the maximum page count"))
}

func (s *BillingService) failRun(run BillingSyncRun, err error) (BillingSyncRun, error) {
	run.Status = BillingSyncFailed
	httpErr := billingHTTPError(err)
	run.ErrorCode = httpErr.Code
	run.ErrorMessage = httpErr.Message
	finished, finishErr := s.store.FinishBillingSyncRun(run)
	if finishErr != nil {
		return run, finishErr
	}
	return finished, httpErr
}

func (s *BillingService) begin(connectorID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[connectorID] {
		return false
	}
	s.active[connectorID] = true
	return true
}

func (s *BillingService) end(connectorID string) {
	s.mu.Lock()
	delete(s.active, connectorID)
	s.mu.Unlock()
}

func resolveBillingSyncRange(connector BillingConnector, requested BillingSyncRequest, now time.Time) (time.Time, time.Time, string, error) {
	if requested.From.IsZero() && requested.To.IsZero() && strings.TrimSpace(connector.Checkpoint) != "" {
		var checkpoint billingCheckpoint
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
		return time.Time{}, time.Time{}, "", NewHTTPError(http.StatusBadRequest, "invalid_billing_range", "from must be earlier than to")
	}
	if to.After(now.Add(5 * time.Minute)) {
		return time.Time{}, time.Time{}, "", NewHTTPError(http.StatusBadRequest, "invalid_billing_range", "to cannot be in the future")
	}
	return from, to, "", nil
}

func fetchBillingPageWithRetry(ctx context.Context, maxAttempts int, baseDelay time.Duration, fetch func() (BillingFetchPage, error)) (BillingFetchPage, int, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		page, err := fetch()
		if err == nil {
			return page, attempt, nil
		}
		lastErr = err
		if attempt == maxAttempts || !isRetryableBillingError(err) {
			return BillingFetchPage{}, attempt, err
		}
		delay := baseDelay * time.Duration(1<<(attempt-1))
		var upstream *billingUpstreamError
		if errors.As(err, &upstream) && upstream.retryAfter > delay {
			delay = upstream.retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return BillingFetchPage{}, attempt, ctx.Err()
		case <-timer.C:
		}
	}
	return BillingFetchPage{}, maxAttempts, lastErr
}

func isRetryableBillingError(err error) bool {
	var upstream *billingUpstreamError
	if errors.As(err, &upstream) {
		return upstream.retryable
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func billingHTTPError(err error) *HTTPError {
	var upstream *billingUpstreamError
	if errors.As(err, &upstream) {
		status := upstream.status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		return NewHTTPError(status, defaultString(upstream.code, "billing_upstream_error"), defaultString(upstream.message, "Billing source request failed"))
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return NewHTTPError(http.StatusGatewayTimeout, "billing_sync_timeout", "Billing sync timed out")
	}
	return AsHTTPError(err)
}

func billingConfigInt(config map[string]string, key string, fallback int, minimum int, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(config[key]))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}

func (run BillingSyncRun) String() string {
	return fmt.Sprintf("%s:%s", run.ConnectorID, run.Status)
}
