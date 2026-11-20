package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tokenhub/backend/internal/dbschema"
)

func TestOverviewProjectionTracksAllDatabaseWriters(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	record := UsageRecord{ID: "overview_usage", ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", InputTokens: 5, TotalTokens: 5, CostUSD: .5, CreatedAt: time.Now().UTC()}
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	query := UsageSummaryQuery{UsageRecords: UsageSummaryScope{Global: true}, RequestLogs: UsageSummaryScope{Global: true}}
	assert := func(wantTokens int64, wantCount int64) {
		t.Helper()
		summary, progress, err := store.QueryOverviewSummary(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if !progress.Ready || summary.InputTokens != wantTokens || summary.UsageRecordCount != wantCount {
			t.Fatalf("summary=%+v progress=%+v", summary, progress)
		}
	}
	assert(5, 1)
	if err := store.db.Exec("UPDATE usage_records SET input_tokens = 9, attributed_user_id = 'other' WHERE id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	assert(9, 1)
	if err := store.db.Delete(&record).Error; err != nil {
		t.Fatal(err)
	}
	assert(0, 0)
}

func assertOverviewProjectionParity(t *testing.T, store *GormStore) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	records := []UsageRecord{
		usageSummaryRecord("proj_u1", "p1", "k1", "u1", 10, now),
		usageSummaryRecord("proj_u2", "p2", "k2", "u2", 20, now),
		usageSummaryRecord("proj_u3", "p1", "k2", "u2", 30, now),
	}
	logs := []RequestLog{
		usageSummaryLog("proj_r1", "p1", "k1", "u1", 200, now),
		usageSummaryLog("proj_r2", "p2", "k2", "u2", 502, now),
		usageSummaryLog("proj_r3", "admin_playground", "k1", "u1", 502, now),
		usageSummaryLog("proj_r4", "p1", "k2", "u2", 499, now),
	}
	logs[3].ErrorCode = "request_cancelled"
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	for _, scope := range []UsageSummaryScope{
		{Global: true}, {}, {ProjectIDs: []string{"p1"}}, {APIKeyIDs: []string{"k2"}},
		{AttributedUserIDs: []string{"u1"}, ProjectIDs: []string{"p1"}, APIKeyIDs: []string{"k1"}},
	} {
		query := UsageSummaryQuery{UsageRecords: scope, RequestLogs: scope}
		expected, err := store.QueryUsageSummary(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		got, progress, err := store.QueryOverviewSummary(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		if !progress.Ready {
			t.Fatal("fresh inserts require backfill")
		}
		assertUsageSummaryEqual(t, usageSummaryPayload(got), usageSummaryPayload(expected))
	}
	if err := store.db.Model(&logs[0]).Updates(map[string]any{"status_code": 503, "project_id": "p2"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Delete(&logs[1]).Error; err != nil {
		t.Fatal(err)
	}
	global := UsageSummaryScope{Global: true}
	query := UsageSummaryQuery{UsageRecords: global, RequestLogs: global}
	got, _, err := store.QueryOverviewSummary(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := store.QueryUsageSummary(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	assertUsageSummaryEqual(t, usageSummaryPayload(got), usageSummaryPayload(expected))
}

func TestOverviewProjectionMatchesLegacyScopes(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	assertOverviewProjectionParity(t, store)
}

func TestOverviewBackfillResumesWithoutDoubleCounting(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	restore := suspendOverviewInserts(t, store, "usage_records")
	records := []UsageRecord{{ID: "old_a", InputTokens: 5}, {ID: "old_b", InputTokens: 7}, {ID: "old_c", InputTokens: 11}}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	restore()
	progress, err := store.overviewProgress(context.Background())
	if err != nil || progress.Ready {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	if _, err := store.backfillOverviewBatch(context.Background(), "usage_records", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&records[0]).Update("input_tokens", 13).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Delete(&records[1]).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{ID: "new_before_cursor", InputTokens: 17}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := store.backfillOverviewBatch(context.Background(), "usage_records", 1); err != nil {
			t.Fatal(err)
		}
	}
	global := UsageSummaryScope{Global: true}
	summary, progress, err := store.QueryOverviewSummary(context.Background(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
	if err != nil || !progress.Ready || summary.InputTokens != 41 || summary.UsageRecordCount != 3 {
		t.Fatalf("summary=%+v progress=%+v err=%v", summary, progress, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.backfillOverviewBatch(canceled, "usage_records", 100); err == nil {
		t.Fatal("canceled backfill succeeded")
	}
}

func TestOverviewBreakdownUsesProjectedDimensions(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	records := []UsageRecord{
		{ID: "group_a", ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", ModelName: "a", ProviderID: "pa", ProviderResourceID: "ra", InputTokens: 7},
		{ID: "group_b", ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", ModelName: "b", ProviderID: "pb", ProviderResourceID: "rb", InputTokens: 11},
		{ID: "group_c", ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", ModelName: "a", ProviderID: "pa", ProviderResourceID: "ra", InputTokens: 13},
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	check := func(want int64, count int64) {
		t.Helper()
		groups := map[string]UsageGroup{}
		err := store.StreamUsageGroups(t.Context(), UsageSummaryScope{Global: true}, []string{"models"}, func(_ string, group UsageGroup) { groups[group.ID] = group })
		if err != nil || groups["a"].InputTokens != want || groups["a"].UsageRecordCount != count {
			t.Fatalf("groups=%+v err=%v", groups, err)
		}
	}
	check(20, 2)
	if err := store.db.Delete(&records[2]).Error; err != nil {
		t.Fatal(err)
	}
	check(7, 1)
	if err := store.db.Model(&records[1]).Updates(map[string]any{"model_name": "a", "provider_id": "pa", "provider_resource_id": "ra"}).Error; err != nil {
		t.Fatal(err)
	}
	check(18, 2)
}

func TestOverviewShutdownKeepsHeartbeatUntilBackfillDrains(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	heartbeatStopped := false
	workerDone := make(chan struct{})
	server := &Server{overviewBackfillCancel: cancel, overviewBackfillDone: workerDone, stopHeartbeat: func() { heartbeatStopped = true }}
	server.overviewBackfillStart.Do(func() {})
	deadline, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Shutdown(deadline) }()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded || ctx.Err() != context.Canceled || heartbeatStopped {
			t.Fatalf("err=%v cancel=%v heartbeat_stopped=%v", err, ctx.Err(), heartbeatStopped)
		}
	case <-time.After(time.Second):
		t.Error("shutdown ignored its deadline")
	}
	close(workerDone)
}

func TestOverviewBackfillCoordinatesInstancesAndWriters(t *testing.T) {
	path := "sqlite://" + filepath.Join(t.TempDir(), "overview-shared.db")
	first, err := NewStoreWithDialect(path, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewStoreWithDialect(path, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	assertOverviewBackfillCoordinatesInstances(t, first, second)
}

func assertOverviewBackfillCoordinatesInstances(t *testing.T, first, second *GormStore) {
	t.Helper()
	restore := suspendOverviewInserts(t, first, "usage_records")
	records := make([]UsageRecord, 3000)
	for i := range records {
		records[i] = UsageRecord{ID: fmt.Sprintf("old_%04d", i), ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", InputTokens: 1}
	}
	if err := first.db.CreateInBatches(records, 100).Error; err != nil {
		t.Fatal(err)
	}
	restore()

	one, err := first.overviewBackfillExecutor(dbschema.WithBackfillOwner("one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.overviewBackfillExecutor(dbschema.WithBackfillOwner("two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := one.RunOnlineBatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 3)
	go func() { _, err := one.RunOnlineBatch(t.Context()); errors <- err }()
	go func() { _, err := two.RunOnlineBatch(t.Context()); errors <- err }()
	go func() {
		for i := 0; i < 10; i++ {
			if err := second.db.Create(&UsageRecord{ID: fmt.Sprintf("new_%d", i), InputTokens: 3, AttributedUserID: "other"}).Error; err != nil {
				errors <- err
				return
			}
			if err := second.db.Model(&UsageRecord{}).Where("id = ?", fmt.Sprintf("old_%04d", i)).Updates(map[string]any{"input_tokens": 5, "attributed_user_id": "other"}).Error; err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
	}()
	for i := 0; i < 3; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if err := one.ReleaseOnlineLeases(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := two.RunOnlineBatch(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	states, err := two.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range states {
		if state.State != dbschema.BackfillStateComplete {
			t.Fatalf("incomplete takeover=%+v", state)
		}
	}
	for _, scope := range []UsageSummaryScope{{Global: true}, {AttributedUserIDs: []string{"other"}, ProjectIDs: []string{"p"}}} {
		query := UsageSummaryQuery{UsageRecords: scope, RequestLogs: scope}
		expected, err := first.QueryUsageSummary(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		actual, progress, err := second.QueryOverviewSummary(t.Context(), query)
		if err != nil || !progress.Ready {
			t.Fatalf("progress=%+v err=%v", progress, err)
		}
		assertUsageSummaryEqual(t, usageSummaryPayload(actual), usageSummaryPayload(expected))
	}
}

func TestSQLiteOverviewOverflowRemainsExactAfterDeletion(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Exec("INSERT INTO usage_records (id, input_tokens) VALUES ('huge_a', 5000000000000000000), ('huge_b', 5000000000000000001)").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DELETE FROM usage_records WHERE id = 'huge_a'").Error; err != nil {
		t.Fatal(err)
	}
	global := UsageSummaryScope{Global: true}
	summary, _, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
	if err != nil || summary.InputTokens != 5000000000000000001 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestSQLiteOverviewOrdinaryCostsKeepProjection(t *testing.T) {
	for _, mode := range []string{"live", "backfill"} {
		t.Run(mode, func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			var restore func()
			if mode == "backfill" {
				restore = suspendOverviewInserts(t, store, "usage_records")
			}
			if err := store.db.Exec("INSERT INTO usage_records (id, cost_usd) VALUES ('a_small', 0.01), ('b_large', 10)").Error; err != nil {
				t.Fatal(err)
			}
			if mode == "backfill" {
				restore()
				for i := 0; i < 3; i++ {
					if _, err := store.backfillOverviewBatch(t.Context(), "usage_records", 1); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := store.db.Exec("DELETE FROM usage_records WHERE id = 'b_large'").Error; err != nil {
				t.Fatal(err)
			}
			global := UsageSummaryScope{Global: true}
			summary, progress, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
			if err != nil || !progress.Ready || progress.useRaw || summary.EstimatedCostUSD != 0.01 {
				t.Fatalf("summary=%+v progress=%+v err=%v", summary, progress, err)
			}
		})
	}
}

func TestSQLiteOverviewCostCorrectionKeepsSmallBalance(t *testing.T) {
	for _, small := range []float64{0.01, 3.01} {
		for _, order := range []string{"large_first", "small_first"} {
			for _, batchSize := range []int{0, 1, 100} {
				t.Run(fmt.Sprintf("%s/small_%g/batch_%d", order, small, batchSize), func(t *testing.T) {
					store := NewMemoryStore()
					t.Cleanup(func() { _ = store.Close() })
					var restore func()
					if batchSize > 0 {
						restore = suspendOverviewInserts(t, store, "usage_records")
					}
					values := fmt.Sprintf("('large', 10000000000000000), ('small', %.17g)", small)
					if order == "small_first" {
						values = fmt.Sprintf("('small', %.17g), ('large', 10000000000000000)", small)
					}
					if err := store.db.Exec("INSERT INTO usage_records (id,cost_usd) VALUES " + values).Error; err != nil {
						t.Fatal(err)
					}
					if batchSize > 0 {
						restore()
						for {
							remaining, err := store.backfillOverviewBatch(t.Context(), "usage_records", batchSize)
							if err != nil {
								t.Fatal(err)
							}
							if remaining == 0 {
								break
							}
						}
					}
					if err := store.db.Exec("DELETE FROM usage_records WHERE id = 'large'").Error; err != nil {
						t.Fatal(err)
					}
					global := UsageSummaryScope{Global: true}
					got, _, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
					if err != nil || got.EstimatedCostUSD != small {
						t.Fatalf("summary=%+v err=%v", got, err)
					}
				})
			}
		}
	}
}

func TestSQLiteOverviewAmountOverflowKeepsSourceRows(t *testing.T) {
	for _, batchSize := range []int{0, 1, 100} {
		t.Run(fmt.Sprintf("batch_%d", batchSize), func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			var restore func()
			if batchSize > 0 {
				restore = suspendOverviewInserts(t, store, "usage_records")
			}
			if err := store.db.Exec("INSERT INTO usage_records (id,cost_usd) VALUES ('huge_a', 1e308), ('huge_b', 1e308), ('huge_c', 1e308)").Error; err != nil {
				t.Fatalf("derived amount rejected source: %v", err)
			}
			if batchSize > 0 {
				restore()
				for {
					remaining, err := store.backfillOverviewBatch(t.Context(), "usage_records", batchSize)
					if err != nil {
						t.Fatal(err)
					}
					if remaining == 0 {
						break
					}
				}
			}
			response := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/overview", nil, "")
			if response.Code != 200 || !strings.Contains(response.Body, `"state":"error"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if err := store.db.Exec("DELETE FROM usage_records WHERE id IN ('huge_a','huge_b')").Error; err != nil {
				t.Fatal(err)
			}
			global := UsageSummaryScope{Global: true}
			summary, _, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
			if err != nil || summary.EstimatedCostUSD != 1e308 {
				t.Fatalf("summary=%+v err=%v", summary, err)
			}
		})
	}
}

// suspendOverviewInserts creates the same backlog and ledger state as an
// upgrade from a release without projection triggers.
func suspendOverviewInserts(t *testing.T, store *GormStore, source string) func() {
	t.Helper()
	for _, query := range []string{
		"UPDATE overview_projection_progress SET ready = 0 WHERE source = ?",
		"UPDATE data_backfills SET state = 'pending', remaining = -1 WHERE id = 'overview-v1-' || ?",
	} {
		if err := store.db.Exec(query, source).Error; err != nil {
			t.Fatal(err)
		}
	}
	name := "overview_" + source + "_insert"
	disable, enable := "ALTER TABLE "+source+" DISABLE TRIGGER "+name, "ALTER TABLE "+source+" ENABLE TRIGGER "+name
	if store.dbDriver == "sqlite" {
		disable = "DROP TRIGGER " + name
		if err := store.db.Raw("SELECT sql FROM sqlite_master WHERE type='trigger' AND name = ?", name).Scan(&enable).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Exec(disable).Error; err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := store.db.Exec(enable).Error; err != nil {
			t.Fatal(err)
		}
	}
}
