package server

import (
	"strconv"
	"strings"
)

// Unknown or malformed limits preserve legacy eligibility; a verified positive
// limit constrains a single response independently of the total context window.
func catalogOutputBudgetFits(model ProviderModel, budget int) bool {
	limit, err := strconv.ParseInt(strings.TrimSpace(model.Metadata["max_output_tokens"]), 10, 64)
	return err != nil || limit <= 0 || int64(budget) <= limit
}
