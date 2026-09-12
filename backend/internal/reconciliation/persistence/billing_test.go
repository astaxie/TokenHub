package persistence

import (
	"testing"
	"time"

	"tokenhub/backend/internal/billing"
)

type billingSourceFake struct{ records []billing.Record }

func (f billingSourceFake) GetBillingConnector(string, bool) (billing.Connector, error) {
	return billing.Connector{}, nil
}
func (f billingSourceFake) ListBillingRecordsInRange(string, time.Time, time.Time) ([]billing.Record, error) {
	return f.records, nil
}

func TestBillingReaderPreservesSnapshotAttribution(t *testing.T) {
	reader := NewBillingReader(billingSourceFake{records: []billing.Record{
		{ID: "snapshot", Metadata: map[string]string{
			"tokenhub_provider_id": "snapshot-provider", "provider_id": "legacy-provider",
			"tokenhub_resource_id": "snapshot-resource", "provider_resource_id": "legacy-resource",
		}},
		{ID: "empty-snapshot", Metadata: map[string]string{
			"tokenhub_provider_id": "", "provider_id": "legacy-provider",
			"tokenhub_resource_id": "", "provider_resource_id": "legacy-resource",
		}},
		{ID: "legacy", Metadata: map[string]string{"provider_id": "legacy-provider", "provider_resource_id": "legacy-resource"}},
	}})
	records, err := reader.ListRecordsInRange("connector", time.Time{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"snapshot-provider", "snapshot-resource"}, {"", ""}, {"legacy-provider", "legacy-resource"}}
	for index, record := range records {
		if record.ProviderID != want[index][0] || record.ProviderResourceID != want[index][1] {
			t.Fatalf("record %q attribution = %q/%q, want %q/%q", record.ID, record.ProviderID, record.ProviderResourceID, want[index][0], want[index][1])
		}
	}
}
