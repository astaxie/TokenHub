package server

import (
	"gorm.io/gorm"
	"strings"
	billingstore "tokenhub/backend/internal/billing/persistence"
)

// External records remain a separate source, never added to local estimates.
func appendExternalStatementRows(tx *gorm.DB, out *statementResult) error {
	if len(out.Query.ProjectIDs) > 0 {
		return nil
	}
	var records []billingstore.RecordRow
	query := tx.Where("usage_start_at < ? AND (usage_end_at > ? OR (usage_end_at = ? AND usage_start_at = ?)) AND created_at <= ?", out.To, out.From, out.From, out.From, out.GeneratedAt)
	providerExpr := statementJSON(tx, "metadata", "tokenhub_provider_id")
	resourceExpr := statementJSON(tx, "metadata", "tokenhub_resource_id")
	if out.Query.ProviderID != "" {
		query = query.Where(providerExpr+" = ?", out.Query.ProviderID)
	}
	if out.Query.ResourceID != "" {
		query = query.Where(resourceExpr+" = ?", out.Query.ResourceID)
	}
	if out.Query.Model != "" {
		query = query.Where("model = ?", out.Query.Model)
	}
	if err := query.Limit(statementRowLimit + 1).Find(&records).Error; err != nil {
		return err
	}
	if err := statementLimit(len(records)); err != nil {
		return err
	}
	for _, r := range records {
		provider := r.Metadata["tokenhub_provider_id"]
		resource := r.Metadata["tokenhub_resource_id"]
		if out.Query.ProviderID != "" && provider != out.Query.ProviderID || out.Query.ResourceID != "" && resource != out.Query.ResourceID {
			continue
		}
		row := statementRow{ExternalID: r.ExternalID, ExternalRequestID: r.ExternalRequestID, UsageQuantity: r.UsageQuantity, UsageUnit: r.UsageUnit, ID: r.ID, Source: "provider_billed", At: r.UsageStartAt, EndAt: &r.UsageEndAt, Timezone: r.SourceTimezone, Model: r.Model, ProviderID: provider, ResourceID: resource, Currency: strings.ToUpper(r.Currency), Amount: statementString(r.NetAmount), Status: "provider_billed", Reason: "supplier_reported"}
		if row.Currency == "USD" {
			row.USD = row.Amount
		}
		if r.UsageStartAt.Before(out.From) || r.UsageEndAt.After(out.To) {
			row.Status = "period_overlap"
			row.Reason = "full_supplier_amount_not_prorated"
		}
		out.Rows = append(out.Rows, row)
	}
	return statementLimit(len(out.Rows))
}
