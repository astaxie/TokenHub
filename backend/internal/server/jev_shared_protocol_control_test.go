package server

import (
	"context"
	"encoding/json"
	"net/http"
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
				Metadata:            map[string]string{"endpoints": "chat/completions,responses"},
			}
			for key, value := range tc.metadata {
				model.Metadata[key] = value
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatal(err)
			}
			result := jevCheckAdmission(model, tc.endpoint, raw, 10, 0, "task", tc.forwards).first()
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
	registry := NewAdapterRegistry()
	for _, providerType := range []string{ProviderOpenAI, ProviderOpenAICompatible, "deepseek"} {
		registry.Register(providerType, OpenAICompatibleAdapter{})
	}
	registry.Register(ProviderMock, MockAdapter{})
	registry.Register(ProviderAnthropic, AnthropicAdapter{})
	registry.Register(ProviderGemini, GeminiAdapter{})
	registry.Register(ProviderAzureOpenAI, AzureOpenAIAdapter{})
	server.adapterRegistry = registry
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
	// A plugin may replace a built-in type with an adapter that only translates;
	// the effective registry entry decides, not the original built-in.
	registry.Register(ProviderOpenAICompatible, AnthropicAdapter{})
	if got := server.forwardsProtocolControls(ProviderOpenAICompatible); got {
		t.Fatal("plugin replacement inherited the built-in forwarding exemption")
	}
	// Plugin command adapters are a wrapper type; they never forward controls
	// themselves, so a plugin that claims an OpenAI-compatible type stays gated.
	if _, claims := any(providerPluginAdapter{}).(protocolControlForwarder); claims {
		t.Fatal("plugin adapter wrapper claims protocol control forwarding")
	}
}

// TestJevBoundResponsesAdmissionUsesRouteForwarding pins continuation requests
// to the same adapter capability as the initial request, so a shared control
// accepted once is not rejected when the conversation continues.
func TestJevBoundResponsesAdmissionUsesRouteForwarding(t *testing.T) {
	for _, test := range []struct {
		name         string
		providerType string
		wantRejected bool
	}{
		{name: "native adapter keeps the control", providerType: ProviderOpenAICompatible},
		{name: "anthropic bridge keeps the gate", providerType: ProviderAnthropic, wantRejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			provider := store.AddProvider(Provider{ID: "p_" + test.providerType, Name: "P", Type: test.providerType, Status: StatusActive})
			store.AddProviderModel(ProviderModel{
				ProviderID: provider.ID, UpstreamModel: "model_x", Status: StatusActive,
				SupportedParameters: []string{"max_tokens"},
				Metadata:            map[string]string{"endpoints": "chat/completions,responses"},
			})
			route := store.AddRoute(ModelRoute{
				ID: "route_x", ModelName: "auto-chat", ProviderID: provider.ID,
				ProviderModel: "model_x", Status: StatusActive, Strategy: RouteStrategyJev,
			})
			server := NewWithConfig(store, Config{})
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			selection := RouteSelection{Provider: provider, ProviderModel: "model_x", Route: route}
			request := ResponsesRequest{Model: "auto-chat", Input: "task", ServiceTier: "auto"}
			err := server.validateJevBoundModel(context.Background(), "req_bound", selection, request)
			if test.wantRejected {
				if err == nil || AsHTTPError(err).Code != "jev_previous_route_unavailable" {
					t.Fatalf("expected previous-route rejection, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("shared control rejected on continuation: %v", err)
			}
		})
	}
}

// TestJevResponsesContinuationKeepsSharedControl drives the full HTTP path: a
// shared control accepted on the initial Responses request must stay accepted
// when the conversation continues through previous_response_id.
func TestJevResponsesContinuationKeepsSharedControl(t *testing.T) {
	server, _, _, _ := jevFixture(t)
	store := server.store.(*GormStore)
	for _, model := range store.ListProviderModels() {
		model.Metadata = cloneStringMap(model.Metadata)
		model.Metadata["endpoints"] = "chat/completions,responses"
		if _, err := store.UpdateProviderModel(model.ID, model); err != nil {
			t.Fatal(err)
		}
	}
	hits := jevHTTPUpstreams(t, server, false)
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
	})
	body := jevHTTPBody(true, false)
	body["service_tier"] = "auto"
	first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if first.Code != http.StatusOK {
		t.Fatalf("initial request rejected: %d %s", first.Code, first.Body)
	}
	body["previous_response_id"] = "resp_model_1"
	second := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if second.Code != http.StatusOK {
		t.Fatalf("continuation rejected: %d %s", second.Code, second.Body)
	}
	if hits.Load() != 2 {
		t.Fatalf("continuation did not reach the upstream twice: hits=%d", hits.Load())
	}
}
