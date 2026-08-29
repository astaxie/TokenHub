package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGuardrailPostRunsAfterResponsePostTransformBeforeUsage(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Guardrail Order", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-guardrail-order-key",
		Allowed: []string{"gpt-response-guardrail-order"},
		Status:  StatusActive,
	}, "thk_response_guardrail_order")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_response_guardrail_order", Name: "Guardrail Order", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-response-guardrail-order", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_response_guardrail_order", ModelName: "gpt-response-guardrail-order", ProviderID: provider.ID, ProviderModel: "upstream-chat", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	stages := []pluginmeta.GatewayHookStage{}
	responsePost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-order",
		HookID:        "response-post",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	guardrailPost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-order",
		HookID:        "guardrail-post",
		Stage:         pluginmeta.StageGuardrailPost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	usageAttribution := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-order",
		HookID:        "usage",
		Stage:         pluginmeta.StageUsageAttribution,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{responsePost, guardrailPost, usageAttribution} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", hook.Stage, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(responsePost, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageResponsePost)
		return rawProviderResponsePatch(t, guardrailPostChatResponse("gpt-response-guardrail-order", "response_post transformed")), nil
	})); err != nil {
		t.Fatalf("register response_post handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(guardrailPost, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageGuardrailPost)
		raw := string(input.Data[pluginmeta.DataProviderResponse])
		if !strings.Contains(raw, "response_post transformed") {
			t.Fatalf("guardrail_post saw provider response %s, want response_post output", raw)
		}
		if len(input.Data[pluginmeta.DataUsage]) == 0 {
			t.Fatal("guardrail_post did not receive usage data")
		}
		return rawProviderResponsePatch(t, guardrailPostChatResponse("gpt-response-guardrail-order", "guardrail approved")), nil
	})); err != nil {
		t.Fatalf("register guardrail_post handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(usageAttribution, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageUsageAttribution)
		raw := string(input.Data[pluginmeta.DataProviderResponse])
		if !strings.Contains(raw, "guardrail approved") || strings.Contains(raw, "response_post transformed") {
			t.Fatalf("usage attribution saw provider response %s, want guardrail output", raw)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register usage attribution handler: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-response-guardrail-order",
		"messages": []map[string]any{
			{"role": "user", "content": "guardrail order"},
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("guardrail response = %d %s, want 200", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "guardrail approved") || strings.Contains(response.Body, "response_post transformed") {
		t.Fatalf("response body = %s, want guardrail-approved output only", response.Body)
	}
	want := []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost, pluginmeta.StageUsageAttribution}
	if !serverGatewayStageSlicesEqual(stages, want) {
		t.Fatalf("post-provider stages = %v, want %v", stages, want)
	}
}

func TestGuardrailPostDenyRecordsRedactedAuditEvent(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Guardrail Audit", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-guardrail-audit-key",
		Allowed: []string{"gpt-response-guardrail-audit"},
		Status:  StatusActive,
	}, "thk_response_guardrail_audit")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_response_guardrail_audit", Name: "Guardrail Audit", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-response-guardrail-audit", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_response_guardrail_audit", ModelName: "gpt-response-guardrail-audit", ProviderID: provider.ID, ProviderModel: "upstream-chat", Status: StatusActive, Priority: 1, Weight: 100})
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-audit",
		HookID:        "deny",
		Stage:         pluginmeta.StageGuardrailPost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register guardrail_post hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionDeny,
			AuditEvents: []json.RawMessage{
				json.RawMessage(`{"reason":"unsafe_output","authorization":"Bearer raw-secret","nested":{"refresh_token":"refresh-secret","api_key":"thk_secret"}}`),
			},
		}, nil
	})); err != nil {
		t.Fatalf("register guardrail_post handler: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-response-guardrail-audit",
		"messages": []map[string]any{
			{"role": "user", "content": "deny unsafe output"},
		},
	}, secret)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body, "gateway_hook_denied") {
		t.Fatalf("guardrail deny response = %d %s, want 403 gateway_hook_denied", response.Code, response.Body)
	}
	event := requireGuardrailPostAuditEvent(t, store.ListAuditEvents(), "denied")
	if strings.Contains(event.AfterSnapshot, "raw-secret") || strings.Contains(event.AfterSnapshot, "refresh-secret") || strings.Contains(event.AfterSnapshot, "thk_secret") {
		t.Fatalf("guardrail audit snapshot leaked secret material: %s", event.AfterSnapshot)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(event.AfterSnapshot), &snapshot); err != nil {
		t.Fatalf("decode guardrail audit snapshot: %v; snapshot=%s", err, event.AfterSnapshot)
	}
	pluginEvent, ok := snapshot["plugin_event"].(map[string]any)
	if !ok {
		t.Fatalf("plugin_event = %#v, want object", snapshot["plugin_event"])
	}
	if pluginEvent["authorization"] != "[redacted]" {
		t.Fatalf("authorization = %v, want redacted", pluginEvent["authorization"])
	}
	nested, ok := pluginEvent["nested"].(map[string]any)
	if !ok || nested["refresh_token"] != "[redacted]" || nested["api_key"] != "[redacted]" {
		t.Fatalf("nested plugin_event = %#v, want redacted refresh_token and api_key", pluginEvent["nested"])
	}
}

