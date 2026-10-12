package server

import (
	"encoding/json"
	"math"
)

// Quantity nil means unreported, while a pointer to zero means measured zero.
type RetrievalUsageEvidence struct {
	Unit     string `json:"unit"`
	Quantity *int64 `json:"quantity,omitempty"`
	Source   string `json:"source"`
}

func retrievalCount(value any) (*int64, bool) {
	if value == nil {
		return nil, true
	}
	encoded, err := json.Marshal(value)
	var count float64
	if err != nil || json.Unmarshal(encoded, &count) != nil || count < 0 || math.Trunc(count) != count || count >= float64(math.MaxInt64) {
		return nil, false
	}
	n := int64(count)
	return &n, true
}
func retrievalUsage(body map[string]any, searchUnits bool) Usage {
	usage := usageFromMap(body)
	evidence := &RetrievalUsageEvidence{Unit: "token", Source: "unreported"}
	usage.RetrievalEvidence = evidence
	if searchUnits {
		evidence.Unit = "search_unit"
		meta, _ := body["meta"].(map[string]any)
		billed, _ := meta["billed_units"].(map[string]any)
		count, ok := retrievalCount(billed["search_units"])
		if !ok {
			usage.MeteringInvalid = true
			evidence.Source = "invalid"
			return usage
		}
		evidence.Quantity = count
		if count != nil {
			evidence.Source = "upstream"
		}
		return usage
	}
	raw, _ := body["usage"].(map[string]any)
	counts := map[string]*int64{}
	for _, key := range []string{"prompt_tokens", "input_tokens", "completion_tokens", "output_tokens", "total_tokens"} {
		count, ok := retrievalCount(raw[key])
		if !ok {
			usage.MeteringInvalid = true
			evidence.Source = "invalid"
			return usage
		}
		counts[key] = count
	}
	alias := func(a, b string) (*int64, bool) {
		x, y := counts[a], counts[b]
		if x != nil && y != nil && *x != *y {
			return nil, false
		}
		if x != nil {
			return x, true
		}
		return y, true
	}
	input, okIn := alias("prompt_tokens", "input_tokens")
	output, okOut := alias("completion_tokens", "output_tokens")
	total := counts["total_tokens"]
	invalid := !okIn || !okOut
	var out int64
	if output != nil {
		out = *output
	}
	if input != nil && out > math.MaxInt64-*input {
		invalid = true
	}
	if total != nil && input != nil && !invalid && *total != *input+out {
		invalid = true
	}
	if total != nil && out > *total {
		invalid = true
	}
	if invalid {
		usage.MeteringInvalid = true
		evidence.Source = "invalid"
		return usage
	}
	if total == nil && input != nil {
		n := *input + out
		total = &n
	}
	if total != nil {
		evidence.Quantity = total
		evidence.Source = "upstream"
		usage.TotalTokens = *total
		usage.CompletionTokens = out
		if input != nil {
			usage.PromptTokens = *input
		} else {
			usage.PromptTokens = *total - out
		}
	}
	return usage
}
func validNativeRetrievalEvidence(e *RetrievalUsageEvidence) bool {
	return e != nil && e.Unit == "search_unit" && e.Quantity != nil && *e.Quantity >= 0 && (e.Source == "upstream" || e.Source == "plugin")
}
func validateRetrievalUsageResult(usage Usage) error {
	e := usage.RetrievalEvidence
	if e == nil {
		return nil
	}
	if (e.Unit != "token" && e.Unit != "search_unit") || e.Source == "invalid" || (e.Quantity != nil && *e.Quantity < 0) ||
		(e.Unit == "token" && usage.MeteringInvalid) {
		return NewHTTPError(502, "invalid_provider_usage", "Provider returned inconsistent retrieval usage")
	}
	return nil
}
