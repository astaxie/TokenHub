//go:build integration

package server

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func testPostgresAnalyticsLegacySequenceMigration(t *testing.T, adminStore *GormStore, config Config) {
	t.Helper()
	schema := fmt.Sprintf("tokenhub_e2e_analytics_upgrade_%d", time.Now().UnixNano())
	if err := adminStore.db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create analytics upgrade schema: %v", err)
	}
	defer func() {
		if err := adminStore.db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop analytics upgrade schema: %v", err)
		}
	}()
	parsedURL, err := url.Parse(config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	config.DatabaseURL = parsedURL.String()

	closeStore := func(store *GormStore) {
		t.Helper()
		for _, database := range []interface{ DB() (*sql.DB, error) }{store.db, store.analyticsDB} {
			sqlDB, databaseErr := database.DB()
			if databaseErr == nil {
				_ = sqlDB.Close()
			}
		}
	}
	legacyStore, err := NewStoreWithDialect(config.DatabaseURL, config)
	if err != nil {
		t.Fatalf("create legacy analytics schema: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	projectID := "project_legacy_analytics"
	logs := []RequestLog{
		{
			ID: "log_legacy_in_window", RequestID: "req_legacy_in_window", ProjectID: projectID,
			ModelName: "gpt-legacy", StatusCode: http.StatusOK, CreatedAt: now.Add(-time.Hour),
		},
		{
			ID: "log_legacy_after_to", RequestID: "req_legacy_after_to", ProjectID: projectID,
			ModelName: "gpt-legacy", StatusCode: http.StatusOK, CreatedAt: now.Add(time.Hour),
		},
	}
	if err := legacyStore.db.Create(&logs).Error; err != nil {
		closeStore(legacyStore)
		t.Fatal(err)
	}
	// Simulate the pre-upgrade allocator with commit order opposite event order.
	if err := legacyStore.db.Model(&RequestLog{}).
		Where("id = ?", logs[0].ID).Update("commit_sequence", 2).Error; err != nil {
		closeStore(legacyStore)
		t.Fatal(err)
	}
	if err := legacyStore.db.Model(&RequestLog{}).
		Where("id = ?", logs[1].ID).Update("commit_sequence", 1).Error; err != nil {
		closeStore(legacyStore)
		t.Fatal(err)
	}
	if err := legacyStore.db.Model(&AnalyticsSequence{}).
		Where("name = ?", requestLogSequenceName).Updates(map[string]any{
		"last_value": 2, "sequence_offset": 0, "history_migrated": false,
	}).Error; err != nil {
		closeStore(legacyStore)
		t.Fatal(err)
	}
	closeStore(legacyStore)

	upgradedStore, err := NewStoreWithDialect(config.DatabaseURL, config)
	if err != nil {
		t.Fatalf("upgrade legacy analytics schema: %v", err)
	}
	defer closeStore(upgradedStore)
	page, err := upgradedStore.QueryTokenCostPage(t.Context(), TokenCostQuery{
		From: now.Add(-2 * time.Hour), To: now, ProjectID: projectID,
		Granularity: "request", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].RequestID != logs[0].RequestID {
		t.Fatalf("legacy time window after PostgreSQL upgrade = %#v", page)
	}
	var migrated []RequestLog
	if err := upgradedStore.db.Where("id IN ?", []string{logs[0].ID, logs[1].ID}).
		Order("created_at ASC, id ASC").Find(&migrated).Error; err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 2 || migrated[0].CommitSequence <= 0 ||
		migrated[0].CommitSequence >= migrated[1].CommitSequence || page.Checkpoint != migrated[0].CommitSequence {
		t.Fatalf("legacy PostgreSQL sequences were not ordered by event time: rows=%#v checkpoint=%d", migrated, page.Checkpoint)
	}
	var marker AnalyticsSequence
	if err := upgradedStore.db.First(&marker, "name = ?", requestLogSequenceName).Error; err != nil {
		t.Fatal(err)
	}
	if !marker.HistoryMigrated || marker.SequenceOffset < 0 {
		t.Fatalf("legacy PostgreSQL migration metadata = %#v", marker)
	}
	if err := backfillRequestLogCommitSequence(upgradedStore.db, "postgres"); err != nil {
		t.Fatalf("repeat legacy PostgreSQL migration: %v", err)
	}
	var repeated []RequestLog
	if err := upgradedStore.db.Where("id IN ?", []string{logs[0].ID, logs[1].ID}).
		Order("created_at ASC, id ASC").Find(&repeated).Error; err != nil {
		t.Fatal(err)
	}
	if len(repeated) != 2 || repeated[0].CommitSequence != migrated[0].CommitSequence ||
		repeated[1].CommitSequence != migrated[1].CommitSequence {
		t.Fatalf("legacy PostgreSQL migration was not idempotent: first=%#v repeated=%#v", migrated, repeated)
	}

	// Hold one historical row so the migration's MVCC update remains in flight.
	// A new gateway insert must still commit while that update is waiting.
	if err := upgradedStore.db.Model(&AnalyticsSequence{}).
		Where("name = ?", requestLogSequenceName).Update("history_migrated", false).Error; err != nil {
		t.Fatal(err)
	}
	blockedRow := upgradedStore.db.Begin()
	if blockedRow.Error != nil {
		t.Fatal(blockedRow.Error)
	}
	if err := blockedRow.Model(&RequestLog{}).Where("id = ?", logs[0].ID).
		Update("model_name", "gpt-legacy-blocked").Error; err != nil {
		_ = blockedRow.Rollback().Error
		t.Fatal(err)
	}
	migrationDone := make(chan error, 1)
	go func() {
		migrationDone <- backfillRequestLogCommitSequence(upgradedStore.db, "postgres")
	}()
	locked := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		probe := upgradedStore.db.Begin()
		if probe.Error != nil {
			_ = blockedRow.Rollback().Error
			t.Fatal(probe.Error)
		}
		err := probe.Exec(`SELECT 1 FROM analytics_sequences
WHERE name = ? FOR UPDATE NOWAIT`, requestLogSequenceName).Error
		_ = probe.Rollback().Error
		if err != nil {
			locked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !locked {
		_ = blockedRow.Rollback().Error
		t.Fatal("PostgreSQL history migration did not reach its row update")
	}
	concurrentLog := RequestLog{
		ID: "log_during_legacy_migration", RequestID: "req_during_legacy_migration", ProjectID: projectID,
		ModelName: "gpt-legacy", StatusCode: http.StatusOK, CreatedAt: now,
	}
	insertDone := make(chan error, 1)
	go func() { insertDone <- upgradedStore.db.Create(&concurrentLog).Error }()
	select {
	case err := <-insertDone:
		if err != nil {
			_ = blockedRow.Rollback().Error
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = blockedRow.Rollback().Error
		t.Fatal("PostgreSQL history migration blocked a new request log insert")
	}
	if err := blockedRow.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-migrationDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PostgreSQL history migration did not resume")
	}

	// Simulate pg_restore into a cluster whose transaction-ID space is behind
	// the source watermark. Rebase must preserve that watermark and place new
	// request logs above it without rewriting frozen history.
	var currentTransactionID int64
	if err := upgradedStore.db.Raw("SELECT txid_current()::bigint").Scan(&currentTransactionID).Error; err != nil {
		t.Fatal(err)
	}
	savedWatermark := currentTransactionID + 10_000
	if err := upgradedStore.db.Model(&RequestLog{}).Where("id = ?", logs[1].ID).
		Update("commit_sequence", savedWatermark).Error; err != nil {
		t.Fatal(err)
	}
	if err := upgradedStore.db.Model(&AnalyticsSequence{}).
		Where("name = ?", requestLogSequenceName).Updates(map[string]any{
		"sequence_offset": 0, "history_migrated": true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillRequestLogCommitSequence(upgradedStore.db, "postgres"); err != nil {
		t.Fatalf("rebase restored PostgreSQL checkpoint: %v", err)
	}
	restoredLog := RequestLog{
		ID: "log_after_pg_restore", RequestID: "req_after_pg_restore", ProjectID: projectID,
		ModelName: "gpt-restored", StatusCode: http.StatusOK, CreatedAt: now.Add(2 * time.Hour),
	}
	if err := upgradedStore.db.Create(&restoredLog).Error; err != nil {
		t.Fatal(err)
	}
	if err := upgradedStore.db.First(&restoredLog, "id = ?", restoredLog.ID).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint, err := upgradedStore.TokenCostCheckpoint(t.Context(), TokenCostQuery{
		From: now.Add(-2 * time.Hour), To: now.Add(3 * time.Hour), ProjectID: projectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restoredLog.CommitSequence <= savedWatermark || checkpoint <= savedWatermark {
		t.Fatalf("restored PostgreSQL checkpoint did not rebase: saved=%d row=%d checkpoint=%d",
			savedWatermark, restoredLog.CommitSequence, checkpoint)
	}
}
