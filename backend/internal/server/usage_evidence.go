package server

import (
	"encoding/json"
	"math"
	"strings"
)

// Evidence is internal accounting metadata, never part of a public model reply.
// Missing values stay nullable instead of inheriting a numeric zero default.
type usageEvidenceField struct {
	Value *int64 `json:"value"`
	State string `json:"state"`
}
type usageEvidence struct {
	Protocol       string                        `json:"protocol"`
	Fields         map[string]usageEvidenceField `json:"fields"`
	StreamComplete *bool                         `json:"stream_complete,omitempty"`
	InvocationID   string                        `json:"invocation_id,omitempty"`
	ResponseID     string                        `json:"response_id,omitempty"`
	TraceID        string                        `json:"trace_id,omitempty"`
}

func evidenceField(values ...any) usageEvidenceField {
	for _, raw := range values {
		if raw == nil {
			continue
		}
		var number int64
		valid := true
		switch value := raw.(type) {
		case int64:
			number = value
		case int:
			number = int64(value)
		case json.Number:
			var err error
			number, err = value.Int64()
			valid = err == nil
		case float64:
			valid = !math.IsNaN(value) && !math.IsInf(value, 0) && value >= math.MinInt64 && value < math.MaxInt64 && math.Trunc(value) == value
			if valid {
				number = int64(value)
			}
		default:
			valid = false
		}
		if !valid {
			return usageEvidenceField{State: "invalid"}
		}
		state := "reported"
		if number < 0 {
			state = "invalid"
		}
		return usageEvidenceField{Value: &number, State: state}
	}
	return usageEvidenceField{State: "missing"}
}
func derivedEvidence(value int64) usageEvidenceField {
	return usageEvidenceField{Value: &value, State: "derived"}
}
func (e *usageEvidence) has(name string) bool {
	field, ok := e.Fields[name]
	return ok && field.Value != nil && field.State != "invalid" && field.State != "missing"
}
func (e *usageEvidence) invalid() bool {
	for _, f := range e.Fields {
		if f.State == "invalid" {
			return true
		}
	}
	return false
}
func captureOpenAIUsageEvidence(raw map[string]any, usage Usage) *usageEvidence {
	input, _ := firstNonNil(raw["prompt_tokens_details"], raw["input_tokens_details"]).(map[string]any)
	output, _ := firstNonNil(raw["completion_tokens_details"], raw["output_tokens_details"]).(map[string]any)
	e := &usageEvidence{Protocol: "openai", Fields: map[string]usageEvidenceField{
		"input_total":         evidenceField(raw["prompt_tokens"], raw["input_tokens"]),
		"cache_read":          evidenceField(input["cached_tokens"], raw["prompt_cache_hit_tokens"], raw["cached_input_tokens"], raw["cached_tokens"], raw["total_cached_tokens"]),
		"cache_write_total":   evidenceField(raw["cache_write_input_tokens"], input["cache_write_tokens"]),
		"cache_write_5m":      evidenceField(raw["cache_write_5m_input_tokens"], input["cache_write_5m_tokens"], input["ephemeral_5m_input_tokens"]),
		"cache_write_1h":      evidenceField(raw["cache_write_1h_input_tokens"], input["cache_write_1h_tokens"], input["ephemeral_1h_input_tokens"]),
		"output":              evidenceField(raw["completion_tokens"], raw["output_tokens"]),
		"total":               evidenceField(raw["total_tokens"]),
		"input_audio":         evidenceField(input["audio_tokens"], raw["input_audio_tokens"]),
		"output_audio":        evidenceField(output["audio_tokens"], raw["output_audio_tokens"]),
		"reasoning":           evidenceField(raw["reasoning_output_tokens"], output["reasoning_tokens"]),
		"accepted_prediction": evidenceField(output["accepted_prediction_tokens"], raw["accepted_prediction_tokens"], raw["output_accepted_prediction_tokens"]),
		"rejected_prediction": evidenceField(output["rejected_prediction_tokens"], raw["rejected_prediction_tokens"], raw["output_rejected_prediction_tokens"]),
	}}
	if !e.has("total") && e.Fields["total"].State == "missing" && e.has("input_total") && e.has("output") {
		e.Fields["total"] = derivedEvidence(usage.TotalTokens)
	}
	return e
}
func captureAnthropicUsageEvidence(raw map[string]any, usage Usage) *usageEvidence {
	creation, _ := raw["cache_creation"].(map[string]any)
	e := &usageEvidence{Protocol: "anthropic", Fields: map[string]usageEvidenceField{
		"input_uncached": evidenceField(raw["input_tokens"]), "cache_read": evidenceField(raw["cache_read_input_tokens"]),
		"cache_write_total": evidenceField(raw["cache_creation_input_tokens"]),
		"cache_write_5m":    evidenceField(creation["ephemeral_5m_input_tokens"]), "cache_write_1h": evidenceField(creation["ephemeral_1h_input_tokens"]),
		"output": evidenceField(raw["output_tokens"]), "input_total": {State: "missing"}, "total": {State: "missing"},
	}}
	if e.has("input_uncached") && e.has("cache_read") && e.has("cache_write_total") {
		e.Fields["input_total"] = derivedEvidence(usage.PromptTokens)
	}
	if e.has("input_total") && e.has("output") {
		e.Fields["total"] = derivedEvidence(usage.TotalTokens)
	}
	return e
}
func captureGeminiUsageEvidence(raw map[string]any, usage Usage) *usageEvidence {
	e := &usageEvidence{Protocol: "gemini", Fields: map[string]usageEvidenceField{
		"input_total": evidenceField(raw["promptTokenCount"]), "cache_read": evidenceField(raw["cachedContentTokenCount"], raw["totalCachedTokens"]),
		"output": evidenceField(raw["candidatesTokenCount"]), "reasoning": evidenceField(raw["thoughtsTokenCount"]),
		"total": evidenceField(raw["totalTokenCount"]), "cache_write_total": {State: "missing"}, "cache_write_5m": {State: "missing"}, "cache_write_1h": {State: "missing"},
	}}
	if e.has("output") && e.has("reasoning") {
		e.Fields["output"] = derivedEvidence(usage.CompletionTokens)
	}
	if e.Fields["total"].State == "missing" && e.has("input_total") && e.has("output") {
		e.Fields["total"] = derivedEvidence(usage.TotalTokens)
	}
	return e
}
func evidenceForUsage(usage Usage) *usageEvidence {
	if usage.Evidence != nil {
		return usage.Evidence
	}
	fields := map[string]usageEvidenceField{}
	for name, v := range map[string]int64{"input_total": usage.PromptTokens, "cache_read": usage.CachedInputTokens, "cache_write_total": usage.CacheWriteInputTokens, "cache_write_5m": usage.CacheWrite5mInputTokens, "cache_write_1h": usage.CacheWrite1hInputTokens, "output": usage.CompletionTokens, "total": usage.TotalTokens} {
		value := v
		fields[name] = usageEvidenceField{Value: &value, State: "legacy_unverified"}
	}
	return &usageEvidence{Protocol: "legacy", Fields: fields}
}
func usageEvidenceReason(e *usageEvidence) string {
	if e.invalid() {
		return "invalid_usage_fields"
	}
	if e.StreamComplete != nil && !*e.StreamComplete {
		return "stream_completion_unknown"
	}
	if e.Protocol != "legacy" && (!e.has("input_total") || !e.has("output")) {
		return "usage_fields_missing"
	}
	return ""
}
func boundedUsageID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		return ""
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return ""
		}
	}
	return value
}