func TestGuardrailPostRejectsUndeclaredWritesWithoutApplyingPatch(t *testing.T) {
	server := New(NewMemoryStore())
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-policy",
		HookID:        "undeclared-usage-write",
		Stage:         pluginmeta.StageGuardrailPost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register guardrail_post hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		usageData, err := json.Marshal(Usage{TotalTokens: 99})
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{
			Decision: pluginmeta.HookDecisionContinue,
			Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
				pluginmeta.DataUsage: {Value: usageData},
			},
		}, nil
	})); err != nil {
		t.Fatalf("register guardrail_post handler: %v", err)
	}

	_, err := server.runGatewayGuardrailPostHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, map[string]any{"id": "upstream"}, Usage{TotalTokens: 3}, providerRouteProtocolChatCompletions)
	httpErr := AsHTTPError(err)
	if httpErr.Status != http.StatusInternalServerError || httpErr.Code != "gateway_hook_failed" {
		t.Fatalf("guardrail undeclared write error = %d/%s, want 500/gateway_hook_failed", httpErr.Status, httpErr.Code)
	}
}

func TestGuardrailPostRunsOnCacheHitBeforeUsageAttribution(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Response Guardrail Cache Hit", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "response-guardrail-cache-key",
		Allowed: []string{"gpt-response-guardrail-cache"},
		Status:  StatusActive,
	}, "thk_response_guardrail_cache")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-response-guardrail-cache", Modality: "chat", Status: StatusActive})
	server := New(store)
	stages := []pluginmeta.GatewayHookStage{}
	cacheLookup := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-cache",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      1000,
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	responsePost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-cache",
		HookID:        "response-post",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	guardrailPost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-cache",
		HookID:        "guardrail-post",
		Stage:         pluginmeta.StageGuardrailPost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	usageAttribution := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-response-guardrail-cache",
		HookID:        "usage",
		Stage:         pluginmeta.StageUsageAttribution,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{cacheLookup, responsePost, guardrailPost, usageAttribution} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", hook.Stage, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(cacheLookup, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageCacheLookup)
		return rawProviderCallResult(t, guardrailPostChatResponse("gpt-response-guardrail-cache", "cached raw output"), Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}), nil
	})); err != nil {
		t.Fatalf("register cache lookup handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(responsePost, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageResponsePost)
		return rawProviderResponsePatch(t, guardrailPostChatResponse("gpt-response-guardrail-cache", "cached response_post output")), nil
	})); err != nil {
		t.Fatalf("register response_post handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(guardrailPost, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageGuardrailPost)
		raw := string(input.Data[pluginmeta.DataProviderResponse])
		if !strings.Contains(raw, "cached response_post output") {
			t.Fatalf("cache-hit guardrail saw provider response %s, want response_post output", raw)
		}
		return rawProviderResponsePatch(t, guardrailPostChatResponse("gpt-response-guardrail-cache", "cached guardrail approved")), nil
	})); err != nil {
		t.Fatalf("register guardrail_post handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(usageAttribution, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		stages = append(stages, pluginmeta.StageUsageAttribution)
		raw := string(input.Data[pluginmeta.DataProviderResponse])
		if !strings.Contains(raw, "cached guardrail approved") {
			t.Fatalf("cache-hit usage saw provider response %s, want guardrail output", raw)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register usage handler: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-response-guardrail-cache",
		"messages": []map[string]any{
			{"role": "user", "content": "cache hit response guardrail"},
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("cache-hit guardrail response = %d %s, want 200", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "cached guardrail approved") || strings.Contains(response.Body, "cached response_post output") || strings.Contains(response.Body, "cached raw output") {
		t.Fatalf("cache-hit response body = %s, want guardrail output only", response.Body)
	}
	want := []pluginmeta.GatewayHookStage{pluginmeta.StageCacheLookup, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost, pluginmeta.StageUsageAttribution}
	if !serverGatewayStageSlicesEqual(stages, want) {
		t.Fatalf("cache-hit stages = %v, want %v", stages, want)
	}
}

func TestBackgroundResponsesGuardrailPostPersistsApprovedResult(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	responsePost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-background-response-guardrail",
		HookID:        "response-post",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	guardrailPost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-background-response-guardrail",
		HookID:        "guardrail-post",
		Stage:         pluginmeta.StageGuardrailPost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{responsePost, guardrailPost} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register %s hook: %v", hook.Stage, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(responsePost, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderResponsePatch(t, guardrailPostResponseObject("gpt-background", "background response_post output")), nil
	})); err != nil {
		t.Fatalf("register response_post handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(guardrailPost, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		raw := string(input.Data[pluginmeta.DataProviderResponse])
		if !strings.Contains(raw, "background response_post output") {
			t.Fatalf("background guardrail saw response %s, want response_post output", raw)
		}
		if len(input.Data[pluginmeta.DataUsage]) == 0 {
			t.Fatal("background guardrail did not receive usage data")
		}
		return rawProviderResponsePatch(t, guardrailPostResponseObject("gpt-background", "background guardrail approved")), nil
	})); err != nil {
		t.Fatalf("register guardrail_post handler: %v", err)
	}

	id := submitBackgroundResponse(t, server.Handler(), secret, "background unsafe source")
	completed := waitForResponseJobStatus(t, server.Handler(), secret, id, "completed")
	if completed["output_text"] != "background guardrail approved" {
		t.Fatalf("completed response = %#v, want guardrail-approved output", completed)
	}
	_, resultJSON, err := store.LoadResponseJobPayload(id)
	if err != nil {
		t.Fatal(err)
	}
	resultRaw := string(resultJSON)
	if !strings.Contains(resultRaw, "background guardrail approved") ||
		strings.Contains(resultRaw, "background response_post output") ||
		strings.Contains(resultRaw, "background unsafe source") {
		t.Fatalf("persisted result = %s, want guardrail-approved response only", resultRaw)
	}
}

func guardrailPostChatResponse(model string, content string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl_response_guardrail",
		"object":  "chat.completion",
		"created": float64(1),
		"model":   model,
		"choices": []map[string]any{{
			"index": float64(0),
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
	}
}

func guardrailPostResponseObject(model string, outputText string) map[string]any {
	return map[string]any{
		"id":          "resp_response_guardrail",
		"object":      "response",
		"model":       model,
		"output_text": outputText,
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": outputText,
			}},
		}},
	}
}

func requireGuardrailPostAuditEvent(t *testing.T, events []AuditEvent, status string) AuditEvent {
	t.Helper()
	for _, event := range events {
		if event.Action == "plugin.gateway.guardrail_post" && event.Status == status {
			return event
		}
	}
	t.Fatalf("missing guardrail_post audit event with status %q: %+v", status, events)
	return AuditEvent{}
}
