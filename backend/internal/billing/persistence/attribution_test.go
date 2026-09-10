package persistence

import (
	"testing"
	"time"
	"tokenhub/backend/internal/billing"
)

func TestBillingRecordAttributionSurvivesConnectorChangesAndReimport(t *testing.T) {
	store, db := newTestStore(t)
	connector, err := store.CreateBillingConnector(billing.Connector{ID: "attribution", Name: "Attribution", Type: billing.ConnectorOneAPI, Config: map[string]string{"provider_id": "old-provider", "provider_resource_id": "old-resource"}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	// A pre-upgrade row must be frozen before the first configuration change.
	if err := db.Create(&RecordRow{ID: "legacy", ConnectorID: connector.ID, ExternalID: "legacy", UsageStartAt: at, UsageEndAt: at, Metadata: map[string]string{}}).Error; err != nil {
		t.Fatal(err)
	}
	record := billing.Record{ExternalID: "new", RawPayload: `{}`, UsageStartAt: at, UsageEndAt: at, Metadata: map[string]string{"tokenhub_provider_id": "forged"}}
	if _, _, err := store.SaveBillingPage(connector.ID, "", []billing.Record{record}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateBillingConnector(connector.ID, billing.Connector{Config: map[string]string{"provider_id": "new-provider", "provider_resource_id": "new-resource"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SaveBillingPage(connector.ID, "", []billing.Record{record}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBillingConnector(connector.ID); err != nil {
		t.Fatal(err)
	}
	var rows []RecordRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("records=%d", len(rows))
	}
	for _, row := range rows {
		if row.Metadata[attributionProvider] != "old-provider" || row.Metadata[attributionResource] != "old-resource" {
			t.Fatalf("historical attribution changed: %+v", row)
		}
	}
}
