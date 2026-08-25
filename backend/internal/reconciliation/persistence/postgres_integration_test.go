//go:build integration

package persistence

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"tokenhub/backend/internal/reconciliation"
)

// TestPostgresStoreBackfillUsesThePublicPort verifies the PostgreSQL adapter
// directly. The server compatibility projection is intentionally not involved
// so this contract survives its eventual removal.
func TestPostgresStoreBackfillUsesThePublicPort(t *testing.T) {
	pgURL := strings.TrimSpace(os.Getenv("TEST_POSTGRES_URL"))
	if pgURL == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping PostgreSQL persistence integration test")
	}
	db, err := gorm.Open(postgres.Open(pgURL), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open PostgreSQL database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&RuleRow{}, &RunRow{}, &ItemRow{}); err != nil {
		t.Fatalf("ensure reconciliation tables: %v", err)
	}

	storeA := NewStore(db, &sync.Mutex{}, nil)
	storeB := NewStore(db, &sync.Mutex{}, nil)
	ruleID := fmt.Sprintf("reconciliation_pg_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = db.Where("id = ?", ruleID).Delete(&RuleRow{}).Error })
	if err := db.Create(&RuleRow{ID: ruleID, Name: "legacy", ConnectorID: "connector", Status: reconciliation.StatusActive}).Error; err != nil {
		t.Fatalf("create legacy rule: %v", err)
	}
	if err := db.Model(&RuleRow{}).Where("id = ?", ruleID).Updates(map[string]any{"connector_type": nil, "provider_id": nil}).Error; err != nil {
		t.Fatalf("clear legacy snapshot: %v", err)
	}

	candidates := []reconciliation.Rule{
		{ID: ruleID, ConnectorType: "oneapi", ProviderID: "provider-a", Version: 2, RuleHash: "sha256:a"},
		{ID: ruleID, ConnectorType: "oneapi", ProviderID: "provider-b", Version: 2, RuleHash: "sha256:b"},
	}
	start := make(chan struct{})
	results := make(chan reconciliation.Rule, 2)
	errorsFound := make(chan error, 2)
	var workers sync.WaitGroup
	for i, store := range []*Store{storeA, storeB} {
		workers.Add(1)
		go func(store *Store, candidate reconciliation.Rule) {
			defer workers.Done()
			<-start
			value, backfillErr := store.BackfillRuleConnectorSnapshot(candidate)
			results <- value
			errorsFound <- backfillErr
		}(store, candidates[i])
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsFound)
	for backfillErr := range errorsFound {
		if backfillErr != nil {
			t.Fatalf("backfill failed: %v", backfillErr)
		}
	}
	persisted, err := storeA.GetRule(ruleID)
	if err != nil {
		t.Fatalf("read persisted winner: %v", err)
	}
	for result := range results {
		if result.ProviderID != persisted.ProviderID || result.RuleHash != persisted.RuleHash || result.Version != persisted.Version {
			t.Fatalf("caller did not observe persisted winner: result=%#v persisted=%#v", result, persisted)
		}
	}
}
