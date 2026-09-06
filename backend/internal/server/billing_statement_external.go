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
	var connectors []billingstore.ConnectorRow
	if err := tx.Select("id", "config").Find(&connectors).Error; err != nil {
		return err
	}
	configs := map[string]map[string]string{}
	for _, c := range connectors {
		configs[c.ID] = c.Config
	}
	var records []billingstore.RecordRow
	query := tx.Where("usage_start_at < ? AND usage_end_at >= ? AND created_at <= ?", out.To, out.From, out.GeneratedAt)
	providerExpr := "COALESCE(NULLIF(" + statementJSON(tx, "metadata", "provider_id") + ", ''), (SELECT " + statementJSON(tx, "config", "provider_id") + " FROM billing_connectors WHERE id = billing_records.connector_id), '')"
	resourceExpr := "COALESCE(NULLIF(" + statementJSON(tx, "metadata", "provider_resource_id") + ", ''), NULLIF(" + statementJSON(tx, "metadata", "resource_id") + ", ''), (SELECT NULLIF(" + statementJSON(tx, "config", "provider_resource_id") + ", '') FROM billing_connectors WHERE id = billing_records.connector_id), account_id, '')"
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
		if r.UsageEndAt.Equal(out.From) && r.UsageStartAt.Before(out.From) {
			continue
		}
		config := configs[r.ConnectorID]
		provider := firstNonEmpty(r.Metadata["provider_id"], config["provider_id"])
		resource := firstNonEmpty(r.Metadata["provider_resource_id"], r.Metadata["resource_id"], config["provider_resource_id"], r.AccountID)
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
