package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPrivacyPreHookCanRewriteChatRequestBody(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy",
		HookID:        "mask",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataRequestHeaders},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available in the hook envelope")
		}
		var headers map[string][]string
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestHeaders], &headers); err != nil {
			t.Fatalf("decode headers: %v", err)
		}
		if got := headers["Authorization"]; len(got) != 1 || got[0] != "[redacted]" {
			t.Fatalf("authorization header = %v, want redacted", got)
		}
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[masked]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "secret"}},
	}
	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{
		"Authorization": []string{"Bearer thk_secret"},
	}, request, func(data json.RawMessage) error {
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(request.Model, request.Stream, patched.Model, patched.Stream); err != nil {
			return err
		}
		request = patched
		return nil
	})
	if err != nil {
		t.Fatalf("run privacy hook: %v", err)
	}
	if got := request.Messages[0].Content; got != "[masked]" {
		t.Fatalf("message content = %v, want masked", got)
	}
}

func TestPrivacyPreHookCannotChangeRequestedModel(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy",
		HookID:        "reroute",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-other",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "secret"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "secret"}},
	}
	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, request, func(data json.RawMessage) error {
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		return validateGatewayHookRequestInvariant(request.Model, request.Stream, patched.Model, patched.Stream)
	})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_patch_invalid" {
		t.Fatalf("privacy hook error = %d/%s, want 502/gateway_hook_patch_invalid", httpErr.Status, httpErr.Code)
	}
}

func TestPrivacyPreHookDenyReturnsForbidden(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy",
		HookID:        "deny",
		Stage:         pluginmeta.StagePrivacyPre,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, func(json.RawMessage) error {
		t.Fatal("deny decision should not apply a request patch")
		return nil
	})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusForbidden || httpErr.Code != "gateway_hook_denied" {
		t.Fatalf("privacy hook error = %d/%s, want 403/gateway_hook_denied", httpErr.Status, httpErr.Code)
	}
}

func TestContextOptimizeHookCanRewriteChatRequestBody(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-context",
		HookID:        "trim",
		Stage:         pluginmeta.StageContextOptimize,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register context optimize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available in the context optimize envelope")
		}
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[optimized]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register context optimize handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "verbose context"}},
	}
	err := server.runGatewayContextOptimizeHooks(context.Background(), gatewayPluginTestCall(), request, func(data json.RawMessage) error {
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(request.Model, request.Stream, patched.Model, patched.Stream); err != nil {
			return err
		}
		request = patched
		return nil
	})
	if err != nil {
		t.Fatalf("run context optimize hook: %v", err)
	}
	if got := request.Messages[0].Content; got != "[optimized]" {
		t.Fatalf("message content = %v, want optimized", got)
	}
}

func TestContextOptimizeHookRewritesGatewayChatRequestBeforeUpstream(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Context Plugin App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "context-plugin-key",
		Allowed: []string{"gpt-context"},
		Status:  StatusActive,
	}, "thk_context_plugin")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_context_plugin", Name: "Context Plugin", Type: "context_capture", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-context", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_context_plugin", ModelName: "gpt-context", ProviderID: provider.ID, ProviderModel: "upstream-context", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	adapter := &contextOptimizeCaptureAdapter{}
	registerTestAdapter(server, "context_capture", adapter)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-context",
		HookID:        "trim",
		Stage:         pluginmeta.StageContextOptimize,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register context optimize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-context",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[optimized]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register context optimize handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-context",
		"messages": []map[string]any{
			{"role": "user", "content": "verbose context"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if len(adapter.seenChat.Messages) != 1 || adapter.seenChat.Messages[0].Content != "[optimized]" {
		t.Fatalf("upstream chat request was not optimized: %+v", adapter.seenChat)
	}
}

func TestRequestTransformHookCanRewriteChatProviderRequest(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "shape",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest, pluginmeta.DataProviderCredentials},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("provider request was not available in the hook envelope")
		}
		var credentials map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderCredentials], &credentials); err != nil {
			t.Fatalf("decode provider credentials: %v", err)
		}
		provider, ok := credentials["provider"].(map[string]any)
		if !ok || provider["api_key"] != "provider-secret" {
			t.Fatalf("provider credentials = %#v, want provider api key", credentials)
		}
		return rawProviderRequestPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[provider-shaped]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "original"}},
	}
	err := server.runGatewayChatRequestTransformHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Provider: Provider{ID: "prv_transform", Type: "transform_capture", APIKey: "provider-secret"},
	}, &request)
	if err != nil {
		t.Fatalf("run request transform hook: %v", err)
	}
	if got := request.Messages[0].Content; got != "[provider-shaped]" {
		t.Fatalf("message content = %v, want provider-shaped", got)
	}
}

