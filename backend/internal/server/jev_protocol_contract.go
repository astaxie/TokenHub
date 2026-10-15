package server

import (
	"encoding/json"
	"slices"
	"strings"
)

const jevOpenAIProfile = "openai-chat-responses-v1"

// Compatibility reports only fixed reason codes and recognized field names.
// Request values and unknown user-controlled field names must not enter audit logs.
type jevCompatibility struct {
	Reason    string `json:"reason,omitempty"`
	Parameter string `json:"parameter,omitempty"`
}

func jevReject(reason, field string) jevCompatibility {
	if field != "" && !slices.Contains(strings.Fields(jevKnownFields), field) {
		field = "unknown"
	}
	return jevCompatibility{Reason: reason, Parameter: field}
}

const jevKnownFields = `model messages input instructions stream stream_options background store user metadata prompt_cache_key previous_response_id client_metadata max_tokens max_completion_tokens max_output_tokens temperature top_p top_logprobs logprobs tools tool_choice parallel_tool_calls response_format text reasoning reasoning_effort stop presence_penalty frequency_penalty min_p top_k include truncation service_tier safety_identifier prompt_cache_options prompt_cache_retention max_tool_calls`

func jevChatCompatibility(model ProviderModel, req ChatCompletionRequest) jevCompatibility {
	data, err := json.Marshal(req)
	if err != nil {
		return jevReject("invalid_request", "")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return jevReject("invalid_request", "")
	}
	result := jevCheckRequest(model, "chat/completions", raw, len(data), jevOutputBudget(raw, req.MaxTokens), req.Messages)
	if result.Reason == "" && !catalogReasoningEffortFits(model, req.ReasoningEffort) {
		return jevReject("reasoning_effort_unsupported", "reasoning_effort")
	}
	return result
}

func jevResponsesCompatibility(model ProviderModel, req ResponsesRequest) jevCompatibility {
	data, err := json.Marshal(req)
	if err != nil {
		return jevReject("invalid_request", "")
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return jevReject("invalid_request", "")
	}
	result := jevCheckRequest(model, "responses", raw, len(data), req.MaxTokens, req.Input)
	if result.Reason == "" && !catalogReasoningEffortFits(model, responsesReasoningEffort(req)) {
		return jevReject("reasoning_effort_unsupported", "reasoning")
	}
	return result
}

func jevCheckRequest(model ProviderModel, endpoint string, raw map[string]json.RawMessage, size, budget int, input any) jevCompatibility {
	model.SupportedParameters = catalogReasoningParameters(model.SupportedParameters, model.Modality, model.Metadata)
	if model.Status != StatusActive {
		return jevReject("model_disabled", "")
	}
	if budget < 0 {
		return jevReject("invalid_output_budget", "")
	}
	if len(model.OutputModalities) > 0 && !slices.Contains(model.OutputModalities, "text") {
		return jevReject("output_modality_unsupported", "")
	}
	if (len(model.InputModalities) > 0 && !slices.Contains(model.InputModalities, "text")) || !jevInputModalitiesFit(model, input) {
		return jevReject("input_modality_unsupported", "")
	}
	if model.ContextWindow > 0 && (int64(size) > model.ContextWindow || int64(budget) > model.ContextWindow-int64(size)) {
		return jevReject("context_limit_exceeded", "")
	}
	profile := model.Metadata["request_parameter_profile"]
	if profile != "" && strings.TrimSpace(model.Metadata["endpoints"]) == "" {
		return jevReject("profile_endpoint_missing", "")
	}
	if profile != "" && profile != jevOpenAIProfile {
		return jevReject("unknown_parameter_profile", "")
	}
	// Legacy catalog endpoints describe the upstream, which can be reached
	// through a protocol bridge. Only an explicit request profile constrains
	// the incoming protocol here; route adapters retain their own checks.
	if profile != "" && !jevHasEndpoint(model.Metadata["endpoints"], endpoint) {
		return jevReject("endpoint_unsupported", "")
	}
	structural := "model messages stream stream_options user metadata prompt_cache_key"
	if endpoint == "responses" {
		structural = "model input instructions stream background store user metadata prompt_cache_key previous_response_id client_metadata"
	}
	keys := make([]string, 0, len(raw))
	for key, value := range raw {
		if string(value) != "null" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		if jevCSVContains(model.Metadata["request_parameter_denied"], key) {
			return jevReject("parameter_explicitly_denied", key)
		}
		if slices.Contains(strings.Fields(structural), key) {
			continue
		}
		if profile == jevOpenAIProfile {
			if jevWrongEndpointField(endpoint, key) {
				return jevReject("parameter_endpoint_mismatch", key)
			}
			if jevProtocolControl(endpoint, key) {
				continue
			}
			if required := jevParameterCapability(key); required != "" {
				if !jevHasCapability(model, required) {
					return jevReject("capability_missing", key)
				}
				continue
			}
		}
		if !slices.Contains(model.SupportedParameters, key) {
			return jevReject("parameter_not_declared", key)
		}
	}
	if profile == jevOpenAIProfile {
		return jevCheckConditions(model, endpoint, raw)
	}
	return jevCompatibility{}
}

// Normalize metadata lists, not request values. Empty entries never grant
// support or match a denied field.
func jevCSVContains(list, wanted string) bool {
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item != "" && item == wanted {
			return true
		}
	}
	return false
}

func jevHasEndpoint(endpoints, wanted string) bool {
	for _, endpoint := range strings.Split(endpoints, ",") {
		if strings.TrimPrefix(strings.TrimSpace(endpoint), "/v1/") == wanted {
			return true
		}
	}
	return false
}

