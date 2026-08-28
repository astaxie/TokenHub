package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdmissionHookReceivesRequestContextAndReservation(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "inspect",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAuthContext, pluginmeta.DataAPIKeyMetadata, pluginmeta.DataRequestHeaders, pluginmeta.DataRequestBody, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if input.RequestID == "" {
			t.Fatal("request ID was not available to admission hook")
		}
		if len(input.Envelope.RequestBody) == 0 {
			t.Fatal("request body was not available in the admission envelope")
		}
		if _, ok := input.Data[pluginmeta.DataAuthContext]; !ok {
			t.Fatal("auth context was not available to admission hook")
		}
		if _, ok := input.Data[pluginmeta.DataAPIKeyMetadata]; !ok {
			t.Fatal("API key metadata was not available to admission hook")
		}
		var headers map[string][]string
		if err := json.Unmarshal(input.Data[pluginmeta.DataRequestHeaders], &headers); err != nil {
			t.Fatalf("decode headers: %v", err)
		}
		if got := headers["Authorization"]; len(got) != 1 || got[0] != "[redacted]" {
			t.Fatalf("authorization header = %v, want redacted", got)
		}
		var usage Usage
		if err := json.Unmarshal(input.Data[pluginmeta.DataUsage], &usage); err != nil {
			t.Fatalf("decode reservation: %v", err)
		}
		if usage.TotalTokens != 42 {
			t.Fatalf("reservation total tokens = %d, want 42", usage.TotalTokens)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	err := server.runGatewayAdmissionHooks(context.Background(), gatewayPluginTestCall(), http.Header{
		"Authorization": []string{"Bearer thk_secret"},
	}, ChatCompletionRequest{Model: "gpt-test"}, 42)
	if err != nil {
		t.Fatalf("run admission hook: %v", err)
	}
}

func TestAdmissionHookDenyReturnsForbidden(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "deny",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	err := server.runGatewayAdmissionHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, 0)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusForbidden || httpErr.Code != "gateway_hook_denied" {
		t.Fatalf("admission hook error = %d/%s, want 403/gateway_hook_denied", httpErr.Status, httpErr.Code)
	}
}

func TestAdmissionHookContinueRecordsPluginAuditEvent(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "allow-audit",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision:    pluginmeta.HookDecisionContinue,
			AuditEvents: []json.RawMessage{json.RawMessage(`{"outcome":"allowed","authorization":"Bearer hidden"}`)},
		}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	err := server.runGatewayAdmissionHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, 0)
	if err != nil {
		t.Fatalf("run admission hook: %v", err)
	}

	event := requireGatewayAdmissionAuditEvent(t, store.ListAuditEvents(), "success")
	if !strings.Contains(event.AfterSnapshot, `"authorization":"[redacted]"`) {
		t.Fatalf("admission audit snapshot did not redact authorization: %s", event.AfterSnapshot)
	}
}

func TestAdmissionHookDenyRecordsPluginAuditEvent(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "deny-audit",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision:    pluginmeta.HookDecisionDeny,
			AuditEvents: []json.RawMessage{json.RawMessage(`{"outcome":"denied","reason":"blocked_model"}`)},
		}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	err := server.runGatewayAdmissionHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, 0)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusForbidden || httpErr.Code != "gateway_hook_denied" {
		t.Fatalf("admission hook error = %d/%s, want 403/gateway_hook_denied", httpErr.Status, httpErr.Code)
	}
	requireGatewayAdmissionAuditEvent(t, store.ListAuditEvents(), "denied")
}

func TestAdmissionHookScopeMismatchDoesNotRecordPluginAuditEvent(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "scoped-audit",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		Scope:         pluginmeta.GatewayHookScope{ProjectIDs: []string{"prj_other"}},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	called := false
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		return pluginmeta.GatewayHookResult{
			AuditEvents: []json.RawMessage{json.RawMessage(`{"outcome":"should_not_record"}`)},
		}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	err := server.runGatewayAdmissionHooks(context.Background(), gatewayPluginTestCall(), http.Header{}, ChatCompletionRequest{Model: "gpt-test"}, 0)
	if err != nil {
		t.Fatalf("run admission hook: %v", err)
	}
	if called {
		t.Fatal("scoped admission hook ran for a non-matching project")
	}
	for _, event := range store.ListAuditEvents() {
		if event.Action == "plugin.gateway.admission" {
			t.Fatalf("scope-mismatched admission hook recorded audit event: %+v", event)
		}
	}
}

func TestAdmissionHookDenyStopsGatewayChatBeforeProvider(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Admission Plugin App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "admission-plugin-key",
		Allowed: []string{"gpt-admission-plugin"},
		Status:  StatusActive,
	}, "thk_admission_plugin")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_admission_plugin", Name: "Admission Plugin Provider", Type: "admission_capture", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-admission-plugin", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_admission_plugin", ModelName: "gpt-admission-plugin", ProviderID: provider.ID, ProviderModel: "upstream-admission", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	adapter := &requestTransformCaptureAdapter{}
	server.adapterRegistry.Register("admission_capture", adapter, AdapterCapabilityChat)

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-admission",
		HookID:        "deny-chat",
		Stage:         pluginmeta.StageAdmission,
		Priority:      2000,
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register admission hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatalf("register admission handler: %v", err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-admission-plugin",
		"messages": []map[string]any{
			{"role": "user", "content": "blocked by admission"},
		},
	}, secret)
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body, "gateway_hook_denied") {
		t.Fatalf("expected 403 gateway_hook_denied, got %d: %s", resp.Code, resp.Body)
	}
	if adapter.seenProviderID != "" {
		t.Fatalf("adapter was invoked for provider %q after admission denial", adapter.seenProviderID)
	}
}

func requireGatewayAdmissionAuditEvent(t *testing.T, events []AuditEvent, status string) AuditEvent {
	t.Helper()
	for _, event := range events {
		if event.Action == "plugin.gateway.admission" && event.Status == status {
			if event.ResourceType != "gateway_request" {
				t.Fatalf("admission audit resource type = %q, want gateway_request", event.ResourceType)
			}
			if !strings.Contains(event.AfterSnapshot, `"stage":"admission"`) {
				t.Fatalf("admission audit snapshot missing stage: %s", event.AfterSnapshot)
			}
			return event
		}
	}
	t.Fatalf("missing admission audit event with status %s: %+v", status, events)
	return AuditEvent{}
}
