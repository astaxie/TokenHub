//go:build integration

package server

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
	"tokenhub/backend/internal/dbschema"
)

func TestPostgresOverviewProjection(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	assertOverviewProjectionParity(t, store)
	t.Run("alert history project filters", func(t *testing.T) { assertAlertHistoryProjectFilters(t, store) })
}

func TestPostgresOverviewRepresentativeScale(t *testing.T) {
	if os.Getenv("TOKENHUB_OVERVIEW_SCALE") != "1" {
		t.Skip("set TOKENHUB_OVERVIEW_SCALE=1")
	}
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_scale_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runOverviewScale(t, store)
}

func TestPostgresOverviewAmountCorrectionsPreservePrecision(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_precision_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Exec("INSERT INTO usage_records (id, cost_usd) VALUES ('large', 10000000000000000), ('small', 1)").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DELETE FROM usage_records WHERE id = 'large'").Error; err != nil {
		t.Fatal(err)
	}
	global := UsageSummaryScope{Global: true}
	got, _, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
	if err != nil || got.EstimatedCostUSD != 1 {
		t.Fatalf("summary=%+v err=%v", got, err)
	}
}

func TestPostgresOverviewOverflowPreservesSourceRecords(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_overflow_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Exec("INSERT INTO usage_records (id, input_tokens) VALUES ('huge_a', 5000000000000000000), ('huge_b', 5000000000000000000)").Error; err != nil {
		t.Fatalf("derived totals rejected valid source rows: %v", err)
	}
	var count int64
	if err := store.db.Model(&UsageRecord{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("source_count=%d err=%v", count, err)
	}
	global := UsageSummaryScope{Global: true}
	if _, _, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global}); err == nil {
		t.Fatal("out-of-range API summary was silently truncated")
	}
}

func TestPostgresOverviewBackfillConcurrency(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_concurrency_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	assertOverviewBackfillCoordinatesInstances(t, first, second)
}

