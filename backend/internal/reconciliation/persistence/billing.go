package persistence

import (
	"time"

	"tokenhub/backend/internal/billing"
	"tokenhub/backend/internal/reconciliation"
)

// BillingSource is the narrow billing projection consumed by the
// reconciliation billing adapter.
type BillingSource interface {
	GetBillingConnector(string, bool) (billing.Connector, error)
	ListBillingRecordsInRange(string, time.Time, time.Time) ([]billing.Record, error)
}

// BillingReader adapts billing's public projection to reconciliation's port.
type BillingReader struct {
	reader BillingSource
}

func NewBillingReader(reader BillingSource) *BillingReader {
	return &BillingReader{reader: reader}
}

var _ reconciliation.BillingReader = (*BillingReader)(nil)

func (b *BillingReader) GetConnectorSnapshot(id string) (reconciliation.ConnectorSnapshot, error) {
	connector, err := b.reader.GetBillingConnector(id, false)
	if err != nil {
		return reconciliation.ConnectorSnapshot{}, err
	}
	return reconciliation.ConnectorSnapshot{Type: connector.Type, ProviderID: connector.Config["provider_id"], ProviderResourceID: connector.Config["provider_resource_id"]}, nil
}

func (b *BillingReader) ListRecordsInRange(connectorID string, from, to time.Time) ([]reconciliation.BillingRecord, error) {
	records, err := b.reader.ListBillingRecordsInRange(connectorID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]reconciliation.BillingRecord, len(records))
	for i, record := range records {
		result[i] = reconciliation.BillingRecord{ID: record.ID, ExternalID: record.ExternalID, SourceType: record.SourceType, AccountID: record.AccountID, ProviderID: record.Metadata["provider_id"], ProviderResourceID: record.Metadata["provider_resource_id"], ResourceID: record.Metadata["resource_id"], ProjectID: record.Metadata["project_id"], Model: record.Model, Currency: record.Currency, NetAmount: record.NetAmount, UsageStartAt: record.UsageStartAt, ExternalRequestID: record.ExternalRequestID}
	}
	return result, nil
}
