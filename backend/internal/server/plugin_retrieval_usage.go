package server

import "encoding/json"

// New plugins can report native units directly. Legacy positive token counters
// remain supported, but an omitted/zero-only legacy object cannot prove zero.
func pluginRetrievalUsage(response any, reported Usage, searchUnits bool) (Usage, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return reported, err
	}
	var body map[string]any
	if json.Unmarshal(encoded, &body) != nil {
		body = map[string]any{}
	}
	if evidence := reported.RetrievalEvidence; evidence != nil {
		copy := *evidence
		if (copy.Unit == "search_unit") != searchUnits {
			copy.Source = "invalid"
		}
		if copy.Source == "" {
			if copy.Quantity == nil {
				copy.Source = "unreported"
			} else {
				copy.Source = "plugin"
			}
		}
		if copy.Source != "upstream" && copy.Source != "plugin" && copy.Source != "unreported" && copy.Source != "invalid" {
			copy.Source = "invalid"
		}
		if (copy.Source == "unreported") != (copy.Quantity == nil) && copy.Source != "invalid" {
			copy.Source = "invalid"
		}
		reported.RetrievalEvidence = &copy
		if copy.Unit == "token" && copy.Quantity != nil {
			if reported.PromptTokens == 0 && reported.CompletionTokens == 0 && reported.TotalTokens == 0 {
				reported.PromptTokens = *copy.Quantity
				reported.TotalTokens = *copy.Quantity
			}
			if reported.PromptTokens < 0 || reported.CompletionTokens < 0 || reported.TotalTokens != *copy.Quantity || reported.PromptTokens > reported.TotalTokens || reported.CompletionTokens != reported.TotalTokens-reported.PromptTokens {
				reported.MeteringInvalid = true
				copy.Source = "invalid"
			}
		}
		return reported, validateRetrievalUsageResult(reported)
	}
	meta, _ := body["meta"].(map[string]any)
	billed, _ := meta["billed_units"].(map[string]any)
	_, nativePresent := billed["search_units"]
	if nativePresent && !searchUnits {
		reported.RetrievalEvidence = &RetrievalUsageEvidence{Unit: "search_unit", Source: "invalid"}
		return reported, validateRetrievalUsageResult(reported)
	}
	parsed := retrievalUsage(body, searchUnits || nativePresent)
	if parsed.RetrievalEvidence.Source == "unreported" && !searchUnits && !nativePresent &&
		(reported.PromptTokens != 0 || reported.CompletionTokens != 0 || reported.TotalTokens != 0) {
		total := reported.TotalTokens
		if total == 0 {
			total = saturatingAddNonNegative(reported.PromptTokens, reported.CompletionTokens)
		}
		parsed = retrievalUsage(map[string]any{"usage": map[string]any{"prompt_tokens": reported.PromptTokens, "completion_tokens": reported.CompletionTokens, "total_tokens": total}}, false)
	}
	reported.RetrievalEvidence = parsed.RetrievalEvidence
	reported.MeteringInvalid = reported.MeteringInvalid || parsed.MeteringInvalid
	if parsed.RetrievalEvidence.Unit == "token" {
		reported.PromptTokens = parsed.PromptTokens
		reported.CompletionTokens = parsed.CompletionTokens
		reported.TotalTokens = parsed.TotalTokens
	}
	return reported, validateRetrievalUsageResult(reported)
}
