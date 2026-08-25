package persistence

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"tokenhub/backend/internal/reconciliation"
)

func newTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:reconciliation_persistence_test?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&RuleRow{}, &RunRow{}, &ItemRow{}); err != nil {
		t.Fatal(err)
	}
	return NewStore(db, &sync.Mutex{}, nil), db
}

func testRule(id string) reconciliation.Rule {
	return reconciliation.Rule{ID: id, Name: "test rule", ConnectorID: "connector-1", ConnectorType: "oneapi", ProviderID: "provider-1", Status: reconciliation.StatusActive, Granularity: reconciliation.GranularityDay, MatchDimensions: []string{"model"}, Currency: "USD", RuleHash: "sha256:test", Timezone: "UTC"}
}

func TestStorePreservesTablesAndGeneratesOpaqueRuleIDs(t *testing.T) {
	store, _ := newTestStore(t)
	rule, err := store.CreateRule(reconciliation.Rule{
		Name: "test rule", ConnectorID: "connector-1", Status: reconciliation.StatusActive,
		Granularity: reconciliation.GranularityDay, MatchDimensions: []string{"model", "currency"},
		Currency: "USD", RuleHash: "sha256:test", Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rule.ID, "recrule_") || len(rule.ID) != len("recrule_")+16 {
		t.Fatalf("unexpected rule ID format: %q", rule.ID)
	}
	if rule.ID == "recrule_"+time.Now().UTC().Format("20060102150405.000000000") {
		t.Fatalf("rule ID reverted to timestamp-only format: %q", rule.ID)
	}
	if (RuleRow{}).TableName() != "reconciliation_rules" || (RunRow{}).TableName() != "reconciliation_runs" || (ItemRow{}).TableName() != "reconciliation_items" {
		t.Fatal("reconciliation table names changed")
	}
	loaded, err := store.GetRule(rule.ID)
	if err != nil || loaded.RuleHash != "sha256:test" || loaded.Currency != "USD" {
		t.Fatalf("rule round trip changed: %#v %v", loaded, err)
	}
}

func TestStoreMasksResourceAccountInManagementQueries(t *testing.T) {
	store, db := newTestStore(t)
	run := RunRow{ID: "run-1", RuleID: "rule-1", Status: reconciliation.RunSucceeded, StartedAt: time.Now().UTC()}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ItemRow{ID: "item-1", RunID: run.ID, Status: reconciliation.ProviderOnly, ResourceAccount: "account-secret-77", BucketStart: time.Now().UTC(), BucketEnd: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	items, total := store.ListItems(run.ID, "", 10, 0)
	if total != 1 || len(items) != 1 || items[0].ResourceAccount != "account-secret-77" || items[0].ResourceAccountMasked != "ac****77" {
		t.Fatalf("unexpected masked item projection: total=%d items=%#v", total, items)
	}
	batch := store.ListItemBatch(run.ID, "", "", false, 10)
	if len(batch) != 1 || batch[0].ResourceAccountMasked != "ac****77" {
		t.Fatalf("unexpected masked batch projection: %#v", batch)
	}
}

func TestStoreMapsOnlyExpectedDatabaseErrors(t *testing.T) {
	store, db := newTestStore(t)
	if _, err := store.GetRule("missing-rule"); err == nil {
		t.Fatal("missing rule unexpectedly succeeded")
	} else if kind, code, _, ok := reconciliation.ErrorInfo(err); !ok || kind != reconciliation.ErrorNotFound || code != "reconciliation_rule_not_found" {
		t.Fatalf("missing rule error was not mapped: %v", err)
	}

	rule := testRule("duplicate-rule")
	if _, err := store.CreateRule(rule); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRule(rule); err == nil {
		t.Fatal("duplicate rule unexpectedly succeeded")
	} else if kind, code, _, ok := reconciliation.ErrorInfo(err); !ok || kind != reconciliation.ErrorConflict || code != "reconciliation_rule_conflict" {
		t.Fatalf("duplicate rule error was not mapped: %v", err)
	}
	for name, operation := range map[string]func() error{
		"get run": func() error {
			_, err := store.GetRun("missing-run")
			return err
		},
		"replace run": func() error {
			_, err := store.ReplaceRun(reconciliation.Run{ID: "missing-run"}, nil)
			return err
		},
		"lock run": func() error {
			_, err := store.GetRun("missing-run")
			return err
		},
	} {
		if err := operation(); err == nil {
			t.Fatalf("%s unexpectedly succeeded", name)
		} else if kind, _, _, ok := reconciliation.ErrorInfo(err); !ok || kind != reconciliation.ErrorNotFound {
			t.Fatalf("%s error was not mapped as not found: %v", name, err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func() error{
		"create rule": func() error {
			_, err := store.CreateRule(testRule("database-failure-create"))
			return err
		},
		"get rule": func() error {
			_, err := store.GetRule("database-failure-get")
			return err
		},
		"update rule": func() error {
			_, err := store.UpdateRule(testRule("database-failure-update"))
			return err
		},
		"backfill rule": func() error {
			_, err := store.BackfillRuleConnectorSnapshot(testRule("database-failure-backfill"))
			return err
		},
		"replace run": func() error {
			_, err := store.ReplaceRun(reconciliation.Run{ID: "database-failure-replace"}, nil)
			return err
		},
		"get run": func() error {
			_, err := store.GetRun("database-failure-run")
			return err
		},
		"lock run": func() error {
			_, err := store.SaveRunLock(reconciliation.Run{ID: "database-failure-lock"})
			return err
		},
	}
	for name, operation := range operations {
		if err := operation(); err == nil {
			t.Fatalf("closed database unexpectedly succeeded for %s", name)
		} else if _, _, _, ok := reconciliation.ErrorInfo(err); ok {
			t.Fatalf("database failure was incorrectly mapped to a domain error for %s: %v", name, err)
		}
	}
}

func TestStoreSaveRunRollsBackRunAndItemsOnDatabaseError(t *testing.T) {
	store, db := newTestStore(t)
	if err := db.Create(&ItemRow{ID: "existing-item", RunID: "old-run"}).Error; err != nil {
		t.Fatal(err)
	}
	run := reconciliation.Run{ID: "new-run", RuleID: "missing-rule", Status: reconciliation.RunSucceeded, StartedAt: time.Now().UTC()}
	items := []reconciliation.Item{{ID: "existing-item", RunID: run.ID}}
	if _, err := store.SaveRun(run, items); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	if _, err := store.GetRun(run.ID); err == nil {
		t.Fatal("run remained after failed transaction")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		// The adapter's public error is expected to preserve the not-found category.
		if kind, _, _, ok := reconciliation.ErrorInfo(err); !ok || kind != reconciliation.ErrorNotFound {
			t.Fatalf("unexpected missing run error: %v", err)
		}
	}
	var itemCount int64
	if err := db.Model(&ItemRow{}).Where("run_id = ?", run.ID).Count(&itemCount).Error; err != nil {
		t.Fatal(err)
	}
	if itemCount != 0 {
		t.Fatalf("items remained after failed transaction: %d", itemCount)
	}
}

func TestStoreReplaceRunRollsBackExistingDataOnDatabaseError(t *testing.T) {
	store, db := newTestStore(t)
	oldStartedAt := time.Now().UTC().Add(-time.Hour)
	if err := db.Create(&RunRow{ID: "replace-run", Status: reconciliation.RunSucceeded, StartedAt: oldStartedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ItemRow{ID: "replace-old-item", RunID: "replace-run"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ItemRow{ID: "replace-conflict-item", RunID: "other-run"}).Error; err != nil {
		t.Fatal(err)
	}
	replacement := reconciliation.Run{ID: "replace-run", Status: reconciliation.RunSucceeded, StartedAt: time.Now().UTC()}
	if _, err := store.ReplaceRun(replacement, []reconciliation.Item{{ID: "replace-conflict-item", RunID: replacement.ID}}); err == nil {
		t.Fatal("replace unexpectedly succeeded")
	}
	loaded, err := store.GetRun(replacement.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.StartedAt.Equal(oldStartedAt) {
		t.Fatalf("run changed after failed replacement: %#v", loaded)
	}
	var item ItemRow
	if err := db.First(&item, "id = ?", "replace-old-item").Error; err != nil {
		t.Fatal(err)
	}
	if item.RunID != replacement.ID {
		t.Fatalf("old item changed after failed replacement: %#v", item)
	}
}

func TestStoreBackfillsLegacyConnectorSnapshot(t *testing.T) {
	store, db := newTestStore(t)
	legacy := testRule("legacy-rule")
	legacy.ConnectorType = ""
	legacy.ProviderID = ""
	legacyRow := RuleRow(legacy)
	if err := db.Create(&legacyRow).Error; err != nil {
		t.Fatal(err)
	}
	updated, err := store.BackfillRuleConnectorSnapshot(testRule(legacy.ID))
	if err != nil {
		t.Fatal(err)
	}
	if updated.ConnectorType != "oneapi" || updated.ProviderID != "provider-1" {
		t.Fatalf("legacy snapshot was not backfilled: %#v", updated)
	}
}

func TestStoreListUsagesExpandsTimeWindow(t *testing.T) {
	store, db := newTestStore(t)
	if err := db.AutoMigrate(&usageRow{}); err != nil {
		t.Fatal(err)
	}
	center := time.Now().UTC().Truncate(time.Second)
	rows := []usageRow{
		{ID: "usage-before", CreatedAt: center.Add(-15 * time.Second)},
		{ID: "usage-inside", CreatedAt: center.Add(30 * time.Second)},
		{ID: "usage-after", CreatedAt: center.Add(2 * time.Minute)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	values, err := store.ListUsages(center, center.Add(time.Minute), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].ID != "usage-before" || values[1].ID != "usage-inside" {
		t.Fatalf("unexpected usage window: %#v", values)
	}
}

func TestStoreLockRunIsIdempotent(t *testing.T) {
	store, db := newTestStore(t)
	now := time.Now().UTC()
	if err := db.Create(&RunRow{ID: "lock-run", Status: reconciliation.RunSucceeded, StartedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	first, err := store.GetRun("lock-run")
	if err != nil {
		t.Fatal(err)
	}
	prepared, changed, err := reconciliation.PrepareRunLock(first, "actor-1", time.Now().UTC())
	if err != nil || !changed {
		t.Fatalf("prepare lock: %#v %v", prepared, err)
	}
	first, err = store.SaveRunLock(prepared)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GetRun("lock-run")
	if err != nil {
		t.Fatal(err)
	}
	if first.LockedAt == nil || second.LockedAt == nil || second.LockedBy != first.LockedBy || second.LockedBy != "actor-1" {
		t.Fatalf("repeated lock changed the lock: first=%#v second=%#v", first, second)
	}
}
