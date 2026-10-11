package server

import (
	"encoding/json"
	"testing"
)

func TestJevRequestContract(t *testing.T) {
	model := ProviderModel{Status: StatusActive, SupportedParameters: []string{"reasoning", "temperature", "top_p", "tools", "response_format"}, Capabilities: []string{"reasoning", "tools", "structured_outputs"}, Metadata: map[string]string{
		"endpoints": "responses,chat/completions", "request_parameter_profile": "openai-chat-responses-v1", "reasoning_effort_options": "none,low,medium,high", "reasoning_effort_default": "medium", "sampling_requires_reasoning_effort": "none", "chat_tools_requires_reasoning_effort": "none",
	}}
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"protocol controls do not need repeated model declarations", `{"input":"task","include":["reasoning.encrypted_content"],"truncation":"disabled","service_tier":"auto","store":false}`, true},
		{"tool controls follow tools capability", `{"input":"task","tools":[{"type":"function","name":"lookup"}],"tool_choice":"required","parallel_tool_calls":false}`, true},
		{"Responses structured outputs follow capability", `{"input":"task","text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}}}`, true},
		{"sampling default effort rejected", `{"input":"task","temperature":0.5}`, false},
		{"sampling explicit none accepted", `{"input":"task","temperature":0.5,"reasoning":{"effort":"none"}}`, true},
		{"invalid effort rejected", `{"input":"task","reasoning":{"effort":"minimal"}}`, false},
		{"unknown parameter rejected", `{"input":"task","vendor_secret":"sensitive"}`, false},
		{"Chat field rejected on Responses", `{"input":"task","response_format":{"type":"json_object"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req ResponsesRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatal(err)
			}
			if got := jevResponsesModelFits(model, req); got != tc.want {
				t.Fatalf("compatibility=%v, want %v", got, tc.want)
			}
		})
	}
	t.Run("empty declarations are not a wildcard", func(t *testing.T) {
		var req ResponsesRequest
		if err := json.Unmarshal([]byte(`{"input":"task","vendor_secret":true}`), &req); err != nil {
			t.Fatal(err)
		}
		if jevResponsesModelFits(ProviderModel{Status: StatusActive}, req) {
			t.Fatal("unknown parameter admitted with empty metadata")
		}
	})
}

func TestJevProtocolConditionsAndLegacyBoundary(t *testing.T) {
	base := ProviderModel{Status: StatusActive, SupportedParameters: []string{"reasoning", "tools", "response_format", "temperature", "top_logprobs"}, Metadata: map[string]string{
		"endpoints": "chat/completions,responses", "request_parameter_profile": jevOpenAIProfile, "reasoning_effort_options": "none,low,medium", "reasoning_effort_default": "medium", "sampling_requires_reasoning_effort": "none", "chat_tools_requires_reasoning_effort": "none",
	}}
	for _, tc := range []struct {
		name, endpoint, body, reason string
		change                       func(*ProviderModel)
	}{
		{"chat tools explicitly disabled", "chat/completions", `{"tool_choice":"none"}`, "", nil},
		{"chat tools disabled despite parallel flag", "chat/completions", `{"tool_choice":"none","parallel_tool_calls":false}`, "", nil},
		{"chat tool default effort", "chat/completions", `{"tools":[{"type":"function"}]}`, "tools_reasoning_conflict", nil},
		{"chat tool explicit none", "chat/completions", `{"tools":[{"type":"function"}],"reasoning_effort":"none"}`, "", nil},
		{"tools forbidden on chat", "chat/completions", `{"tools":[{"type":"function"}],"reasoning_effort":"none"}`, "tools_endpoint_unsupported", func(m *ProviderModel) { m.Metadata["chat_tools_requires_reasoning_effort"] = "unsupported" }},
		{"explicit empty tools", "chat/completions", `{"tools":[]}`, "", nil},
		{"unsupported endpoint", "responses", `{}`, "endpoint_unsupported", func(m *ProviderModel) { m.Metadata["endpoints"] = "chat/completions" }},
		{"unknown profile", "responses", `{}`, "unknown_parameter_profile", func(m *ProviderModel) { m.Metadata["request_parameter_profile"] = "unknown" }},
		{"legacy bridge endpoint is not the ingress endpoint", "chat/completions", `{}`, "", func(m *ProviderModel) {
			delete(m.Metadata, "request_parameter_profile")
			m.Metadata["endpoints"] = "responses"
		}},
		{"Responses stream options", "responses", `{"stream":true,"stream_options":{"include_obfuscation":false}}`, "", nil},
		{"no implicit profile for legacy inventory", "responses", `{"truncation":"disabled"}`, "parameter_not_declared", func(m *ProviderModel) { delete(m.Metadata, "request_parameter_profile") }},
		{"deny overrides protocol baseline", "responses", `{"service_tier":"auto"}`, "parameter_explicitly_denied", func(m *ProviderModel) { m.Metadata["request_parameter_denied"] = "service_tier" }},
		{"deny list comma spaces", "responses", `{"service_tier":"priority"}`, "parameter_explicitly_denied", func(m *ProviderModel) { m.Metadata["request_parameter_denied"] = "temperature, service_tier" }},
		{"deny list tabs and empty entries", "responses", `{"service_tier":"priority"}`, "parameter_explicitly_denied", func(m *ProviderModel) { m.Metadata["request_parameter_denied"] = " , temperature, \tservice_tier\n, " }},
		{"effort list comma spaces", "responses", `{"reasoning":{"effort":"low"}}`, "", func(m *ProviderModel) { m.Metadata["reasoning_effort_options"] = " none, low, medium " }},
		{"request effort whitespace remains invalid", "responses", `{"reasoning":{"effort":" low "}}`, "reasoning_effort_unsupported", nil},
		{"legacy declared field still denied with spaces", "responses", `{"temperature":0.5}`, "parameter_explicitly_denied", func(m *ProviderModel) {
			delete(m.Metadata, "request_parameter_profile")
			m.Metadata["request_parameter_denied"] = "service_tier, temperature "
		}},
		{"plain text config needs no structured capability", "responses", `{"text":{"verbosity":"low"}}`, "", func(m *ProviderModel) { m.SupportedParameters = nil }},
		{"schema requires structured capability", "responses", `{"text":{"format":{"type":"json_schema"}}}`, "capability_missing", func(m *ProviderModel) { m.SupportedParameters = nil }},
		{"probability include follows effort", "responses", `{"include":["message.output_text.logprobs"]}`, "sampling_reasoning_conflict", nil},
		{"probability include none", "responses", `{"include":["message.output_text.logprobs"],"reasoning":{"effort":"none"}}`, "", nil},
		{"encrypted reasoning requires capability", "responses", `{"include":["reasoning.encrypted_content"]}`, "capability_missing", func(m *ProviderModel) { m.SupportedParameters = nil }},
		{"null is absent", "responses", `{"unknown":null}`, "", nil},
		{"false is present", "responses", `{"unknown":false}`, "parameter_not_declared", nil},
		{"declared provider extension", "responses", `{"vendor_feature":true}`, "", func(m *ProviderModel) { m.SupportedParameters = append(m.SupportedParameters, "vendor_feature") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Metadata = cloneStringMap(base.Metadata)
			m.SupportedParameters = append([]string(nil), base.SupportedParameters...)
			if tc.change != nil {
				tc.change(&m)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatal(err)
			}
			if result := jevCheckRequest(m, tc.endpoint, raw, 10, 0, "task"); result.Reason != tc.reason {
				t.Fatalf("reason=%q want=%q", result.Reason, tc.reason)
			}
		})
	}
}
