package server

import (
	"net/http"
	"sort"
	"strings"
)

func snapshotReconciliationConnector(rule *ReconciliationRule, connector BillingConnector) error {
	rule.ConnectorType = strings.ToLower(strings.TrimSpace(connector.Type))
	rule.ProviderID = normalizeReconciliationScope(connector.Config["provider_id"])
	rule.ProviderResourceID = normalizeReconciliationScope(connector.Config["provider_resource_id"])
	return validateReconciliationConnectorSnapshot(rule.Granularity, rule.ConnectorType, rule.ProviderID)
}

func snapshotReconciliationRunConnector(run *ReconciliationRun, connector BillingConnector) error {
	run.ConnectorType = strings.ToLower(strings.TrimSpace(connector.Type))
	run.ProviderID = normalizeReconciliationScope(connector.Config["provider_id"])
	run.ProviderResourceID = normalizeReconciliationScope(connector.Config["provider_resource_id"])
	return validateReconciliationConnectorSnapshot(run.Granularity, run.ConnectorType, run.ProviderID)
}

func validateReconciliationConnectorSnapshot(granularity string, connectorType string, providerID string) error {
	if normalizeReconciliationScope(providerID) == "" {
		return NewHTTPError(http.StatusBadRequest, "reconciliation_scope_required", "The billing connector must define provider_id before it can be used for reconciliation")
	}
	connectorType = strings.ToLower(strings.TrimSpace(connectorType))
	if connectorType == "" {
		return NewHTTPError(http.StatusBadRequest, "reconciliation_connector_snapshot_required", "The billing connector type must be captured before reconciliation")
	}
	if connectorType == BillingConnectorNewAPI && granularity == ReconciliationGranularityDetail {
		return NewHTTPError(http.StatusBadRequest, "reconciliation_detail_unsupported", "The selected billing connector does not provide request-level identifiers")
	}
	return nil
}

func scopeReconciliationUsages(run ReconciliationRun, usages []UsageRecord) []UsageRecord {
	providerIDs := map[string]bool{}
	resourceIDs := map[string]bool{}
	addReconciliationScopeValues(providerIDs, run.ProviderID)
	addReconciliationScopeValues(resourceIDs, run.ProviderResourceID)

	result := make([]UsageRecord, 0, len(usages))
	for _, usage := range usages {
		if len(resourceIDs) > 0 {
			if resourceIDs[strings.TrimSpace(usage.ProviderResourceID)] {
				result = append(result, usage)
			}
			continue
		}
		if providerIDs[strings.TrimSpace(usage.ProviderID)] {
			result = append(result, usage)
		}
	}
	return result
}

func normalizeReconciliationScope(raw string) string {
	values := map[string]bool{}
	addReconciliationScopeValues(values, raw)
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
}

func addReconciliationScopeValues(target map[string]bool, raw string) {
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			target[value] = true
		}
	}
}