func TestRequestTransformHookCannotChangeRequestedModel(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "reroute",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderRequestPatch(t, map[string]any{
			"model":  "gpt-other",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "original"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "original"}},
	}
	err := server.runGatewayChatRequestTransformHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, &request)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_patch_invalid" {
		t.Fatalf("request transform error = %d/%s, want 502/gateway_hook_patch_invalid", httpErr.Status, httpErr.Code)
	}
}

func TestRequestTransformHookCanSkipRouteAndRewriteNextChatAttempt(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Request Transform App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "request-transform-key",
		Allowed: []string{"gpt-transform"},
		Status:  StatusActive,
	}, "thk_request_transform")
	if err != nil {
		t.Fatal(err)
	}
	firstProvider := store.AddProvider(Provider{ID: "prv_transform_a", Name: "Transform A", Type: "transform_capture", Status: StatusActive, Healthy: true, Priority: 1})
	secondProvider := store.AddProvider(Provider{ID: "prv_transform_b", Name: "Transform B", Type: "transform_capture", Status: StatusActive, Healthy: true, Priority: 1})
	store.AddModel(Model{Name: "gpt-transform", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_transform_a", ModelName: "gpt-transform", ProviderID: firstProvider.ID, ProviderModel: "upstream-a", Status: StatusActive, Priority: 1, Weight: 100})
	store.AddRoute(ModelRoute{ID: "route_transform_b", ModelName: "gpt-transform", ProviderID: secondProvider.ID, ProviderModel: "upstream-b", Status: StatusActive, Priority: 2, Weight: 100})
	server := New(store)
	adapter := &requestTransformCaptureAdapter{}
	server.adapterRegistry.Register("transform_capture", adapter, AdapterCapabilityChat, AdapterCapabilityResponses)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-transform",
		HookID:   "skip-and-shape",
		Stage:    pluginmeta.StageRequestTransform,
		Priority: 2000,
		Reads:    []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest, pluginmeta.DataProviderCredentials},
		Writes:   []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var credentials map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderCredentials], &credentials); err != nil {
			t.Fatalf("decode provider credentials: %v", err)
		}
		provider, _ := credentials["provider"].(map[string]any)
		if provider["id"] == "prv_transform_a" {
			return pluginmeta.GatewayHookResult{}, NewHTTPError(http.StatusBadGateway, "plugin_route_rejected", "skip this provider")
		}
		return rawProviderRequestPatch(t, map[string]any{
			"model":  "gpt-transform",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[second-provider-shaped]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-transform",
		"messages": []map[string]any{
			{"role": "user", "content": "original"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenProviderID != "prv_transform_b" {
		t.Fatalf("provider ID = %q, want prv_transform_b", adapter.seenProviderID)
	}
	if len(adapter.seenChat.Messages) != 1 || adapter.seenChat.Messages[0].Content != "[second-provider-shaped]" {
		t.Fatalf("upstream chat request was not transformed: %+v", adapter.seenChat)
	}
}

func TestRequestTransformFailClosedStopsRouteFailover(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Request Transform Fail Closed App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "request-transform-fail-closed-key",
		Allowed: []string{"gpt-transform-closed"},
		Status:  StatusActive,
	}, "thk_request_transform_closed")
	if err != nil {
		t.Fatal(err)
	}
	firstProvider := store.AddProvider(Provider{ID: "prv_transform_closed_a", Name: "Closed A", Type: "transform_capture", Status: StatusActive, Healthy: true, Priority: 1})
	secondProvider := store.AddProvider(Provider{ID: "prv_transform_closed_b", Name: "Closed B", Type: "transform_capture", Status: StatusActive, Healthy: true, Priority: 1})
	store.AddModel(Model{Name: "gpt-transform-closed", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_transform_closed_a", ModelName: "gpt-transform-closed", ProviderID: firstProvider.ID, ProviderModel: "upstream-a", Status: StatusActive, Priority: 1, Weight: 100})
	store.AddRoute(ModelRoute{ID: "route_transform_closed_b", ModelName: "gpt-transform-closed", ProviderID: secondProvider.ID, ProviderModel: "upstream-b", Status: StatusActive, Priority: 2, Weight: 100})
	server := New(store)
	adapter := &requestTransformCaptureAdapter{}
	server.adapterRegistry.Register("transform_capture", adapter, AdapterCapabilityChat, AdapterCapabilityResponses)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "closed",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, NewHTTPError(http.StatusBadGateway, "plugin_failed_closed", "closed")
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-transform-closed",
		"messages": []map[string]any{
			{"role": "user", "content": "original"},
		},
	}, secret)
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenProviderID != "" {
		t.Fatalf("adapter was invoked for provider %q after fail-closed transform", adapter.seenProviderID)
	}
}

func TestRequestTransformHookRewritesGatewayResponsesRequestBeforeUpstream(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Responses Transform App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "responses-transform-key",
		Allowed: []string{"gpt-responses-transform"},
		Status:  StatusActive,
	}, "thk_responses_transform")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_responses_transform", Name: "Responses Transform", Type: "transform_capture", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-responses-transform", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_responses_transform", ModelName: "gpt-responses-transform", ProviderID: provider.ID, ProviderModel: "upstream-responses", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	adapter := &requestTransformCaptureAdapter{}
	server.adapterRegistry.Register("transform_capture", adapter, AdapterCapabilityChat, AdapterCapabilityResponses)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "shape-responses",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderRequestPatch(t, map[string]any{
			"model": "gpt-responses-transform",
			"input": "[responses-shaped]",
		}), nil
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "gpt-responses-transform",
		"input": "original",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenResponses.Input != "[responses-shaped]" {
		t.Fatalf("upstream responses request input = %#v, want responses-shaped", adapter.seenResponses.Input)
	}
}

