package server

import (
	"encoding/json"
	"reflect"
)

func validateClaudeSubscriptionControls(request ChatCompletionRequest) error {
	if request.Temperature != nil || request.TopP != nil || request.Stop != nil || request.ResponseFormat != nil ||
		request.PresencePenalty != nil && *request.PresencePenalty != 0 ||
		request.FrequencyPenalty != nil && *request.FrequencyPenalty != 0 ||
		request.MinP != nil && *request.MinP != 0 || request.TopK != nil && *request.TopK != 0 {
		return unsupportedCapabilityError("Claude Code subscriptions do not support sampling, stop, or response_format controls")
	}
	// The CLI does not expose these API controls. Explicit default/no-op values
	// remain valid; caller identifiers and gateway usage options are unaffected.
	for _, control := range []struct {
		name         string
		defaultValue any
	}{
		{"n", float64(1)}, {"seed", nil}, {"logit_bias", map[string]any{}},
		{"logprobs", false}, {"top_logprobs", float64(0)},
		{"modalities", []any{"text"}}, {"audio", nil}, {"prediction", nil},
		{"functions", []any{}}, {"function_call", "none"}, {"web_search_options", nil},
		{"service_tier", "default"}, {"verbosity", nil}, {"thinking", nil},
		{"store", false}, {"prompt_cache_retention", nil}, {"max_tool_calls", nil},
	} {
		raw := request.raw[control.name]
		if !jsonRawIsPresent(raw) {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil && control.defaultValue != nil &&
			(reflect.DeepEqual(value, control.defaultValue) || control.name == "service_tier" && value == "auto") {
			continue
		}
		return unsupportedCapabilityError("Claude Code subscriptions do not support " + control.name + " controls")
	}
	return nil
}
