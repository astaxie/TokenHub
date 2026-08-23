package reconciliation

import (
	"sort"
	"strings"

	"tokenhub/backend/internal/billing"
)

func snapshotConnector(rule *Rule, connector ConnectorSnapshot) error {
	rule.ConnectorType = strings.ToLower(strings.TrimSpace(connector.Type))
	rule.ProviderID = normalizeScope(connector.ProviderID)
	rule.ProviderResourceID = normalizeScope(connector.ProviderResourceID)
	return validateConnectorSnapshot(rule.Granularity, rule.ConnectorType, rule.ProviderID)
}

func snapshotRunConnector(run *Run, connector ConnectorSnapshot) error {
	run.ConnectorType = strings.ToLower(strings.TrimSpace(connector.Type))
	run.ProviderID = normalizeScope(connector.ProviderID)
	run.ProviderResourceID = normalizeScope(connector.ProviderResourceID)
	return validateConnectorSnapshot(run.Granularity, run.ConnectorType, run.ProviderID)
}

func validateConnectorSnapshot(granularity string, connectorType string, providerID string) error {
	if normalizeScope(providerID) == "" {
		return NewError(ErrorInvalidInput, "reconciliation_scope_required", "The billing connector must define provider_id before it can be used for reconciliation")
	}
	connectorType = strings.ToLower(strings.TrimSpace(connectorType))
	if connectorType == "" {
		return NewError(ErrorInvalidInput, "reconciliation_connector_snapshot_required", "The billing connector type must be captured before reconciliation")
	}
	if connectorType == billing.ConnectorNewAPI && granularity == GranularityDetail {
		return NewError(ErrorInvalidInput, "reconciliation_detail_unsupported", "The selected billing connector does not provide request-level identifiers")
	}
	return nil
}

func scopeUsages(run Run, usages []Usage) []Usage {
	providerIDs := map[string]bool{}
	resourceIDs := map[string]bool{}
	addScopeValues(providerIDs, run.ProviderID)
	addScopeValues(resourceIDs, run.ProviderResourceID)

	result := make([]Usage, 0, len(usages))
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

func normalizeScope(raw string) string {
	values := map[string]bool{}
	addScopeValues(values, raw)
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
}

func addScopeValues(target map[string]bool, raw string) {
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			target[value] = true
		}
	}
}