func TestResponsePostHookCanRewriteProviderResponse(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response",
		HookID:        "rewrite",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response post hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("provider response was not available in the hook envelope")
		}
		if _, ok := input.Data[pluginmeta.DataProviderResponse]; !ok {
			t.Fatal("provider response data was not available to the hook")
		}
		return rawProviderResponsePatch(t, map[string]any{"id": "post_processed"}), nil
	})); err != nil {
		t.Fatalf("register response post handler: %v", err)
	}

	response, err := server.runGatewayResponsePostHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, map[string]any{"id": "upstream"})
	if err != nil {
		t.Fatalf("run response post hook: %v", err)
	}
	body, ok := response.(map[string]any)
	if !ok || body["id"] != "post_processed" {
		t.Fatalf("provider response = %#v, want post_processed", response)
	}
}

func TestResponsePostHookRewritesGatewayChatResponseBeforeClient(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Post Chat App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-post-chat-key",
		Allowed: []string{"gpt-response-post"},
		Status:  StatusActive,
	}, "thk_response_post_chat")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_response_post_chat", Name: "Response Post Chat", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-response-post", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_response_post_chat", ModelName: "gpt-response-post", ProviderID: provider.ID, ProviderModel: "upstream-chat", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response",
		HookID:        "shape-chat",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response post hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderResponsePatch(t, map[string]any{
			"id":      "chat_post_processed",
			"object":  "chat.completion",
			"created": float64(1),
			"model":   "gpt-response-post",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "[post-processed]",
				},
				"finish_reason": "stop",
			}},
		}), nil
	})); err != nil {
		t.Fatalf("register response post handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-response-post",
		"messages": []map[string]any{
			{"role": "user", "content": "original"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "[post-processed]") || !strings.Contains(resp.Body, "chat_post_processed") {
		t.Fatalf("response body was not post processed: %s", resp.Body)
	}
}

func TestResponsePostHookRewritesGatewayResponsesResponseBeforeClient(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Post Responses App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-post-responses-key",
		Allowed: []string{"gpt-response-post-responses"},
		Status:  StatusActive,
	}, "thk_response_post_responses")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_response_post_responses", Name: "Response Post Responses", Type: "transform_capture", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-response-post-responses", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_response_post_responses", ModelName: "gpt-response-post-responses", ProviderID: provider.ID, ProviderModel: "upstream-responses", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	server.adapterRegistry.Register("transform_capture", &requestTransformCaptureAdapter{}, AdapterCapabilityChat, AdapterCapabilityResponses)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response",
		HookID:        "shape-responses",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register response post hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderResponsePatch(t, map[string]any{
			"id":          "resp_post_processed",
			"object":      "response",
			"created_at":  float64(1),
			"model":       "gpt-response-post-responses",
			"output_text": "[responses-post-processed]",
		}), nil
	})); err != nil {
		t.Fatalf("register response post handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "gpt-response-post-responses",
		"input": "original",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "[responses-post-processed]") || !strings.Contains(resp.Body, "resp_post_processed") {
		t.Fatalf("response body was not post processed: %s", resp.Body)
	}
}

