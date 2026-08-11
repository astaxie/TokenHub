package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/guardrails"
)

func TestGuardrailsRejectPathologicalDeterministicWorkBeforeRouting(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	expressions := make([]string, 64)
	for index := range expressions {
		expressions[index] = fmt.Sprintf("a{%d,512}b", index+1)
	}
	items := make([]guardrails.DetectionItem, 32)
	for index := range items {
		items[index] = guardrails.DetectionItem{
			Name: "Expensive regex", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionAudit,
			Config: map[string]any{"regex": expressions},
		}
	}
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Pathological workload", DetectionItems: items,
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	usageBefore := len(store.ListUsageRecords())
	response := doGuardrailProtocolRequest(t, New(store).Handler(), "/v1/chat/completions", map[string]any{
		"model":    "gpt-4.1-mini",
		"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("a", 64*1024)}},
	}, "thk_demo_local")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"guardrail_evaluation_budget_exceeded"`) {
		t.Fatalf("expected work-budget rejection, got %d: %s", response.Code, response.Body)
	}
	if usageAfter := len(store.ListUsageRecords()); usageAfter != usageBefore {
		t.Fatalf("work-budget rejection reached provider usage: before=%d after=%d", usageBefore, usageAfter)
	}
}

func TestGuardrailsBlockAllSupportedOutboundProtocolsBeforeRouting(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	usageBefore := len(store.ListUsageRecords())
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Block internal marker",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Marker", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock,
			Config: map[string]any{"keywords": []string{"Project Aurora"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	tests := []struct {
		name              string
		request           func(stream bool) *httptest.ResponseRecorder
		expectedErrorType string
	}{
		{
			name: "chat completions",
			request: func(stream bool) *httptest.ResponseRecorder {
				return doGuardrailProtocolRequest(t, app, "/v1/chat/completions", map[string]any{
					"model": "gpt-4.1-mini", "stream": stream,
					"messages": []any{map[string]any{"role": "user", "content": "Project Aurora"}},
				}, "thk_demo_local")
			},
			expectedErrorType: "guardrail_blocked",
		},
		{
			name: "responses",
			request: func(stream bool) *httptest.ResponseRecorder {
				return doGuardrailProtocolRequest(t, app, "/v1/responses", map[string]any{
					"model": "gpt-4.1-mini", "stream": stream, "input": "Project Aurora",
				}, "thk_demo_local")
			},
			expectedErrorType: "guardrail_blocked",
		},
		{
			name: "anthropic messages",
			request: func(stream bool) *httptest.ResponseRecorder {
				return doAnthropicRequest(t, app, "/v1/messages", map[string]any{
					"model": "gpt-4.1-mini", "max_tokens": 8, "stream": stream,
					"messages": []any{map[string]any{"role": "user", "content": "Project Aurora"}},
				}, "", "thk_demo_local")
			},
			expectedErrorType: "permission_error",
		},
	}

	for _, test := range tests {
		for _, stream := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: "/json", true: "/stream"}[stream], func(t *testing.T) {
				response := test.request(stream)
				assertGuardrailProtocolError(t, response, test.expectedErrorType)
			})
		}
	}

	if usageAfter := len(store.ListUsageRecords()); usageAfter != usageBefore {
		t.Fatalf("blocked requests recorded routed usage: before=%d after=%d", usageBefore, usageAfter)
	}
}

func doGuardrailProtocolRequest(t *testing.T, handler http.Handler, path string, payload any, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func assertGuardrailProtocolError(t *testing.T, response *httptest.ResponseRecorder, expectedType string) {
	t.Helper()
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("content-type"); contentType != "application/json" {
		t.Fatalf("content-type=%q", contentType)
	}
	requestID := response.Header().Get("x-request-id")
	if requestID == "" {
		t.Fatal("missing x-request-id header")
	}
	var payload struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Error     struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Action        string   `json:"action"`
				Categories    []string `json:"categories"`
				ReasonCodes   []string `json:"reason_codes"`
				PolicyMatches []struct {
					PolicyID          string `json:"policy_id"`
					PolicyName        string `json:"policy_name"`
					DetectionItemID   string `json:"detection_item_id"`
					DetectionItemName string `json:"detection_item_name"`
					DetectorType      string `json:"detector_type"`
					Category          string `json:"category"`
					ReasonCode        string `json:"reason_code"`
				} `json:"policy_matches"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
	if payload.Error.Code != "guardrail_blocked" || payload.Error.Type != expectedType || payload.Error.Message != "Request blocked by a content security policy" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
	if payload.Error.Details.Action != "block" || !reflect.DeepEqual(payload.Error.Details.Categories, []string{"pattern"}) || !reflect.DeepEqual(payload.Error.Details.ReasonCodes, []string{"guardrail_pattern_match"}) {
		t.Fatalf("unexpected safe guardrail details: %#v", payload.Error.Details)
	}
	if len(payload.Error.Details.PolicyMatches) != 1 {
		t.Fatalf("policy_matches=%#v", payload.Error.Details.PolicyMatches)
	}
	match := payload.Error.Details.PolicyMatches[0]
	if match.PolicyID == "" || match.DetectionItemID == "" || match.PolicyName != "Block internal marker" || match.DetectionItemName != "Marker" || match.DetectorType != "pattern" || match.Category != "pattern" || match.ReasonCode != "guardrail_pattern_match" {
		t.Fatalf("unexpected safe policy match: %#v", match)
	}
	if payload.RequestID != requestID {
		t.Fatalf("request_id=%q header=%q", payload.RequestID, requestID)
	}
	if expectedType == "permission_error" && payload.Type != "error" {
		t.Fatalf("anthropic response type=%q", payload.Type)
	}
	if strings.Contains(response.Body.String(), "Project Aurora") {
		t.Fatalf("blocked response leaked matching text: %s", response.Body.String())
	}
}

