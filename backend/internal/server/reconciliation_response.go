package server

type reconciliationRuleResponse ReconciliationRule

type reconciliationRunResponse struct {
	ReconciliationRun
	ComparisonScope string `json:"comparison_scope"`
	TokenComparison string `json:"token_comparison"`
}

type reconciliationDetailResponse struct {
	Run    reconciliationRunResponse `json:"run"`
	Items  []ReconciliationItem      `json:"items"`
	Total  int64                     `json:"total"`
	Limit  int                       `json:"limit"`
	Offset int                       `json:"offset"`
}

func newReconciliationRuleResponse(rule ReconciliationRule) reconciliationRuleResponse {
	response := reconciliationRuleResponse(rule)
	response.DimensionMappings = reconciliationResponseDimensionMappings(rule.DimensionMappings)
	return response
}

func newReconciliationRuleResponses(rules []ReconciliationRule) []reconciliationRuleResponse {
	responses := make([]reconciliationRuleResponse, len(rules))
	for index := range rules {
		responses[index] = newReconciliationRuleResponse(rules[index])
	}
	return responses
}

func newReconciliationRunResponse(run ReconciliationRun) reconciliationRunResponse {
	response := reconciliationRunResponse{ReconciliationRun: run, ComparisonScope: "amount_only", TokenComparison: "not_performed"}
	response.DimensionMappings = reconciliationResponseDimensionMappings(run.DimensionMappings)
	return response
}

func newReconciliationRunResponses(runs []ReconciliationRun) []reconciliationRunResponse {
	responses := make([]reconciliationRunResponse, len(runs))
	for index := range runs {
		responses[index] = newReconciliationRunResponse(runs[index])
	}
	return responses
}

func newReconciliationDetailResponse(detail ReconciliationDetail) reconciliationDetailResponse {
	return reconciliationDetailResponse{
		Run: newReconciliationRunResponse(detail.Run), Items: detail.Items,
		Total: detail.Total, Limit: detail.Limit, Offset: detail.Offset,
	}
}

func reconciliationResponseDimensionMappings(values map[string]map[string]string) map[string]map[string]string {
	result := map[string]map[string]string{}
	for dimension, entries := range values {
		switch dimension {
		case "provider", "model", "project":
		default:
			continue
		}
		copied := make(map[string]string, len(entries))
		for source, canonical := range entries {
			copied[source] = canonical
		}
		result[dimension] = copied
	}
	return result
}