func TestUsageAttributionHookCanRewriteUsage(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-usage",
		HookID:        "attribute",
		Stage:         pluginmeta.StageUsageAttribution,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataUsage, pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register usage attribution hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataProviderResponse]; !ok {
			t.Fatal("provider response data was not available to the hook")
		}
		var usage Usage
		if err := json.Unmarshal(input.Data[pluginmeta.DataUsage], &usage); err != nil {
			t.Fatalf("decode usage: %v", err)
		}
		if usage.TotalTokens != 3 {
			t.Fatalf("usage total tokens = %d, want 3", usage.TotalTokens)
		}
		return rawUsagePatch(t, Usage{PromptTokens: 7, CompletionTokens: 11, TotalTokens: 18}), nil
	})); err != nil {
		t.Fatalf("register usage attribution handler: %v", err)
	}

	usage, err := server.runGatewayUsageAttributionHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, map[string]any{"id": "upstream"}, Usage{TotalTokens: 3})
	if err != nil {
		t.Fatalf("run usage attribution hook: %v", err)
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens != 11 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want attributed usage", usage)
	}
}

func TestUsageAttributionHookUpdatesGatewayChatBillingAndAttemptUsage(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Usage Attribution App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "usage-attribution-key",
		Allowed: []string{"gpt-usage"},
		Status:  StatusActive,
	}, "thk_usage_attribution")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_usage", Name: "Usage Attribution", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-usage", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_usage", ModelName: "gpt-usage", ProviderID: provider.ID, ProviderModel: "upstream-usage", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-usage",
		HookID:        "attribute-chat",
		Stage:         pluginmeta.StageUsageAttribution,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataUsage, pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register usage attribution hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawUsagePatch(t, Usage{PromptTokens: 13, CompletionTokens: 17, TotalTokens: 30}), nil
	})); err != nil {
		t.Fatalf("register usage attribution handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-usage",
		"messages": []map[string]any{
			{"role": "user", "content": "short"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	records := store.ListUsageRecords()
	if len(records) != 1 || records[0].InputTokens != 13 || records[0].OutputTokens != 17 || records[0].TotalTokens != 30 {
		t.Fatalf("usage records = %+v, want attributed tokens", records)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatalf("list route attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].InputTokens != 13 || attempts[0].OutputTokens != 17 || attempts[0].TotalTokens != 30 {
		t.Fatalf("route attempts = %+v, want attributed tokens", attempts)
	}
}

func TestCacheLookupHookCanShortCircuitWithProviderResponse(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyReturnFallback,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available to cache lookup")
		}
		response, err := json.Marshal(map[string]any{"id": "cached_response"})
		if err != nil {
			t.Fatal(err)
		}
		usage, err := json.Marshal(Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9})
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionShortCircuit,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataProviderResponse: {Value: response},
				pluginmeta.DataUsage:            {Value: usage},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	response, usage, hit, err := server.runGatewayCacheLookupHooks(context.Background(), gatewayPluginTestCall(), ChatCompletionRequest{Model: "gpt-test"})
	if err != nil {
		t.Fatalf("run cache lookup hook: %v", err)
	}
	if !hit {
		t.Fatal("cache lookup did not report a hit")
	}
	body, ok := response.(map[string]any)
	if !ok || body["id"] != "cached_response" {
		t.Fatalf("cache response = %#v, want cached_response", response)
	}
	if usage.TotalTokens != 9 {
		t.Fatalf("cache usage = %+v, want total tokens 9", usage)
	}
}

func TestCacheLookupShortCircuitRequiresProviderResponse(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "empty",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyReturnFallback,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit}, nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}

	_, _, hit, err := server.runGatewayCacheLookupHooks(context.Background(), gatewayPluginTestCall(), ChatCompletionRequest{Model: "gpt-test"})
	if hit {
		t.Fatal("invalid cache lookup reported a hit")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_cache_hit_invalid" {
		t.Fatalf("cache lookup error = %d/%s, want 502/gateway_hook_cache_hit_invalid", httpErr.Status, httpErr.Code)
	}
}

