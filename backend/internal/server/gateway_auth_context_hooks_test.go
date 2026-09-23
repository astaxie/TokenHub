package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAuthContextHookAddsMetadataForDownstreamHooks(t *testing.T) {
	server := New(NewMemoryStore())
	authHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-auth",
		HookID:        "tenant-context",
		Stage:         pluginmeta.StageAuthContext,
		Priority:      1000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(authHook); err != nil {
		t.Fatalf("register auth context hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(authHook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return authContextPatchResult(t, map[string]any{
			"project_id": "proj_plugin_test",
			"api_key_id": "key_plugin_test",
			"model":      "gpt-test",
			"stream":     false,
			"metadata":   map[string]any{"tenant": "gold"},
		}), nil
	})); err != nil {
		t.Fatalf("register auth context handler: %v", err)
	}
	admissionHook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "metadata-reader",
		Stage:         pluginmeta.StageAdmission,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(admissionHook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	sawTenant := false
	if err := server.gatewayHooks.RegisterHandler(admissionHook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var authContext struct {
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(input.Data[pluginmeta.DataAuthContext], &authContext); err != nil {
			t.Fatalf("decode auth context: %v", err)
		}
		sawTenant = authContext.Metadata["tenant"] == "gold"
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	call := gatewayPluginTestCall()
	if err := server.runGatewayAuthContextHooks(context.Background(), &call, http.Header{}); err != nil {
		t.Fatalf("run auth context hook: %v", err)
	}
	if string(call.GatewayAuthMetadata["tenant"]) != `"gold"` {
		t.Fatalf("auth metadata tenant = %s, want \"gold\"", call.GatewayAuthMetadata["tenant"])
	}
	if err := server.runGatewayAdmissionHooks(context.Background(), call, http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, 0); err != nil {
		t.Fatalf("run admission hook: %v", err)
	}
	if !sawTenant {
		t.Fatal("downstream admission hook did not receive auth metadata")
	}
}

func TestAuthContextHookCannotChangeCoreAuthenticationFacts(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-auth",
		HookID:        "unsafe-context",
		Stage:         pluginmeta.StageAuthContext,
		Priority:      1000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register auth context hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return authContextPatchResult(t, map[string]any{
			"project_id": "proj_other",
			"metadata":   map[string]any{"tenant": "gold"},
		}), nil
	})); err != nil {
		t.Fatalf("register auth context handler: %v", err)
	}

	call := gatewayPluginTestCall()
	err := server.runGatewayAuthContextHooks(context.Background(), &call, http.Header{})
	httpErr := AsHTTPError(err)
	if httpErr.Code != "gateway_hook_auth_context_invalid" {
		t.Fatalf("error code = %q, want gateway_hook_auth_context_invalid", httpErr.Code)
	}
	if len(call.GatewayAuthMetadata) != 0 {
		t.Fatalf("auth metadata = %v, want no unsafe metadata applied", call.GatewayAuthMetadata)
	}
}

func authContextPatchResult(t *testing.T, value any) pluginmeta.GatewayHookResult {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return pluginmeta.GatewayHookResult{
		Decision: pluginmeta.HookDecisionContinue,
		Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataAuthContext: {Value: data},
		},
	}
}
