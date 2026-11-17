//go:build integration

package server

import (
	"context"
	"database/sql"
	"os"
	"testing"
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

type overviewMigrationProbe struct{ *sql.DB }

func (p overviewMigrationProbe) QueryRowContext(ctx context.Context, query string, args ...any) dbschema.RowScanner {
	return p.DB.QueryRowContext(ctx, query, args...)
}