func TestCacheWriteHookReceivesRequestResponseAndUsage(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-cache",
		HookID:        "write",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register cache write hook: %v", err)
	}
	var sawRequest, sawResponse, sawUsage bool
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		sawRequest = len(input.Data[pluginmeta.DataRequestBody]) > 0
		sawResponse = len(input.Data[pluginmeta.DataProviderResponse]) > 0
		sawUsage = len(input.Data[pluginmeta.DataUsage]) > 0
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register cache write handler: %v", err)
	}

	server.runGatewayCacheWriteHooks(context.Background(), gatewayPluginTestCall(), ChatCompletionRequest{Model: "gpt-test"}, map[string]any{"id": "provider_response"}, Usage{TotalTokens: 7})
	if !sawRequest || !sawResponse || !sawUsage {
		t.Fatalf("cache write saw request=%v response=%v usage=%v, want all true", sawRequest, sawResponse, sawUsage)
	}
}

func TestRouteCandidatesHookCanFilterCoreApprovedCandidates(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "select-b",
		Stage:         pluginmeta.StageRouteCandidates,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route candidates hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataRouteCandidates]; !ok {
			t.Fatal("route candidates were not available to the hook")
		}
		return routeRankPatchResult(t, "route_b"), nil
	})); err != nil {
		t.Fatalf("register route candidates handler: %v", err)
	}

	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a"}, Provider: Provider{ID: "prv_a"}},
		{Route: ModelRoute{ID: "route_b"}, Provider: Provider{ID: "prv_b"}},
	}
	selected, err := server.runGatewayRouteCandidatesHooks(context.Background(), gatewayPluginTestCall(), routes)
	if err != nil {
		t.Fatalf("run route candidates hook: %v", err)
	}
	if got := routeIDs(selected); len(got) != 1 || got[0] != "route_b" {
		t.Fatalf("selected route IDs = %v, want [route_b]", got)
	}
}

func TestRouteCandidatesHookCannotAddUnapprovedCandidates(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "unsafe",
		Stage:         pluginmeta.StageRouteCandidates,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route candidates hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return routeRankPatchResult(t, "route_unapproved"), nil
	})); err != nil {
		t.Fatalf("register route candidates handler: %v", err)
	}

	_, err := server.runGatewayRouteCandidatesHooks(context.Background(), gatewayPluginTestCall(), []RouteSelection{
		{Route: ModelRoute{ID: "route_a"}, Provider: Provider{ID: "prv_a"}},
	})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_route_candidates_invalid" {
		t.Fatalf("route candidates error = %d/%s, want 502/gateway_hook_route_candidates_invalid", httpErr.Status, httpErr.Code)
	}
}