func TestPostgresOverviewIndexPostconditions(t *testing.T) {
	t.Run("deployment search path", testOverviewIndexSearchPath)
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_indexes_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	probe := overviewMigrationProbe{sqlDB}
	index := overviewSourceIndexes[2]
	if err := verifyOverviewSourceIndex(t.Context(), probe, index); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("DROP INDEX idx_overview_usage_pending").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("CREATE INDEX idx_overview_usage_pending ON usage_records (created_at)").Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyOverviewSourceIndex(t.Context(), probe, index); err == nil {
		t.Fatal("wrong key and missing partial predicate accepted")
	}
	for _, statement := range overviewProjectionIndexMigrations()[2].Statements {
		if err := store.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyOverviewSourceIndex(t.Context(), probe, index); err != nil {
		t.Fatal(err)
	}
}

func testOverviewIndexSearchPath(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	neighbor := createPostgresSchema(t, admin, "neighbor_")
	if _, err := admin.Exec("CREATE TABLE " + neighbor + ".other (id text)"); err != nil {
		t.Fatal(err)
	}
	index := overviewSourceIndexes[0]
	if _, err := admin.Exec("CREATE INDEX " + index.name + " ON " + neighbor + ".other(id)"); err != nil {
		t.Fatal(err)
	}
	for _, localConflict := range []bool{false, true} {
		schema := createPostgresSchema(t, admin, "tenant_")
		path := schema + "," + neighbor
		dsn, err := withSearchPath(pgURL, path)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("CREATE TABLE alert_events (id text, created_at timestamptz)"); err != nil {
			t.Fatal(err)
		}
		if localConflict {
			if _, err := db.Exec("CREATE TABLE other(id text)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE INDEX " + index.name + " ON other(id)"); err != nil {
				t.Fatal(err)
			}
		}
		runner, err := dbschema.NewRunner(db, dbschema.DialectPostgres, overviewProjectionIndexMigrations()[:1], dbschema.WithExternalCoordination())
		if err != nil {
			t.Fatal(err)
		}
		_, err = runner.Adopt(t.Context(), nil)
		if (err != nil) != localConflict {
			t.Fatalf("local conflict=%t: %v", localConflict, err)
		}
		var restored string
		var intact bool
		if err := db.QueryRow("SELECT current_setting('search_path'), to_regclass($1) IS NOT NULL", neighbor+"."+index.name).Scan(&restored, &intact); err != nil || restored != path || !intact {
			t.Fatalf("path=%q neighbor index intact=%t err=%v", restored, intact, err)
		}
		if localConflict {
			var table string
			if err := db.QueryRow("SELECT indrelid::regclass::text FROM pg_index WHERE indexrelid=to_regclass($1)", schema+"."+index.name).Scan(&table); err != nil || table != "other" {
				t.Fatalf("local index table=%s err=%v", table, err)
			}
		}
	}
}

type overviewMigrationProbe struct{ *sql.DB }

func (p overviewMigrationProbe) QueryRowContext(ctx context.Context, query string, args ...any) dbschema.RowScanner {
	return p.DB.QueryRowContext(ctx, query, args...)
}

func TestPostgresOverviewTransactionShards(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_overview_shards_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := store.db.Begin()
	if first.Error != nil {
		t.Fatal(first.Error)
	}
	defer func() { _ = first.Rollback().Error }()
	if err := first.Create(&UsageRecord{ID: "shard_a", ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", ModelName: "m", InputTokens: 5, CostUSD: 5}).Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- store.db.Create(&UsageRecord{ID: "shard_b", ProjectID: "p", APIKeyID: "k", AttributedUserID: "u", ModelName: "m", InputTokens: 7, CostUSD: 3}).Error
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("independent transaction blocked on projection rows")
	}
	if err := first.Commit().Error; err != nil {
		t.Fatal(err)
	}
	// The next transaction reuses the first free shard. Its negative offset
	// must survive even when that shard's own net record count becomes zero.
	if err := store.db.Delete(&UsageRecord{}, "id = ?", "shard_b").Error; err != nil {
		t.Fatal(err)
	}
	global := UsageSummaryScope{Global: true}
	got, _, err := store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
	if err != nil || got.UsageRecordCount != 1 || got.InputTokens != 5 || got.EstimatedCostUSD != 5 {
		t.Fatalf("summary=%+v err=%v", got, err)
	}
	var groups []UsageGroup
	err = store.StreamUsageGroups(t.Context(), UsageSummaryScope{AttributedUserIDs: []string{"u"}, ProjectIDs: []string{"p"}, APIKeyIDs: []string{"k"}}, []string{"models"}, func(_ string, g UsageGroup) { groups = append(groups, g) })
	if err != nil || len(groups) != 1 || groups[0].UsageRecordCount != 1 || groups[0].EstimatedCostUSD != 5 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	if err := store.db.Delete(&UsageRecord{}, "id = ?", "shard_a").Error; err != nil {
		t.Fatal(err)
	}
	groups = nil
	if err := store.StreamUsageGroups(t.Context(), global, []string{"models"}, func(_ string, g UsageGroup) { groups = append(groups, g) }); err != nil || len(groups) != 0 {
		t.Fatalf("deleted groups=%+v err=%v", groups, err)
	}
	rolled := store.db.Begin()
	if rolled.Error != nil {
		t.Fatal(rolled.Error)
	}
	if err := rolled.Create(&UsageRecord{ID: "rolled", CostUSD: 9}).Error; err != nil {
		_ = rolled.Rollback().Error
		t.Fatal(err)
	}
	if err := rolled.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	saved := store.db.Begin()
	if saved.Error != nil {
		t.Fatal(saved.Error)
	}
	defer func() { _ = saved.Rollback().Error }()
	if err := saved.Create(&UsageRecord{ID: "saved", CostUSD: 4}).Error; err != nil {
		t.Fatal(err)
	}
	if err := saved.SavePoint("overview_before_change").Error; err != nil {
		t.Fatal(err)
	}
	if err := saved.Model(&UsageRecord{}).Where("id = ?", "saved").Update("cost_usd", 7).Error; err != nil {
		t.Fatal(err)
	}
	if err := saved.RollbackTo("overview_before_change").Error; err != nil {
		t.Fatal(err)
	}
	if err := saved.Model(&UsageRecord{}).Where("id = ?", "saved").Update("cost_usd", 6).Error; err != nil {
		t.Fatal(err)
	}
	if err := saved.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{ID: "reused", CostUSD: 5}).Error; err != nil {
		t.Fatal(err)
	}
	got, _, err = store.QueryOverviewSummary(t.Context(), UsageSummaryQuery{UsageRecords: global, RequestLogs: global})
	if err != nil || got.UsageRecordCount != 2 || got.EstimatedCostUSD != 11 {
		t.Fatalf("rollback/savepoint/reuse summary=%+v err=%v", got, err)
	}

}
