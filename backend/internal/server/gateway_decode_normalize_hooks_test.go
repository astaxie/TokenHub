package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestDecodeNormalizeHookCanRewriteChatRequestBody(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-decode",
		HookID:        "normalize",
		Stage:         pluginmeta.StageDecodeNormalize,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataRequestHeaders},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register decode normalize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available in the decode normalize envelope")
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
				{"role": "user", "content": "[normalized]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register decode normalize handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "raw"}},
	}
	err := server.runGatewayChatDecodeNormalizeHooks(context.Background(), gatewayPluginTestCall(), http.Header{
		"Authorization": []string{"Bearer thk_secret"},
	}, &request)
	if err != nil {
		t.Fatalf("run decode normalize hook: %v", err)
	}
	if got := request.Messages[0].Content; got != "[normalized]" {
		t.Fatalf("message content = %v, want normalized rewrite", got)
	}
}

func TestDecodeNormalizeHookDenyReturnsForbidden(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-decode",
		HookID:        "deny",
		Stage:         pluginmeta.StageDecodeNormalize,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register decode normalize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register decode normalize handler: %v", err)
	}

	request := ChatCompletionRequest{Model: "gpt-test", Messages: []ChatMessage{{Role: "user", Content: "blocked"}}}
	err := server.runGatewayChatDecodeNormalizeHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, &request)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusForbidden || httpErr.Code != "gateway_hook_denied" {
		t.Fatalf("decode normalize hook error = %d/%s, want 403/gateway_hook_denied", httpErr.Status, httpErr.Code)
	}
}

func TestDecodeNormalizeHookRewritesGatewayChatBeforeAdmission(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Decode Normalize App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "decode-normalize-key",
		Allowed: []string{"gpt-decode-normalize"},
		Status:  StatusActive,
	}, "thk_decode_normalize")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_decode_normalize", Name: "Decode Normalize Provider", Type: "decode_normalize_capture", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-decode-normalize", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_decode_normalize", ModelName: "gpt-decode-normalize", ProviderID: provider.ID, ProviderModel: "upstream-decode", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	adapter := &requestTransformCaptureAdapter{}
	server.adapterRegistry.Register("decode_normalize_capture", adapter, AdapterCapabilityChat)

	decodeHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-decode",
		HookID:        "rewrite-before-admission",
		Stage:         pluginmeta.StageDecodeNormalize,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(decodeHook); err != nil {
		t.Fatalf("register decode normalize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(decodeHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-decode-normalize",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[decoded-before-admission]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register decode normalize handler: %v", err)
	}

	admissionHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "inspect-decoded",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(admissionHook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(admissionHook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var body map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &body); err != nil {
			t.Fatalf("decode admission body: %v", err)
		}
		if !strings.Contains(string(input.Data[pluginmeta.DataRequestBody]), "[decoded-before-admission]") {
			t.Fatalf("admission hook did not see decoded body: %#v", body)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-decode-normalize",
		"messages": []map[string]any{
			{"role": "user", "content": "raw"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if got := adapter.seenChat.Messages[0].Content; got != "[decoded-before-admission]" {
		t.Fatalf("provider saw message content = %v, want decode normalize rewrite", got)
	}
}
