package server

import (
	"slices"
	"strings"
)

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