func TestGuardrailsBlockAdminPlaygroundBeforeRouting(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Block phone numbers",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Phone", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionBlock,
			Config: map[string]any{"data_types": []string{"phone"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeProject, ScopeID: "prj_demo"}},
	}); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	expectedRequestIDs := map[string]bool{}
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		t.Run(path, func(t *testing.T) {
			response := doGuardrailProtocolRequest(t, app, path, map[string]any{
				"project_id": "prj_demo",
				"model":      "gpt-4.1-mini",
				"messages":   []any{map[string]any{"role": "user", "content": "call 13312341234"}},
			}, "dev_admin_token")
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"guardrail_blocked"`) || !strings.Contains(response.Body.String(), `"categories":["phone"]`) || !strings.Contains(response.Body.String(), `"policy_name":"Block phone numbers"`) || !strings.Contains(response.Body.String(), `"detection_item_name":"Phone"`) {
				t.Fatalf("expected playground request to be blocked, got %d: %s", response.Code, response.Body)
			}
			requestID := response.Header().Get("x-request-id")
			var payload struct {
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode blocked response: %v", err)
			}
			if requestID == "" || payload.RequestID != requestID {
				t.Fatalf("response request_id=%q header=%q", payload.RequestID, requestID)
			}
			expectedRequestIDs[requestID] = true
		})
	}
	var payloads []RequestPayloadLog
	if err := store.db.Find(&payloads).Error; err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected blocked playground requests to be audited, got %#v", payloads)
	}
	for _, payload := range payloads {
		if strings.Contains(payload.RequestBody, "13312341234") || !strings.Contains(payload.RequestBody, "guardrail") {
			t.Fatalf("blocked playground audit leaked matched input: %#v", payload)
		}
		if !expectedRequestIDs[payload.RequestID] {
			t.Fatalf("audit request_id=%q does not match a blocked response: %#v", payload.RequestID, expectedRequestIDs)
		}
	}
}

func TestAdminPlaygroundRejectsUnauthorizedGuardrailProjectContext(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{
		Username: "guardrail-playground-user", Name: "Guardrail Playground User", Email: "guardrail-playground-user@tokenhub.local",
		Role: "user", Status: StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.AuthenticateAdminUser(user.Username, "user123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		t.Run(path, func(t *testing.T) {
			response := doGuardrailProtocolRequest(t, app, path, map[string]any{
				"project_id": "prj_demo",
				"model":      "gpt-4.1-mini",
				"messages":   []any{map[string]any{"role": "user", "content": "inspect me"}},
			}, session.Token)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"project_forbidden"`) {
				t.Fatalf("expected project access failure, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminPlaygroundRequiresGuardrailProjectContext(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		t.Run(path, func(t *testing.T) {
			response := doGuardrailProtocolRequest(t, app, path, map[string]any{
				"model": "gpt-4.1-mini", "messages": []any{map[string]any{"role": "user", "content": "inspect me"}},
			}, "dev_admin_token")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"missing_project"`) {
				t.Fatalf("expected missing project failure, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGuardrailModelDetectorRunsOnlyAfterAPIKeyAdmission(t *testing.T) {
	var detectorCalls atomic.Int64
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		detectorCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "Safety: Unsafe"}}},
		})
	}))
	defer model.Close()

	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name:   "Model detector",
		Status: guardrails.StatusActive,
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Qwen3Guard", DetectorType: guardrails.DetectorModel, Action: guardrails.ActionBlock,
			Config: map[string]any{"block_on": "unsafe", "on_unavailable": "block"},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", GuardrailModelURL: model.URL}).Handler()
	tests := []struct {
		name    string
		request func() *httptest.ResponseRecorder
	}{
		{
			name: "chat completions",
			request: func() *httptest.ResponseRecorder {
				return doGuardrailProtocolRequest(t, app, "/v1/chat/completions", map[string]any{
					"model": "model-not-allowed", "messages": []any{map[string]any{"role": "user", "content": "inspect me"}},
				}, "thk_demo_local")
			},
		},
		{
			name: "responses",
			request: func() *httptest.ResponseRecorder {
				return doGuardrailProtocolRequest(t, app, "/v1/responses", map[string]any{
					"model": "model-not-allowed", "input": "inspect me",
				}, "thk_demo_local")
			},
		},
		{
			name: "anthropic messages",
			request: func() *httptest.ResponseRecorder {
				return doAnthropicRequest(t, app, "/v1/messages", map[string]any{
					"model": "model-not-allowed", "max_tokens": 8,
					"messages": []any{map[string]any{"role": "user", "content": "inspect me"}},
				}, "", "thk_demo_local")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := test.request()
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"model_not_allowed"`) {
				t.Fatalf("expected model admission failure, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
	if calls := detectorCalls.Load(); calls != 0 {
		t.Fatalf("guard model was invoked %d times before API-key admission", calls)
	}
	var payloads []RequestPayloadLog
	if err := store.db.Find(&payloads).Error; err != nil {
		t.Fatal(err)
	}
	if len(payloads) != len(tests) {
		t.Fatalf("expected one admission audit per request, got %#v", payloads)
	}
	for _, payload := range payloads {
		if strings.Contains(payload.RequestBody, "inspect me") {
			t.Fatalf("admission failure persisted uninspected request content: %#v", payload)
		}
	}
}

func TestGuardrailsMaskAdminPlaygroundBeforeRouting(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Mask email addresses",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionMask,
			Config: map[string]any{"data_types": []string{"email"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		t.Run(path, func(t *testing.T) {
			response := doJSON(t, app, http.MethodPost, path, map[string]any{
				"project_id": "prj_demo",
				"model":      "gpt-4.1-mini",
				"messages":   []any{map[string]any{"role": "user", "content": "contact demo@example.com"}},
			}, "")
			if response.Code != http.StatusOK || !strings.Contains(response.Body, "contact [REDACTED]") || strings.Contains(response.Body, "demo@example.com") {
				t.Fatalf("expected masked playground request, got %d: %s", response.Code, response.Body)
			}
		})
	}
}

func TestGuardrailsMaskChatTextAndDoNotPersistMatchedInput(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Mask customer email",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionMask,
			Config: map[string]any{"data_types": []string{"email"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, New(store).Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini", "messages": []any{map[string]any{"role": "user", "content": "email demo@example.com"}},
	}, "thk_demo_local")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "email [REDACTED]") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var payloads []RequestPayloadLog
	if err := store.db.Find(&payloads).Error; err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 || strings.Contains(payloads[0].RequestBody, "demo@example.com") || !strings.Contains(payloads[0].RequestBody, "guardrail") {
		t.Fatalf("unsafe request audit payload: %#v", payloads)
	}
}

func TestAdminGuardrailPolicyTestUsesUnsavedPolicyWithoutPersistingSample(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	response := doJSON(t, app, http.MethodPost, "/api/admin/guardrail-policies/test", map[string]any{
		"policy": map[string]any{
			"name": "Unsaved policy", "status": "disabled",
			"detection_items": []any{map[string]any{
				"name": "Email", "detector_type": "sensitive_data", "action": "mask",
				"config": map[string]any{"data_types": []string{"email"}},
			}},
			"bindings": []any{map[string]any{"scope_type": "all_projects"}},
		},
		"text": "contact demo@example.com",
	}, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"action":"mask"`) || !strings.Contains(response.Body, `"masked_text":"contact [REDACTED]"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"start":8`) || !strings.Contains(response.Body, `"end":24`) || strings.Contains(response.Body, "demo@example.com") {
		t.Fatalf("test response must return match positions without echoing matched input: %s", response.Body)
	}
	policies, err := store.ListGuardrailPolicies()
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 0 {
		t.Fatalf("policy test persisted the draft: %#v", policies)
	}
	var payloadCount int64
	if err := store.db.Model(&RequestPayloadLog{}).Count(&payloadCount).Error; err != nil {
		t.Fatal(err)
	}
	if payloadCount != 0 {
		t.Fatalf("policy test persisted the sample in request logs: %d", payloadCount)
	}
}

func TestAdminGuardrailPolicyTestUsesConfiguredQwenDetector(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "Safety: Unsafe\nCategories: Data leakage"}}},
		})
	}))
	defer model.Close()
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", GuardrailModelURL: model.URL}).Handler()
	response := doJSON(t, app, http.MethodPost, "/api/admin/guardrail-policies/test", map[string]any{
		"policy": map[string]any{
			"name": "Model policy", "status": "active",
			"detection_items": []any{map[string]any{
				"name": "Qwen3Guard", "detector_type": "model", "action": "block",
				"config": map[string]any{"block_on": "unsafe", "on_unavailable": "allow_and_audit"},
			}},
			"bindings": []any{map[string]any{"scope_type": "all_projects"}},
		},
		"text": "classify this",
	}, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"action":"block"`) || !strings.Contains(response.Body, `"category":"unsafe"`) || strings.Contains(response.Body, "Data leakage") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestGuardrailTextExtractionSkipsToolArguments(t *testing.T) {
	request := ChatCompletionRequest{Messages: []ChatMessage{{
		Role: "assistant",
		Content: []any{
			map[string]any{"type": "text", "text": "visible text"},
			map[string]any{"type": "tool_use", "input": map[string]any{"secret": "do not inspect"}},
		},
	}}}
	targets := chatGuardrailTargets(&request)
	if len(targets) != 1 || targets[0].fragment.Text != "visible text" {
		t.Fatalf("unexpected extracted targets: %#v", targets)
	}
}
