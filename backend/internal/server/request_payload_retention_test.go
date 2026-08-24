package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestParseAuditRetentionDays(t *testing.T) {
	tests := []struct {
		name  string
		value any
		days  int
		ok    bool
	}{
		{name: "minimum", value: "1d", days: 1, ok: true},
		{name: "default", value: "180d", days: 180, ok: true},
		{name: "maximum", value: "3650d", days: 3650, ok: true},
		{name: "missing", value: nil},
		{name: "non string", value: 180},
		{name: "empty", value: ""},
		{name: "missing number", value: "d"},
		{name: "zero", value: "0d"},
		{name: "leading zero", value: "01d"},
		{name: "negative", value: "-1d"},
		{name: "plus sign", value: "+1d"},
		{name: "fractional", value: "1.5d"},
		{name: "uppercase unit", value: "1D"},
		{name: "wrong unit", value: "24h"},
		{name: "leading whitespace", value: " 1d"},
		{name: "trailing whitespace", value: "1d "},
		{name: "above maximum", value: "3651d"},
		{name: "integer overflow", value: "999999999999999999999999999999d"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			days, ok := parseAuditRetentionDays(test.value)
			if days != test.days || ok != test.ok {
				t.Fatalf("parseAuditRetentionDays(%v) = (%d, %t), want (%d, %t)", test.value, days, ok, test.days, test.ok)
			}
		})
	}
}

func TestConfiguredAuditRetentionDaysRequiresGatewaySetting(t *testing.T) {
	settings := []AdminResource{
		{ID: "cfg_other", Fields: map[string]any{"audit_retention": "30d"}},
		{ID: gatewaySettingsID, Status: StatusActive, Fields: map[string]any{"audit_retention": "90d"}},
	}
	if days, ok := configuredAuditRetentionDays(settings); !ok || days != 90 {
		t.Fatalf("configuredAuditRetentionDays() = (%d, %t), want (90, true)", days, ok)
	}
	if _, ok := configuredAuditRetentionDays(settings[:1]); ok {
		t.Fatal("non-gateway settings must not configure request payload retention")
	}
	settings[1].Status = StatusDisabled
	if _, ok := configuredAuditRetentionDays(settings); ok {
		t.Fatal("disabled gateway settings must not configure request payload retention")
	}
}

func TestRequestPayloadRetentionDeletesExpiredPayloadsInBatchesAndPreservesRequestLogs(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Status: StatusActive,
		Fields: map[string]any{"audit_retention": "1d"},
	})
	now := time.Date(2026, time.August, 16, 10, 30, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour)
	payloads := make([]RequestPayloadLog, 0, requestPayloadRetentionBatchSize+4)
	for index := 0; index < requestPayloadRetentionBatchSize+2; index++ {
		payloads = append(payloads, RequestPayloadLog{
			ID:        fmt.Sprintf("payload-expired-%04d", index),
			RequestID: fmt.Sprintf("request-expired-%04d", index),
			CreatedAt: cutoff.Add(-time.Duration(index+1) * time.Second),
		})
	}
	payloads = append(payloads,
		RequestPayloadLog{ID: "payload-boundary", RequestID: "request-boundary", CreatedAt: cutoff},
		RequestPayloadLog{ID: "payload-current", RequestID: "request-current", CreatedAt: now},
	)
	if err := store.db.CreateInBatches(payloads, 250).Error; err != nil {
		t.Fatal(err)
	}
	requestLog := RequestLog{ID: "request-log-retained", RequestID: payloads[0].RequestID, CreatedAt: payloads[0].CreatedAt}
	if err := store.db.Create(&requestLog).Error; err != nil {
		t.Fatal(err)
	}

	service := newRequestPayloadRetentionService(store)
	deleted, err := service.RunDue(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != requestPayloadRetentionBatchSize+2 {
		t.Fatalf("deleted %d payload logs, want %d", deleted, requestPayloadRetentionBatchSize+2)
	}
	var remaining []RequestPayloadLog
	if err := store.db.Order("created_at asc").Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if got, want := payloadLogIDs(remaining), []string{"payload-boundary", "payload-current"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining payload logs = %v, want %v", got, want)
	}
	var requestLogCount int64
	if err := store.db.Model(&RequestLog{}).Where("id = ?", requestLog.ID).Count(&requestLogCount).Error; err != nil {
		t.Fatal(err)
	}
	if requestLogCount != 1 {
		t.Fatalf("request payload cleanup removed request metadata: count=%d", requestLogCount)
	}

	lateExpired := RequestPayloadLog{ID: "payload-late-expired", RequestID: "request-late-expired", CreatedAt: cutoff.Add(-time.Hour)}
	if err := store.db.Create(&lateExpired).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err = service.RunDue(t.Context(), now.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("same-hour cluster task deleted %d rows, want 0", deleted)
	}
	var lateExpiredCount int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", lateExpired.ID).Count(&lateExpiredCount).Error; err != nil {
		t.Fatal(err)
	}
	if lateExpiredCount != 1 {
		t.Fatal("same-hour cleanup ran more than once")
	}

	deleted, err = service.RunDue(t.Context(), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("next-hour cleanup deleted %d rows, want boundary and late-expired rows", deleted)
	}
}

