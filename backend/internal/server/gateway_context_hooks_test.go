package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestContextOptimizeHookUsesProjectAndAPIKeyScopeWithoutExposingMetadata(t *testing.T) {
	server := New(NewMemoryStore())
	call := gatewayPluginTestCall()
	call.Project.ID = "prj_context_scope"
	call.Key.ID = "key_context_scope"
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-context-scope",
		HookID:   "trim",
		Stage:    pluginmeta.StageContextOptimize,
		Scope: pluginmeta.GatewayHookScope{
			ProjectIDs: []string{"prj_context_scope"},
			APIKeyIDs:  []string{"key_context_scope"},
		},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register context optimize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataProjectMetadata]; ok {
			t.Fatal("project metadata leaked to context optimize hook data")
		}
		if _, ok := input.Data[pluginmeta.DataAPIKeyMetadata]; ok {
			t.Fatal("API key metadata leaked to context optimize hook data")
		}
		if _, ok := input.OriginalData[pluginmeta.DataProjectMetadata]; ok {
			t.Fatal("project metadata leaked to context optimize hook original data")
		}
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[optimized-scope]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register context optimize handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "verbose context"}},
	}
	err := server.runGatewayChatContextOptimizeHooks(context.Background(), call, &request)
	if err != nil {
		t.Fatalf("run context optimize hook: %v", err)
	}
	if got := request.Messages[0].Content; got != "[optimized-scope]" {
		t.Fatalf("message content = %q, want optimized scope", got)
	}
}

func TestContextOptimizeHookSkipsMismatchedAPIKeyScope(t *testing.T) {
	server := New(NewMemoryStore())
	call := gatewayPluginTestCall()
	call.Key.ID = "key_allowed"
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-context-scope",
		HookID:   "skip",
		Stage:    pluginmeta.StageContextOptimize,
		Scope: pluginmeta.GatewayHookScope{
			APIKeyIDs: []string{"key_other"},
		},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register context optimize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		t.Fatal("mismatched API key scoped hook should not run")
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register context optimize handler: %v", err)
	}

	request := ChatCompletionRequest{Model: "gpt-test"}
	if err := server.runGatewayChatContextOptimizeHooks(context.Background(), call, &request); err != nil {
		t.Fatalf("run context optimize hook: %v", err)
	}
}

func TestContextOptimizeHookCannotChangeRequestedModelOrStream(t *testing.T) {
	tests := []struct {
		name  string
		patch map[string]any
	}{
		{
			name: "model",
			patch: map[string]any{
				"model":  "gpt-other",
				"stream": false,
				"messages": []map[string]any{
					{"role": "user", "content": "original"},
				},
			},
		},
		{
			name: "stream",
			patch: map[string]any{
				"model":  "gpt-test",
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": "original"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := New(NewMemoryStore())
			hook := pluginmeta.GatewayHookDescriptor{
				PluginID:      "tokenhub.test-context-invariant-" + tt.name,
				HookID:        "unsafe",
				Stage:         pluginmeta.StageContextOptimize,
				Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
				FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatalf("register context optimize hook: %v", err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return rawRequestBodyPatch(t, tt.patch), nil
			})); err != nil {
				t.Fatalf("register context optimize handler: %v", err)
			}

			request := ChatCompletionRequest{
				Model:    "gpt-test",
				Messages: []ChatMessage{{Role: "user", Content: "original"}},
			}
			err := server.runGatewayChatContextOptimizeHooks(context.Background(), gatewayPluginTestCall(), &request)
			httpErr := AsHTTPError(err)
			if httpErr.Status != http.StatusBadGateway || httpErr.Code != "gateway_hook_patch_invalid" {
				t.Fatalf("context optimize error = %d/%s, want 502/gateway_hook_patch_invalid", httpErr.Status, httpErr.Code)
			}
		})
	}
}

func TestContextOptimizeHookRejectsWritesOutsideDeclaredBoundary(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-context-boundary",
		HookID:        "provider-write",
		Stage:         pluginmeta.StageContextOptimize,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register context optimize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionContinue,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataProviderRequest: {Value: json.RawMessage(`{"api_key":"leaked"}`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register context optimize handler: %v", err)
	}

	err := server.runGatewayChatContextOptimizeHooks(context.Background(), gatewayPluginTestCall(), &ChatCompletionRequest{Model: "gpt-test"})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusInternalServerError || httpErr.Code != "gateway_hook_failed" {
		t.Fatalf("context optimize error = %d/%s, want 500/gateway_hook_failed", httpErr.Status, httpErr.Code)
	}
	if !strings.Contains(err.Error(), "context_optimize") {
		t.Fatalf("context optimize error = %v, want stage context", err)
	}
}

func TestContextOptimizeHookRecordsRedactedAuditEvents(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-context-audit",
		HookID:        "audit",
		Stage:         pluginmeta.StageContextOptimize,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register context optimize hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		result := rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[optimized-audit]"},
			},
		})
		result.AuditEvents = []json.RawMessage{
			json.RawMessage(`{"reason":"trimmed","authorization":"Bearer secret","nested":{"api_key":"secret-key"}}`),
		}
		return result, nil
	})); err != nil {
		t.Fatalf("register context optimize handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "verbose context"}},
	}
	if err := server.runGatewayChatContextOptimizeHooks(context.Background(), gatewayPluginTestCall(), &request); err != nil {
		t.Fatalf("run context optimize hook: %v", err)
	}

	events := store.ListAuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Action != "plugin.gateway.context_optimize" || event.ResourceType != "gateway_request" {
		t.Fatalf("audit event = %+v, want context optimize gateway audit", event)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(event.AfterSnapshot), &snapshot); err != nil {
		t.Fatalf("decode audit snapshot: %v", err)
	}
	pluginEvent, ok := snapshot["plugin_event"].(map[string]any)
	if !ok {
		t.Fatalf("plugin event = %#v, want object", snapshot["plugin_event"])
	}
	if pluginEvent["authorization"] != "[redacted]" {
		t.Fatalf("authorization = %v, want redacted", pluginEvent["authorization"])
	}
	nested, ok := pluginEvent["nested"].(map[string]any)
	if !ok || nested["api_key"] != "[redacted]" {
		t.Fatalf("nested plugin event = %#v, want redacted api key", pluginEvent["nested"])
	}
}
