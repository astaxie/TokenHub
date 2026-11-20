package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOverviewRepresentativeScale(t *testing.T) {
	if os.Getenv("TOKENHUB_OVERVIEW_SCALE") != "1" {
		t.Skip("set TOKENHUB_OVERVIEW_SCALE=1 to run the representative dataset")
	}
	store, err := NewStoreWithDialect("sqlite://"+filepath.Join(t.TempDir(), "overview-scale.db"), Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runOverviewScale(t, store)
}

func runOverviewScale(t *testing.T, store *GormStore) {
	t.Helper()
	const count = 3000000
	started := time.Now()
	for _, table := range []string{"usage_records", "request_logs"} {
		restore := suspendOverviewInserts(t, store, table)
		for offset := 0; offset < count; offset += 10000 {
			numbers := "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 10000) "
			id := "'scale_' || CAST(? + x AS TEXT)"
			statement := numbers + "INSERT INTO " + table + " (id, project_id, api_key_id, attributed_user_id, created_at"
			if table == "usage_records" {
				statement += ", input_tokens, total_tokens, cost_usd) SELECT " + id + ", 'p', 'k', 'u', '2026-01-01 00:00:00', 5, 5, 0.01 FROM n"
			} else {
				statement += ", status_code) SELECT " + id + ", 'p', 'k', 'u', '2026-01-01 00:00:00', 200 FROM n"
			}
			if err := store.db.Exec(statement, offset).Error; err != nil {
				t.Fatal(err)
			}
		}
		restore()
	}
	alerts := make([]AlertEvent, 100000)
	for i := range alerts {
		alerts[i] = AlertEvent{ID: fmt.Sprintf("scale_a_%06d", i), Message: strings.Repeat("x", 128), CreatedAt: time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)}
	}
	if err := store.db.CreateInBatches(alerts, 100).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if err := store.db.Create(&Model{ID: fmt.Sprintf("scale_m_%d", i), Name: fmt.Sprintf("scale-model-%d", i), Metadata: map[string]string{"description": strings.Repeat("x", 1000)}, Status: StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("driver=%s seed_duration=%s", store.dbDriver, time.Since(started))
	if store.dbDriver == "postgres" {
		for _, migration := range overviewProjectionIndexMigrations() {
			begin := time.Now()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			for _, statement := range migration.Statements {
				if err := store.db.WithContext(ctx).Exec(statement).Error; err != nil {
					cancel()
					t.Fatal(err)
				}
			}
			cancel()
			t.Logf("populated_source_index=%s duration=%s", migration.Name, time.Since(begin))
		}
	}
	started = time.Now()
	for _, table := range []string{"usage_records", "request_logs"} {
		for {
			remaining, err := store.backfillOverviewBatch(context.Background(), table, 5000)
			if err != nil {
				t.Fatal(err)
			}
			if remaining == 0 {
				break
			}
		}
	}
	t.Logf("backfill_duration=%s", time.Since(started))
	if store.dbDriver == "postgres" {
		for _, table := range []string{"usage_records", "request_logs", "overview_totals", "alert_events"} {
			if err := store.db.Exec("ANALYZE " + table).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	server := New(store)
	handler := server.Handler()
	measure := func(fullPage bool) time.Duration {
		begin := time.Now()
		request := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
		request.Header.Set("authorization", "Bearer dev_admin_token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("overview status=%d body=%s", response.Code, response.Body.String())
		}
		var payload struct {
			Alerts  []AlertEvent
			Summary map[string]float64
			Status  OverviewSummaryStatus `json:"summary_status"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Error(err)
		}
		if len(payload.Alerts) != 20 || payload.Summary["request_count"] != count || payload.Summary["usage_record_count"] != count || payload.Status.State != "ready" {
			t.Errorf("invalid bounded summary alerts=%d summary=%v status=%+v", len(payload.Alerts), payload.Summary, payload.Status)
		}
		if fullPage {
			var calls sync.WaitGroup
			for _, path := range []string{"/api/admin/usage/breakdown", "/api/admin/usage/timeseries"} {
				path := path
				calls.Add(1)
				go func() {
					defer calls.Done()
					request := httptest.NewRequest(http.MethodGet, path, nil)
					request.Header.Set("authorization", "Bearer dev_admin_token")
					result := httptest.NewRecorder()
					handler.ServeHTTP(result, request)
					if result.Code != http.StatusOK {
						t.Errorf("%s status=%d body=%s", path, result.Code, result.Body.String())
					}
				}()
			}
			calls.Wait()
		}
		return time.Since(begin)
	}
	for _, mode := range []string{"cold", "warm", "page"} {
		durations := make([]time.Duration, 100)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for batch := 0; batch < 10; batch++ {
			if mode != "warm" {
				server.overviewSummaries.mu.Lock()
				clear(server.overviewSummaries.entries)
				server.overviewSummaries.mu.Unlock()
			}
			var wait sync.WaitGroup
			for i := 0; i < 10; i++ {
				index := batch*10 + i
				wait.Add(1)
				go func() { defer wait.Done(); durations[index] = measure(mode == "page") }()
			}
			wait.Wait()
		}
		runtime.ReadMemStats(&after)
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		p95 := durations[94]
		t.Logf("driver=%s mode=%s concurrency=10 requests=100 p95=%s allocated_bytes_per_request=%d", store.dbDriver, mode, p95, (after.TotalAlloc-before.TotalAlloc)/100)
		if p95 >= time.Second {
			t.Errorf("%s p95=%s exceeds 1 second", mode, p95)
		}
	}
}
