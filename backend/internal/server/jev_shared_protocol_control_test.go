package server

import (
	"encoding/json"
	"testing"
)

// TestJevSharedProtocolControlsRequireForwardingAdapter pins the boundary
// between controls a route's adapter forwards verbatim and controls it would
// translate away. Only the first group is admitted without a declaration.
func TestJevSharedProtocolControlsRequireForwardingAdapter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoint  string
		body      string
		metadata  map[string]string
		forwards  bool
		reason    string
		parameter string
	}{
		{name: "native chat admits store", endpoint: "chat/completions", body: `{"messages":[],"store":false}`, forwards: true},
		{name: "native chat admits service tier", endpoint: "chat/completions", body: `{"messages":[],"service_tier":"auto"}`, forwards: true},
		{name: "native chat admits safety identifier", endpoint: "chat/completions", body: `{"messages":[],"safety_identifier":"hash"}`, forwards: true},
		{name: "native chat admits prompt cache retention", endpoint: "chat/completions", body: `{"messages":[],"prompt_cache_retention":"24h"}`, forwards: true},
		{name: "responses store stays structural", endpoint: "responses", body: `{"input":"task","store":false}`},
		{name: "anthropic bridge keeps the gate", endpoint: "chat/completions", body: `{"messages":[],"service_tier":"auto"}`, metadata: map[string]string{"endpoints": "anthropic"}, forwards: true, reason: "parameter_not_declared", parameter: "service_tier"},
		{name: "gemini bridge keeps the gate", endpoint: "chat/completions", body: `{"messages":[],"safety_identifier":"hash"}`, metadata: map[string]string{"endpoints": "gemini"}, forwards: true, reason: "parameter_not_declared", parameter: "safety_identifier"},
		{name: "non forwarding adapter keeps the gate", endpoint: "chat/completions", body: `{"messages":[],"store":false}`, forwards: false, reason: "parameter_not_declared", parameter: "store"},
		{name: "legacy route without endpoints keeps the gate", endpoint: "chat/completions", body: `{"messages":[],"store":false}`, metadata: map[string]string{"endpoints": ""}, forwards: true, reason: "parameter_not_declared", parameter: "store"},
		{name: "deny list still wins", endpoint: "chat/completions", body: `{"messages":[],"store":false}`, metadata: map[string]string{"request_parameter_denied": "store"}, forwards: true, reason: "parameter_explicitly_denied", parameter: "store"},
		{name: "undeclared sampling field still rejected", endpoint: "chat/completions", body: `{"messages":[],"top_p":0.5}`, forwards: true, reason: "parameter_not_declared", parameter: "top_p"},
		{name: "endpoint specific control still needs a profile", endpoint: "responses", body: `{"input":"task","truncation":"disabled"}`, forwards: true, reason: "parameter_not_declared", parameter: "truncation"},
		{name: "profile admits endpoint specific controls", endpoint: "responses", body: `{"input":"task","truncation":"disabled"}`, metadata: map[string]string{"request_parameter_profile": jevOpenAIProfile, "endpoints": "responses"}, forwards: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := ProviderModel{
				Status:              StatusActive,
				SupportedParameters: []string{"max_tokens", "tools"},
				Metadata:            map[string]string{"endpoints": "chat/completions"},
			}
			for key, value := range tc.metadata {
				model.Metadata[key] = value
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatal(err)
			}
			result := jevCheckRequestForRoute(model, tc.endpoint, raw, 10, 0, "task", tc.forwards)
			if result.Reason != tc.reason {
				t.Fatalf("reason=%q want=%q", result.Reason, tc.reason)
			}
			if tc.parameter != "" && result.Parameter != tc.parameter {
				t.Fatalf("parameter=%q want=%q", result.Parameter, tc.parameter)
			}
		})
	}
}

// TestForwardsProtocolControlsUsesAdapterCapability checks the exemption is
// derived from the adapter registered for the provider type, not from request
// metadata or model declarations.
func TestForwardsProtocolControlsUsesAdapterCapability(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{})
	server.builtinProviderAdapters = map[string]any{
		ProviderOpenAI:           OpenAICompatibleAdapter{},
		ProviderOpenAICompatible: OpenAICompatibleAdapter{},
		"deepseek":               OpenAICompatibleAdapter{},
		ProviderMock:             MockAdapter{},
		ProviderAnthropic:        AnthropicAdapter{},
		ProviderGemini:           GeminiAdapter{},
		ProviderAzureOpenAI:      AzureOpenAIAdapter{},
	}
	for providerType, want := range map[string]bool{
		ProviderOpenAI:           true,
		ProviderOpenAICompatible: true,
		"deepseek":               true,
		ProviderMock:             true,
		ProviderAnthropic:        false,
		ProviderGemini:           false,
		ProviderAzureOpenAI:      false,
		"trae_cn_subscription":   false,
		"openai_codex":           false,
		"unknown_provider":       false,
	} {
		if got := server.forwardsProtocolControls(providerType); got != want {
			t.Fatalf("%s: got %v want %v", providerType, got, want)
		}
	}
}
