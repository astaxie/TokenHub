package server

import (
	"encoding/json"
	"slices"
	"strings"
)

// A declared effort control is also accepted by the gateway's Chat contract.
// Preserve explicit capability checks when no effort control is declared.
func catalogReasoningParameters(parameters []string, modality string, metadata map[string]string) []string {
	if len(parameters) > 0 && modality == "chat" && strings.TrimSpace(metadata["reasoning_effort_options"]) != "" && !slices.Contains(parameters, "reasoning_effort") {
		parameters = append(append([]string(nil), parameters...), "reasoning_effort")
	}
	return parameters
}

func catalogReasoningEffortFits(model ProviderModel, effort *string) bool {
	value := normalizedReasoningEffort(effort)
	options := strings.TrimSpace(model.Metadata["reasoning_effort_options"])
	if value == nil || options == "" {
		return true
	}
	canonical := *value
	var aliases map[string]string
	if json.Unmarshal([]byte(model.Metadata["reasoning_effort_aliases"]), &aliases) == nil {
		if alias := aliases[canonical]; alias != "" {
			canonical = alias
		}
	}
	return slices.ContainsFunc(strings.Split(options, ","), func(option string) bool { return strings.TrimSpace(option) == canonical })
}

// Advertised endpoints declare their baseline budget fields independently of
// other explicit budget parameters. Keep wire names unchanged when forwarding.
func catalogBudgetParameters(parameters []string, endpoints string) []string {
	for _, endpoint := range strings.Split(endpoints, ",") {
		var budget string
		switch strings.TrimPrefix(strings.TrimSpace(endpoint), "/v1/") {
		case "chat/completions":
			budget = "max_tokens"
		case "responses":
			budget = "max_output_tokens"
		}
		if budget != "" && !slices.Contains(parameters, budget) {
			parameters = append(parameters, budget)
		}
	}
	return parameters
}