func jevProtocolControl(endpoint, key string) bool {
	if slices.Contains([]string{"service_tier", "safety_identifier", "prompt_cache_options", "prompt_cache_retention", "store"}, key) {
		return true
	}
	return endpoint == "responses" && slices.Contains([]string{"include", "truncation", "max_tool_calls", "text", "stream_options"}, key)
}

func jevWrongEndpointField(endpoint, key string) bool {
	if endpoint == "responses" {
		return slices.Contains([]string{"messages", "response_format", "reasoning_effort", "max_tokens", "max_completion_tokens", "logprobs"}, key)
	}
	return slices.Contains([]string{"input", "instructions", "text", "reasoning", "max_output_tokens", "include", "truncation", "background", "previous_response_id", "max_tool_calls", "client_metadata"}, key)
}

func jevParameterCapability(key string) string {
	switch key {
	case "tools", "tool_choice", "parallel_tool_calls":
		return "tools"
	case "response_format":
		return "structured_outputs"
	case "reasoning", "reasoning_effort":
		return "reasoning"
	}
	return ""
}

func jevHasCapability(model ProviderModel, capability string) bool {
	if slices.Contains(model.Capabilities, capability) {
		return true
	}
	parameter := map[string]string{"tools": "tools", "structured_outputs": "response_format", "reasoning": "reasoning"}[capability]
	return slices.Contains(model.SupportedParameters, parameter)
}

func jevCheckConditions(model ProviderModel, endpoint string, raw map[string]json.RawMessage) jevCompatibility {
	if value, ok := raw["text"]; ok && string(value) != "null" {
		var text struct {
			Format struct {
				Type string `json:"type"`
			} `json:"format"`
		}
		if json.Unmarshal(value, &text) != nil {
			return jevReject("invalid_parameter_type", "text")
		}
		if text.Format.Type != "" && text.Format.Type != "text" && !jevHasCapability(model, "structured_outputs") {
			return jevReject("capability_missing", "text")
		}
	}
	effort := model.Metadata["reasoning_effort_default"]
	if endpoint == "responses" {
		if value, ok := raw["reasoning"]; ok && string(value) != "null" {
			var reasoning map[string]json.RawMessage
			if json.Unmarshal(value, &reasoning) != nil {
				return jevReject("invalid_parameter_type", "reasoning")
			}
			if value, ok := reasoning["effort"]; ok {
				if json.Unmarshal(value, &effort) != nil || effort == "" {
					return jevReject("invalid_reasoning_effort", "reasoning")
				}
			}
		}
	} else if value, ok := raw["reasoning_effort"]; ok && string(value) != "null" {
		if json.Unmarshal(value, &effort) != nil || effort == "" {
			return jevReject("invalid_reasoning_effort", "reasoning_effort")
		}
	}
	if options := model.Metadata["reasoning_effort_options"]; effort != "" && options != "" && !jevCSVContains(options, effort) {
		return jevReject("reasoning_effort_unsupported", "reasoning")
	}
	required := model.Metadata["sampling_requires_reasoning_effort"]
	for _, key := range []string{"temperature", "top_p", "logprobs", "top_logprobs"} {
		if value, ok := raw[key]; ok && string(value) != "null" && required != "" && effort != required {
			return jevReject("sampling_reasoning_conflict", key)
		}
	}
	if value, ok := raw["include"]; ok && string(value) != "null" {
		var includes []string
		if json.Unmarshal(value, &includes) != nil {
			return jevReject("invalid_parameter_type", "include")
		}
		for _, item := range includes {
			switch item {
			case "reasoning.encrypted_content":
				if !jevHasCapability(model, "reasoning") {
					return jevReject("capability_missing", "include")
				}
			case "message.output_text.logprobs":
				if !slices.Contains(model.SupportedParameters, "top_logprobs") || (required != "" && effort != required) {
					return jevReject("sampling_reasoning_conflict", "include")
				}
			case "file_search_call.results", "web_search_call.results", "web_search_call.action.sources", "code_interpreter_call.outputs", "computer_call_output.output.image_url":
				if !jevHasCapability(model, "tools") {
					return jevReject("capability_missing", "include")
				}
			case "message.input_image.image_url":
				if !slices.Contains(model.InputModalities, "image") {
					return jevReject("input_modality_unsupported", "include")
				}
			default:
				return jevReject("include_value_unsupported", "include")
			}
		}
	}
	if endpoint == "chat/completions" && jevUsesTools(raw) {
		required := model.Metadata["chat_tools_requires_reasoning_effort"]
		if required == "unsupported" {
			return jevReject("tools_endpoint_unsupported", "tools")
		}
		if required != "" && effort != required {
			return jevReject("tools_reasoning_conflict", "tools")
		}
	}
	return jevCompatibility{}
}

func jevUsesTools(raw map[string]json.RawMessage) bool {
	// The exact sentinel disables tool calls, including when parallel/tool
	// definitions are present. Do not normalize other request values.
	if value, ok := raw["tool_choice"]; ok {
		var choice string
		if json.Unmarshal(value, &choice) == nil && choice == "none" {
			return false
		}
	}
	for _, key := range []string{"tool_choice", "parallel_tool_calls"} {
		if v, ok := raw[key]; ok && string(v) != "null" {
			return true
		}
	}
	if v, ok := raw["tools"]; ok && string(v) != "null" {
		var tools []json.RawMessage
		return json.Unmarshal(v, &tools) != nil || len(tools) > 0
	}
	return false
}
