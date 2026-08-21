//go:build integration

package server

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestQuotaBucketMigrationPostgresAddsAttributionColumnBeforeBackfill(t *testing.T) {
	pgURL := strings.TrimSpace(os.Getenv("TEST_POSTGRES_URL"))
	if pgURL == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping PostgreSQL integration test")
	}

	adminConfig := Config{DatabaseURL: pgURL, DBMaxOpenConns: 5, DBMaxIdleConns: 1}
	adminStore, err := NewStoreWithDialect(pgURL, adminConfig)
	if err != nil {
		t.Fatalf("open PostgreSQL admin store: %v", err)
	}
	t.Cleanup(func() { closeQuotaMigrationPostgresStore(t, adminStore) })

	schema := fmt.Sprintf("tokenhub_quota_migration_%d", time.Now().UnixNano())
	if err := adminStore.db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create migration test schema: %v", err)
	}
	t.Cleanup(func() {
		if err := adminStore.db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop migration test schema: %v", err)
		}
	})
	// Legacy adoption rejects unrelated databases before running AutoMigrate.
	// Include a known TokenHub table so this fixture exercises the supported
	// pre-ledger upgrade path instead of bypassing the database recognizer.
	if err := adminStore.db.Exec(fmt.Sprintf("CREATE TABLE %s.projects (id TEXT PRIMARY KEY)", schema)).Error; err != nil {
		t.Fatalf("create legacy TokenHub marker table: %v", err)
	}

	legacyTable := fmt.Sprintf(`CREATE TABLE %s.quota_buckets (
key_id TEXT NOT NULL,
scope TEXT NOT NULL,
bucket TEXT NOT NULL,
requests INTEGER NOT NULL DEFAULT 0,
prompt_tokens INTEGER NOT NULL DEFAULT 0,
completion_tokens INTEGER NOT NULL DEFAULT 0,
total_tokens INTEGER NOT NULL DEFAULT 0,
cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
PRIMARY KEY (key_id, scope, bucket)
)`, schema)
	if err := adminStore.db.Exec(legacyTable).Error; err != nil {
		t.Fatalf("create legacy quota bucket table: %v", err)
	}
	if err := adminStore.db.Exec(fmt.Sprintf(`INSERT INTO %s.quota_buckets
	(key_id, scope, bucket, requests, total_tokens) VALUES
	('key_legacy', 'day', '2026-08-20', 4, 12),
	('user:usr_legacy', 'day', '2026-08-20', 2, 8)`, schema)).Error; err != nil {
		t.Fatalf("seed legacy quota bucket rows: %v", err)
	}

	schemaURL, err := url.Parse(pgURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}
	query := schemaURL.Query()
	query.Set("search_path", schema)
	schemaURL.RawQuery = query.Encode()
	config := Config{DatabaseURL: schemaURL.String(), DBMaxOpenConns: 5, DBMaxIdleConns: 1}
	store, err := NewStoreWithDialect(config.DatabaseURL, config)
	if err != nil {
		t.Fatalf("upgrade legacy PostgreSQL quota bucket table: %v", err)
	}
	t.Cleanup(func() { closeQuotaMigrationPostgresStore(t, store) })

	var columnCount int64
	if err := store.db.Raw(`SELECT COUNT(*)
	FROM information_schema.columns
	WHERE table_schema = current_schema()
	  AND table_name = 'quota_buckets'
	  AND column_name = 'attributed_user_id'`).Scan(&columnCount).Error; err != nil {
		t.Fatalf("check attribution column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("attribution column count = %d, want 1", columnCount)
	}

	var rows []QuotaBucket
	if err := store.db.Order("key_id").Find(&rows).Error; err != nil {
		t.Fatalf("load migrated quota bucket rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("migrated quota bucket row count = %d, want 2", len(rows))
	}
	if rows[0].KeyID != "key_legacy" || rows[0].AttributedUserID != unattributedQuotaUserID || rows[0].TotalTokens != 12 {
		t.Fatalf("migrated legacy key row = %+v", rows[0])
	}
	if rows[1].KeyID != "user:usr_legacy" || rows[1].AttributedUserID != "usr_legacy" || rows[1].TotalTokens != 8 {
		t.Fatalf("migrated user row = %+v", rows[1])
	}

	var pkColumns []struct {
		Column string `gorm:"column:column_name"`
	}
	if err := store.db.Raw(`SELECT kcu.column_name
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
	  ON tc.constraint_name = kcu.constraint_name
	 AND tc.table_schema = kcu.table_schema
	WHERE tc.table_schema = current_schema()
	  AND tc.table_name = 'quota_buckets'
	  AND tc.constraint_type = 'PRIMARY KEY'
	ORDER BY kcu.ordinal_position`).Scan(&pkColumns).Error; err != nil {
		t.Fatalf("check migrated primary key: %v", err)
	}
	wantPK := []string{"key_id", "scope", "bucket", "attributed_user_id"}
	if len(pkColumns) != len(wantPK) {
		t.Fatalf("migrated primary key columns = %+v, want %+v", pkColumns, wantPK)
	}
	for index, want := range wantPK {
		if pkColumns[index].Column != want {
			t.Fatalf("migrated primary key columns = %+v, want %+v", pkColumns, wantPK)
		}
	}
	var beforeConstraintOID int64
	if err := store.db.Raw(`SELECT c.oid
	FROM pg_constraint c
	JOIN pg_class r ON r.oid = c.conrelid
	JOIN pg_namespace n ON n.oid = r.relnamespace
	WHERE n.nspname = current_schema()
	  AND r.relname = 'quota_buckets'
	  AND c.contype = 'p'`).Scan(&beforeConstraintOID).Error; err != nil {
		t.Fatalf("read migrated primary key identity: %v", err)
	}
	if beforeConstraintOID == 0 {
		t.Fatal("migrated quota bucket primary key identity is missing")
	}
	if err := ensureQuotaBucketAttributionSchema(store.db, "postgres"); err != nil {
		t.Fatalf("rerun PostgreSQL quota bucket migration: %v", err)
	}
	var afterConstraintOID int64
	if err := store.db.Raw(`SELECT c.oid
	FROM pg_constraint c
	JOIN pg_class r ON r.oid = c.conrelid
	JOIN pg_namespace n ON n.oid = r.relnamespace
	WHERE n.nspname = current_schema()
	  AND r.relname = 'quota_buckets'
	  AND c.contype = 'p'`).Scan(&afterConstraintOID).Error; err != nil {
		t.Fatalf("read rerun primary key identity: %v", err)
	}
	if afterConstraintOID != beforeConstraintOID {
		t.Fatalf("rerunning migration changed primary key identity from %d to %d", beforeConstraintOID, afterConstraintOID)
	}
	if err := store.db.Exec("INSERT INTO quota_buckets (key_id, scope, bucket, requests) VALUES (?, ?, ?, ?)", "key_rollback", "day", "2026-08-20", 1).Error; err != nil {
		t.Fatalf("simulate an older release writing without attribution: %v", err)
	}
	var rollbackRow QuotaBucket
	if err := store.db.First(&rollbackRow, "key_id = ?", "key_rollback").Error; err != nil {
		t.Fatalf("load older-release quota row: %v", err)
	}
	if rollbackRow.AttributedUserID != unattributedQuotaUserID {
		t.Fatalf("older-release write attribution = %q, want %q", rollbackRow.AttributedUserID, unattributedQuotaUserID)
	}
}

func closeQuotaMigrationPostgresStore(t *testing.T, store *GormStore) {
	t.Helper()
	for _, database := range []*gorm.DB{store.db, store.analyticsDB} {
		sqlDB, err := database.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
}