func TestRouteCandidatesHookFiltersGatewayChatCandidatesBeforeRouting(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Route Candidates App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "route-candidates-key",
		Allowed: []string{"gpt-candidates"},
		Status:  StatusActive,
	}, "thk_route_candidates")
	if err != nil {
		t.Fatal(err)
	}
	firstProvider := store.AddProvider(Provider{ID: "prv_candidates_a", Name: "Candidates A", Type: "candidate_capture", Status: StatusActive, Healthy: true})
	secondProvider := store.AddProvider(Provider{ID: "prv_candidates_b", Name: "Candidates B", Type: "candidate_capture", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-candidates", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_candidates_a", ModelName: "gpt-candidates", ProviderID: firstProvider.ID, ProviderModel: "upstream-a", Status: StatusActive, Priority: 1, Weight: 100})
	store.AddRoute(ModelRoute{ID: "route_candidates_b", ModelName: "gpt-candidates", ProviderID: secondProvider.ID, ProviderModel: "upstream-b", Status: StatusActive, Priority: 2, Weight: 100})
	server := New(store)
	adapter := &requestTransformCaptureAdapter{}
	server.adapterRegistry.Register("candidate_capture", adapter, AdapterCapabilityChat)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "select-second",
		Stage:         pluginmeta.StageRouteCandidates,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route candidates hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return routeRankPatchResult(t, "route_candidates_b"), nil
	})); err != nil {
		t.Fatalf("register route candidates handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-candidates",
		"messages": []map[string]any{
			{"role": "user", "content": "original"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenProviderID != "prv_candidates_b" {
		t.Fatalf("provider ID = %q, want prv_candidates_b", adapter.seenProviderID)
	}
}

func TestRouteRankHookCanReorderCoreApprovedCandidates(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "reverse",
		Stage:         pluginmeta.StageRouteRank,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route rank hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataRouteCandidates]; !ok {
			t.Fatal("route candidates were not available to the hook")
		}
		return routeRankPatchResult(t, "route_b", "route_a"), nil
	})); err != nil {
		t.Fatalf("register route rank handler: %v", err)
	}

	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
		{Route: ModelRoute{ID: "route_b", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
	}
	planned := server.planRouteOrder(CallContext{RequestID: "req_route_rank"}, routes)
	if got := routeIDs(planned); len(got) != 2 || got[0] != "route_b" || got[1] != "route_a" {
		t.Fatalf("planned route IDs = %v, want [route_b route_a]", got)
	}
}

func TestRouteRankHookCannotAddUnapprovedCandidates(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-router",
		HookID:        "unsafe",
		Stage:         pluginmeta.StageRouteRank,
		Priority:      2000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register route rank hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return routeRankPatchResult(t, "route_b", "route_unapproved"), nil
	})); err != nil {
		t.Fatalf("register route rank handler: %v", err)
	}

	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_a", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
		{Route: ModelRoute{ID: "route_b", Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly}},
	}
	planned := server.planRouteOrder(CallContext{RequestID: "req_route_rank_unsafe"}, routes)
	if got := routeIDs(planned); len(got) != 2 || got[0] != "route_a" || got[1] != "route_b" {
		t.Fatalf("planned route IDs = %v, want safe baseline [route_a route_b]", got)
	}
}

func rawRequestBodyPatch(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataRequestBody: {Value: data},
		},
	}
}

func rawProviderRequestPatch(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataProviderRequest: {Value: data},
		},
	}
}

func rawProviderResponsePatch(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataProviderResponse: {Value: data},
		},
	}
}

func rawUsagePatch(t *testing.T, value Usage) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataUsage: {Value: data},
		},
	}
}

func routeRankPatchResult(t *testing.T, routeIDs ...string) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(map[string]any{"route_ids": routeIDs})
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataRouteCandidates: {Value: data},
		},
	}
}

func routeIDs(routes []RouteSelection) []string {
	ids := make([]string, 0, len(routes))
	for _, route := range routes {
		ids = append(ids, route.Route.ID)
	}
	return ids
}

func gatewayPluginTestCall() CallContext {
	return CallContext{
		RequestID: "req_plugin_test",
		Project:   Project{ID: "proj_plugin_test", Status: StatusActive},
		Key:       APIKey{ID: "key_plugin_test", ProjectID: "proj_plugin_test", Status: StatusActive},
		Model:     Model{Name: "gpt-test"},
	}
}

type contextOptimizeCaptureAdapter struct {
	MockAdapter
	seenChat ChatCompletionRequest
}

func (a *contextOptimizeCaptureAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.seenChat = req
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}

type requestTransformCaptureAdapter struct {
	MockAdapter
	seenProviderID string
	seenChat       ChatCompletionRequest
	seenResponses  ResponsesRequest
}

func (a *requestTransformCaptureAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	a.seenProviderID = provider.ID
	a.seenChat = req
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}

func (a *requestTransformCaptureAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	a.seenProviderID = provider.ID
	a.seenResponses = req
	return a.MockAdapter.Responses(ctx, provider, providerModel, req)
}
