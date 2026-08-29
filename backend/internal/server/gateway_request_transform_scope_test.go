package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRequestTransformHookRequiresMatchingRouteScope(t *testing.T) {
	server := New(NewMemoryStore())
	imageHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "image-only",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      1000,
		Subject:       "transform_capture",
		Metadata:      map[string]string{"protocol": providerRouteProtocolImageGeneration},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	chatHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "chat-only",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Subject:       "transform_capture",
		Metadata:      map[string]string{"route_protocol": providerRouteProtocolChatCompletions},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{imageHook, chatHook} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register request transform hook: %v", err)
		}
	}
	imageCalled := false
	if err := server.gatewayHooks.RegisterHandler(imageHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		imageCalled = true
		return rawProviderRequestPatch(t, map[string]any{
			"model":    "gpt-test",
			"messages": []map[string]any{{"role": "user", "content": "[wrong-scope]"}},
		}), nil
	})); err != nil {
		t.Fatalf("register image request transform handler: %v", err)
	}
	chatCalled := false
	if err := server.gatewayHooks.RegisterHandler(chatHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		chatCalled = true
		return rawProviderRequestPatch(t, map[string]any{
			"model":    "gpt-test",
			"messages": []map[string]any{{"role": "user", "content": "[chat-scoped]"}},
		}), nil
	})); err != nil {
		t.Fatalf("register chat request transform handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "original"}},
	}
	err := server.runGatewayChatRequestTransformHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{
		Provider: Provider{ID: "prv_transform", Type: "transform_capture"},
	}, &request)
	if err != nil {
		t.Fatalf("run request transform hook: %v", err)
	}
	if imageCalled || !chatCalled {
		t.Fatalf("request transform route scope called image=%t chat=%t", imageCalled, chatCalled)
	}
	if got := request.Messages[0].Content; got != "[chat-scoped]" {
		t.Fatalf("message content = %v, want chat-scoped", got)
	}
}

func TestRequestTransformFailoverPreservesOriginalChatRequestPerAttempt(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Request Transform Failover Isolation App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "request-transform-failover-key",
		Allowed: []string{"gpt-transform-failover"},
		Status:  StatusActive,
	}, "thk_request_transform_failover")
	if err != nil {
		t.Fatal(err)
	}
	firstProvider := store.AddProvider(Provider{ID: "prv_transform_failover_a", Name: "Transform Failover A", Type: requestTransformFailoverCaptureType, Status: StatusActive, Healthy: true, Priority: 1})
	secondProvider := store.AddProvider(Provider{ID: "prv_transform_failover_b", Name: "Transform Failover B", Type: requestTransformFailoverCaptureType, Status: StatusActive, Healthy: true, Priority: 1})
	store.AddModel(Model{Name: "gpt-transform-failover", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_transform_failover_a", ModelName: "gpt-transform-failover", ProviderID: firstProvider.ID, ProviderModel: "upstream-a", Status: StatusActive, Priority: 1, Weight: 100})
	store.AddRoute(ModelRoute{ID: "route_transform_failover_b", ModelName: "gpt-transform-failover", ProviderID: secondProvider.ID, ProviderModel: "upstream-b", Status: StatusActive, Priority: 2, Weight: 100})
	server := New(store)
	adapter := &requestTransformFailoverCaptureAdapter{failProviderID: firstProvider.ID}
	server.adapterRegistry.Register(requestTransformFailoverCaptureType, adapter, AdapterCapabilityChat)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "isolate-attempts",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest, pluginmeta.DataProviderCredentials},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	seenHookInputs := map[string]string{}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var credentials map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderCredentials], &credentials); err != nil {
			t.Fatalf("decode provider credentials: %v", err)
		}
		provider, _ := credentials["provider"].(map[string]any)
		providerID, _ := provider["id"].(string)
		var request ChatCompletionRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &request); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		if len(request.Messages) != 1 {
			t.Fatalf("provider request messages = %+v, want one original message", request.Messages)
		}
		content, ok := request.Messages[0].Content.(string)
		if !ok {
			t.Fatalf("provider request content = %#v, want string", request.Messages[0].Content)
		}
		seenHookInputs[providerID] = content
		patchContent := "[first-provider-shaped]"
		if providerID == secondProvider.ID {
			patchContent = "[second-provider-shaped]"
		}
		return rawProviderRequestPatch(t, map[string]any{
			"model":  "gpt-transform-failover",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": patchContent},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-transform-failover",
		"messages": []map[string]any{
			{"role": "user", "content": "original"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 after failover, got %d: %s", resp.Code, resp.Body)
	}
	if got := seenHookInputs[firstProvider.ID]; got != "original" {
		t.Fatalf("first transform hook input = %q, want original", got)
	}
	if got := seenHookInputs[secondProvider.ID]; got != "original" {
		t.Fatalf("second transform hook input = %q, want original", got)
	}
	if len(adapter.seenAttempts) != 2 {
		t.Fatalf("seen attempts = %+v, want two provider attempts", adapter.seenAttempts)
	}
	if got := adapter.seenAttempts[0].Message; got != "[first-provider-shaped]" {
		t.Fatalf("first upstream message = %q, want first-provider-shaped", got)
	}
	if got := adapter.seenAttempts[1].Message; got != "[second-provider-shaped]" {
		t.Fatalf("second upstream message = %q, want second-provider-shaped", got)
	}
}

func TestRequestTransformFailOpenUsesOriginalChatRequest(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Request Transform Fail Open App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "request-transform-fail-open-key",
		Allowed: []string{"gpt-transform-open"},
		Status:  StatusActive,
	}, "thk_request_transform_open")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_transform_open", Name: "Transform Open", Type: requestTransformFailoverCaptureType, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-transform-open", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_transform_open", ModelName: "gpt-transform-open", ProviderID: provider.ID, ProviderModel: "upstream-open", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	adapter := &requestTransformFailoverCaptureAdapter{}
	server.adapterRegistry.Register(requestTransformFailoverCaptureType, adapter, AdapterCapabilityChat)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-transform",
		HookID:        "open",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, NewHTTPError(http.StatusBadGateway, "plugin_transform_unavailable", "transform unavailable")
	})); err != nil {
		t.Fatalf("register request transform handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-transform-open",
		"messages": []map[string]any{
			{"role": "user", "content": "original"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if len(adapter.seenAttempts) != 1 || adapter.seenAttempts[0].ProviderID != provider.ID {
		t.Fatalf("seen attempts = %+v, want provider %q", adapter.seenAttempts, provider.ID)
	}
	if got := adapter.seenAttempts[0].Message; got != "original" {
		t.Fatalf("upstream chat request = %q, want original", got)
	}
}

const requestTransformFailoverCaptureType = "request_transform_failover_capture"

type requestTransformFailoverCaptureAdapter struct {
	MockAdapter
	failProviderID string
	seenAttempts   []requestTransformFailoverAttempt
}

func (a *requestTransformFailoverCaptureAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	attempt := requestTransformFailoverAttempt{ProviderID: provider.ID}
	if len(req.Messages) > 0 {
		if content, ok := req.Messages[0].Content.(string); ok {
			attempt.Message = content
		}
	}
	a.seenAttempts = append(a.seenAttempts, attempt)
	if a.failProviderID != "" && provider.ID == a.failProviderID {
		return nil, Usage{}, NewHTTPError(http.StatusServiceUnavailable, "provider_upstream_unavailable", "upstream unavailable")
	}
	return a.MockAdapter.Chat(ctx, provider, providerModel, req)
}

type requestTransformFailoverAttempt struct {
	ProviderID string
	Message    string
}
