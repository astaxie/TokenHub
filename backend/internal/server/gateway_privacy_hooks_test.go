package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPrivacyPreHookUsesProjectAndAPIKeyScopeWithoutExposingMetadata(t *testing.T) {
	server := New(NewMemoryStore())
	call := gatewayPluginTestCall()
	call.Project.ID = "prj_privacy_scope"
	call.Key.ID = "key_privacy_scope"
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-privacy-scope",
		HookID:   "mask",
		Stage:    pluginmeta.StagePrivacyPre,
		Scope: pluginmeta.GatewayHookScope{
			ProjectIDs: []string{"prj_privacy_scope"},
			APIKeyIDs:  []string{"key_privacy_scope"},
		},
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataProjectMetadata]; ok {
			t.Fatal("project metadata leaked to privacy hook data")
		}
		if _, ok := input.Data[pluginmeta.DataAPIKeyMetadata]; ok {
			t.Fatal("API key metadata leaked to privacy hook data")
		}
		if _, ok := input.OriginalData[pluginmeta.DataProjectMetadata]; ok {
			t.Fatal("project metadata leaked to privacy hook original data")
		}
		return rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[scoped-mask]"},
			},
		}), nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "secret"}},
	}
	err := server.runGatewayPrivacyPreHooks(context.Background(), call, http.Header{}, request, func(data json.RawMessage) error {
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
	if got := request.Messages[0].Content; got != "[scoped-mask]" {
		t.Fatalf("message content = %q, want scoped mask", got)
	}
}

func TestPrivacyPreHookSkipsMismatchedProjectScope(t *testing.T) {
	server := New(NewMemoryStore())
	call := gatewayPluginTestCall()
	call.Project.ID = "prj_allowed"
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-privacy-scope",
		HookID:   "skip",
		Stage:    pluginmeta.StagePrivacyPre,
		Scope: pluginmeta.GatewayHookScope{
			ProjectIDs: []string{"prj_other"},
		},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		t.Fatal("mismatched project scoped hook should not run")
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	calledApply := false
	err := server.runGatewayPrivacyPreHooks(context.Background(), call, http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, func(json.RawMessage) error {
		calledApply = true
		return nil
	})
	if err != nil {
		t.Fatalf("run privacy hook: %v", err)
	}
	if calledApply {
		t.Fatal("privacy patch apply was called for a skipped scoped hook")
	}
}

func TestPrivacyPreHookRejectsWritesOutsideDeclaredBoundary(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy-boundary",
		HookID:        "provider-write",
		Stage:         pluginmeta.StagePrivacyPre,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionContinue,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataProviderRequest: {Value: json.RawMessage(`{"api_key":"leaked"}`)},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, func(json.RawMessage) error {
		t.Fatal("invalid privacy write should not apply a request patch")
		return nil
	})
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusInternalServerError || httpErr.Code != "gateway_hook_failed" {
		t.Fatalf("privacy hook error = %d/%s, want 500/gateway_hook_failed", httpErr.Status, httpErr.Code)
	}
	if !strings.Contains(err.Error(), "privacy_pre") {
		t.Fatalf("privacy hook error = %v, want stage context", err)
	}
}

func TestPrivacyPreHookRecordsRedactedAuditEvents(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-privacy-audit",
		HookID:        "audit",
		Stage:         pluginmeta.StagePrivacyPre,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register privacy hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		result := rawRequestBodyPatch(t, map[string]any{
			"model":  "gpt-test",
			"stream": false,
			"messages": []map[string]any{
				{"role": "user", "content": "[masked]"},
			},
		})
		result.AuditEvents = []json.RawMessage{
			json.RawMessage(`{"reason":"pii","authorization":"Bearer secret","nested":{"api_key":"secret-key"}}`),
		}
		return result, nil
	})); err != nil {
		t.Fatalf("register privacy handler: %v", err)
	}

	request := ChatCompletionRequest{
		Model:    "gpt-test",
		Messages: []ChatMessage{{Role: "user", Content: "secret"}},
	}
	if err := server.runGatewayPrivacyPreHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, request, func(data json.RawMessage) error {
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		request = patched
		return nil
	}); err != nil {
		t.Fatalf("run privacy hook: %v", err)
	}

	events := store.ListAuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Action != "plugin.gateway.privacy_pre" || event.ResourceType != "gateway_request" {
		t.Fatalf("audit event = %+v, want privacy gateway audit", event)
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