func payloadLogIDs(logs []RequestPayloadLog) []string {
	ids := make([]string, len(logs))
	for index, log := range logs {
		ids[index] = log.ID
	}
	return ids
}

func TestRequestPayloadRetentionInvalidConfigurationDoesNotDelete(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Status: StatusActive,
		Fields: map[string]any{"audit_retention": "0d"},
	})
	payload := RequestPayloadLog{
		ID:        "payload-invalid-retention",
		RequestID: "request-invalid-retention",
		CreatedAt: time.Now().UTC().Add(-365 * 24 * time.Hour),
	}
	if err := store.db.Create(&payload).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := newRequestPayloadRetentionService(store).RunDue(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("invalid configuration deleted %d payload logs", deleted)
	}
	var count int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", payload.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("invalid configuration changed payload logs: count=%d err=%v", count, err)
	}
}

func TestRequestPayloadRetentionDisabledConfigurationDoesNotDelete(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID:     gatewaySettingsID,
		Status: StatusDisabled,
		Fields: map[string]any{"audit_retention": "1d"},
	})
	payload := RequestPayloadLog{
		ID:        "payload-disabled-retention",
		RequestID: "request-disabled-retention",
		CreatedAt: time.Now().UTC().Add(-365 * 24 * time.Hour),
	}
	if err := store.db.Create(&payload).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := newRequestPayloadRetentionService(store).RunDue(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("disabled configuration deleted %d payload logs", deleted)
	}
	var count int64
	if err := store.db.Model(&RequestPayloadLog{}).Where("id = ?", payload.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("disabled configuration changed payload logs: count=%d err=%v", count, err)
	}
}

func TestRequestPayloadRetentionFailureRetriesSameHour(t *testing.T) {
	failure := errors.New("delete failed")
	store := &requestPayloadRetentionFakeStore{
		settings:     []AdminResource{{ID: gatewaySettingsID, Status: StatusActive, Fields: map[string]any{"audit_retention": "30d"}}},
		deleteErrors: []error{failure, nil},
	}
	service := newRequestPayloadRetentionService(store)
	now := time.Date(2026, time.August, 16, 10, 30, 0, 0, time.UTC)
	if _, err := service.RunDue(t.Context(), now); !errors.Is(err, failure) {
		t.Fatalf("first cleanup error = %v, want %v", err, failure)
	}
	deleted, err := service.RunDue(t.Context(), now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 || store.deleteCalls != 2 {
		t.Fatalf("retry result deleted=%d calls=%d, want deleted=3 calls=2", deleted, store.deleteCalls)
	}
	deleted, err = service.RunDue(t.Context(), now.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 || store.deleteCalls != 2 {
		t.Fatalf("completed hourly task reran: deleted=%d calls=%d", deleted, store.deleteCalls)
	}
}

func TestRequestPayloadRetentionIndexColumns(t *testing.T) {
	store := NewMemoryStore()
	var columns []requestPayloadRetentionIndexColumn
	if err := store.db.Raw("PRAGMA index_info('idx_request_payload_logs_created_at_id')").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if got, want := indexColumnNames(columns), []string{"created_at", "id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request payload retention index columns = %v, want %v", got, want)
	}
}

type requestPayloadRetentionIndexColumn struct {
	Sequence int    `gorm:"column:seqno"`
	Name     string `gorm:"column:name"`
}

func indexColumnNames(columns []requestPayloadRetentionIndexColumn) []string {
	names := make([]string, len(columns))
	for index, column := range columns {
		names[index] = column.Name
	}
	return names
}

type requestPayloadRetentionFakeStore struct {
	mu           sync.Mutex
	settings     []AdminResource
	revision     int64
	deleteCalls  int
	deleteErrors []error
}

func (s *requestPayloadRetentionFakeStore) ListResourcesContext(context.Context, string) ([]AdminResource, error) {
	return s.settings, nil
}

func (s *requestPayloadRetentionFakeStore) DeleteRequestPayloadLogsBefore(context.Context, time.Time, int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	errorIndex := s.deleteCalls
	s.deleteCalls++
	if errorIndex < len(s.deleteErrors) && s.deleteErrors[errorIndex] != nil {
		return 0, s.deleteErrors[errorIndex]
	}
	return 3, nil
}

func (s *requestPayloadRetentionFakeStore) RunClusterTask(ctx context.Context, _ string, revision int64, fn func(context.Context) error) error {
	s.mu.Lock()
	if s.revision >= revision {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := fn(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.revision = revision
	s.mu.Unlock()
	return nil
}
