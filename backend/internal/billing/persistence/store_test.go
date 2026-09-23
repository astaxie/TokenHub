package persistence

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"tokenhub/backend/internal/billing"
)

func newTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(Models()...); err != nil {
		t.Fatal(err)
	}
	return NewStore(db, &sync.Mutex{}, "billing-persistence-test-secret", nil), db
}

func TestStoreReadsLegacyTablesAndKeepsProtectedDataEncrypted(t *testing.T) {
	store, db := newTestStore(t)
	created, err := store.CreateBillingConnector(billing.Connector{
		ID: "bcon_legacy", Name: "Legacy connector", Type: billing.ConnectorOneAPI,
		BaseURL: "https://billing.example.test", Status: billing.StatusActive,
		Credentials: map[string]string{"api_token": "connector-secret"},
	})
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if created.CredentialsConfigured != true || created.Credentials != nil {
		t.Fatalf("connector summary leaked credentials: %#v", created)
	}

	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	legacyRow := RecordRow{
		ID: "bill_legacy", ConnectorID: created.ID, ExternalID: "legacy-record", SourceType: billing.ConnectorOneAPI,
		Currency: "USD", NetAmount: "1", UsageStartAt: start.Add(time.Hour), UsageEndAt: start.Add(2 * time.Hour),
		BillingPeriod: "2026-07", CreatedAt: start, UpdatedAt: start,
	}
	if err := db.Table("billing_records").Create(&legacyRow).Error; err != nil {
		t.Fatalf("insert legacy billing row: %v", err)
	}
	records, err := store.ListBillingRecordsInRange(created.ID, start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("read legacy billing row: %v", err)
	}
	if len(records) != 1 || records[0].ID != legacyRow.ID || records[0].NetAmount != "1" {
		t.Fatalf("legacy billing record was not mapped correctly: %#v", records)
	}

	inserted, updated, err := store.SaveBillingPage(created.ID, "checkpoint-1", []billing.Record{{
		ExternalID: "protected-record", UsageStartAt: start.Add(3 * time.Hour), UsageEndAt: start.Add(4 * time.Hour),
		BillingPeriod: "2026-07", RawPayload: "provider-raw-secret",
	}})
	if err != nil || inserted != 1 || updated != 0 {
		t.Fatalf("save billing page: inserted=%d updated=%d err=%v", inserted, updated, err)
	}
	inserted, updated, err = store.SaveBillingPage(created.ID, "checkpoint-2", []billing.Record{{
		ExternalID: "protected-record", UsageStartAt: start.Add(3 * time.Hour), UsageEndAt: start.Add(4 * time.Hour),
		BillingPeriod: "2026-07", RawPayload: "provider-raw-secret",
	}})
	if err != nil || inserted != 0 || updated != 1 {
		t.Fatalf("repeat billing page was not idempotent: inserted=%d updated=%d err=%v", inserted, updated, err)
	}

	var connectorRow ConnectorRow
	if err := db.Table("billing_connectors").First(&connectorRow, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if connectorRow.Checkpoint != "checkpoint-2" || !strings.HasPrefix(connectorRow.CredentialCiphertext, "enc:v1:") || strings.Contains(connectorRow.CredentialCiphertext, "connector-secret") {
		t.Fatalf("legacy connector storage changed or exposed credentials: %#v", connectorRow)
	}
	var snapshots []RawSnapshotRow
	if err := db.Table("billing_raw_snapshots").Where("connector_id = ?", created.ID).Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || !strings.HasPrefix(snapshots[0].PayloadCiphertext, "enc:v1:") || strings.Contains(snapshots[0].PayloadCiphertext, "provider-raw-secret") {
		t.Fatalf("raw snapshot was not encrypted or deduplicated: %#v", snapshots)
	}
}
