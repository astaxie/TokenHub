package server

import (
	"time"

	"tokenhub/backend/internal/billing"
	"tokenhub/backend/internal/reconciliation"
)

// reconciliationBillingBridge preserves the attribution projection test seam
// while the production reconciliation service uses its persistence adapter.
type reconciliationBillingBridge struct {
	reader interface {
		ListBillingRecordsInRange(string, time.Time, time.Time) ([]billing.Record, error)
	}
}

func (b *reconciliationBillingBridge) ListRecordsInRange(connectorID string, from, to time.Time) ([]reconciliation.BillingRecord, error) {
	records, err := b.reader.ListBillingRecordsInRange(connectorID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]reconciliation.BillingRecord, len(records))
	for index, record := range records {
		result[index] = reconciliation.BillingRecord{
			ID: record.ID, ExternalID: record.ExternalID, SourceType: record.SourceType,
			AccountID: record.AccountID, ProviderID: reconciliationAttribution(record.Metadata, "tokenhub_provider_id", "provider_id"),
			ProviderResourceID: reconciliationAttribution(record.Metadata, "tokenhub_resource_id", "provider_resource_id"), ResourceID: record.Metadata["resource_id"],
			ProjectID: record.Metadata["project_id"], Model: record.Model, Currency: record.Currency,
			NetAmount: record.NetAmount, UsageStartAt: record.UsageStartAt, ExternalRequestID: record.ExternalRequestID,
		}
	}
	return result, nil
}

func reconciliationAttribution(metadata map[string]string, snapshot, legacy string) string {
	if value, exists := metadata[snapshot]; exists {
		return value
	}
	return metadata[legacy]
}
