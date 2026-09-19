package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func newSystemOneTestServer(t *testing.T, upstream http.HandlerFunc) (*Server, *GormStore) {
	t.Helper()
	fixture := httptest.NewServer(upstream)
	t.Cleanup(fixture.Close)
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_typesafe_test", Name: "TypeSafe Test", Type: providerTypeSafe, BaseURL: fixture.URL + "/v1", APIKey: "synthetic-typesafe-key", Status: StatusActive, Healthy: true})
	store.AddModel(Model{ID: "jev-test", Name: "jev-test", Family: "jev", Modality: "decision", Status: StatusActive, InputPriceUSDPer1M: 0.042, OutputPriceUSDPer1M: 0})
	if _, err := store.CreateRoute(ModelRoute{ModelName: "jev-test", ProviderID: provider.ID, ProviderModel: "jev-latest", Status: StatusActive, Priority: 1, Weight: 100}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateAPIKey("prj_demo", APIKey{Name: "System One Key", Allowed: []string{"jev-test"}, Status: StatusActive}, "thk_systemone_test"); err != nil {
		t.Fatal(err)
	}
	return New(store), store
}

func TestGatewaySystemOneNativeContractAndBilling(t *testing.T) {
	var calls atomic.Int32
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("x-typesafe-request-id", "upstream-jev-test")
		writeFixture(t, w, systemOneFixtureResponse)
	})
	before := len(store.ListUsageRecords())
	resp := doGuardrailProtocolRequest(t, server.Handler(), "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if resp.Code != 200 {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body)
	}
	var result SystemOneResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if err := result.validate(systemOneTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	if resp.Header().Get("x-request-id") == "" || calls.Load() != 1 {
		t.Fatalf("missing request ID or retried low-confidence response: %d", calls.Load())
	}
	if resp.Header().Get("x-typesafe-request-id") != "upstream-jev-test" || resp.Header().Get("x-request-id") == resp.Header().Get("x-typesafe-request-id") {
		t.Fatalf("native and gateway request IDs were not preserved: %v", resp.Header())
	}
	if !strings.Contains(resp.Header().Get("access-control-expose-headers"), "x-typesafe-request-id") {
		t.Fatal("native request ID is not exposed to browser SDK clients")
	}
	records := store.ListUsageRecords()
	if len(records) != before+1 {
		t.Fatalf("usage count=%d before=%d", len(records), before)
	}
	found := false
	for _, usage := range records {
		if usage.ModelName == "jev-test" {
			found = true
			if usage.InputTokens != 422 || usage.OutputTokens != 69 || usage.TotalTokens != 491 || usage.OutputCostUSD != 0 || math.Abs(usage.CostUSD-422*0.042/1e6) > 1e-12 {
				t.Fatalf("usage=%+v", usage)
			}
		}
	}
	if !found {
		t.Fatal("missing Jev usage")
	}
	logs := store.ListRequestLogs()
	found = false
	for _, log := range logs {
		if log.ModelName == "jev-test" {
			found = true
			if log.ProviderModel != "jev-latest" || log.ServedModel != "jev-1.13.0" || log.UpstreamRequestID != "upstream-jev-test" {
				t.Fatalf("log=%+v", log)
			}
		}
	}
	if !found {
		t.Fatal("missing Jev request log")
	}
	models := doJSON(t, server.Handler(), "GET", "/v1/models", nil, "thk_systemone_test")
	if models.Code != 200 || !strings.Contains(models.Body, `"object":"list"`) {
		t.Fatalf("existing models contract changed: %s", models.Body)
	}
}

func TestGatewaySystemOneRejectsMalformedLegend(t *testing.T) {
	server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeFixture(t, w, strings.Replace(systemOneFixtureResponse, `"legend":{"0":"low","1":"high"}`, `"legend":{"0":42,"1":false}`, 1))
	})
	response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, `"provider_invalid_response"`) || strings.Contains(response.Body, `"legend"`) {
		t.Fatalf("expected sanitized invalid-response error, got status=%d body=%s", response.Code, response.Body)
	}
}

func TestGatewaySystemOneRejectsBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeFixture(t, w, systemOneFixtureResponse)
	})
	tests := []struct {
		name, token, body string
		status            int
	}{
		{"missing key", "", systemOneFixtureRequest, 401},
		{"invalid key", "invalid-key", systemOneFixtureRequest, 401},
		{"disallowed model", "thk_demo_local", systemOneFixtureRequest, 403},
		{"unknown field", "thk_systemone_test", strings.Replace(systemOneFixtureRequest, `"state":`, `"stream":true,"state":`, 1), 400},
		{"invalid rubric", "thk_systemone_test", strings.Replace(systemOneFixtureRequest, `["low","high"]`, `["low"]`, 1), 422},
		{"missing capability", "thk_demo_local", strings.Replace(systemOneFixtureRequest, `"jev-test"`, `"gpt-4.1-mini"`, 1), 501},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := doJSON(t, server.Handler(), "POST", "/v1/systemone", json.RawMessage(tt.body), tt.token)
			if resp.Code != tt.status {
				t.Fatalf("status=%d want=%d body=%s", resp.Code, tt.status, resp.Body)
			}
		})
	}
	tokenLimit := int64(1)
	if _, _, err := store.CreateAPIKey("prj_demo", APIKey{Name: "Limited System One Key", Allowed: []string{"jev-test"}, Status: StatusActive, TokenLimitTPM: &tokenLimit}, "thk_systemone_limited"); err != nil {
		t.Fatal(err)
	}
	limited := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_limited")
	if limited.Code != 429 {
		t.Fatalf("quota status=%d body=%s", limited.Code, limited.Body)
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected requests reached upstream %d times", calls.Load())
	}
}

func TestGatewaySystemOneGuardrailsInspectAllInputs(t *testing.T) {
	for _, location := range []string{"state", "instructions", "criteria", "question id", "state key", "choice label"} {
		t.Run(location, func(t *testing.T) {
			var calls atomic.Int32
			server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
			if _, err := store.CreateGuardrailPolicy(guardrails.Policy{Name: "Block marker", DetectionItems: []guardrails.DetectionItem{{Name: "Marker", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock, Config: map[string]any{"keywords": []string{"PRIVATE_MARKER"}}}}, Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}}}); err != nil {
				t.Fatal(err)
			}
			req := systemOneTestRequest(t)
			switch location {
			case "state":
				req.State = json.RawMessage(`{"nested":["PRIVATE_MARKER"]}`)
			case "instructions":
				q := req.Questions["urgent"]
				q.Instructions = json.RawMessage(`{"text":"PRIVATE_MARKER"}`)
				req.Questions["urgent"] = q
			case "criteria":
				q := req.Questions["intent"]
				q.Criteria = json.RawMessage(`{"refund":{"text":"PRIVATE_MARKER"},"other":null}`)
				req.Questions["intent"] = q
			case "question id":
				req.Questions["PRIVATE_MARKER"] = req.Questions["urgent"]
			case "state key":
				req.State = json.RawMessage(`{"PRIVATE_MARKER":"text"}`)
			case "choice label":
				q := req.Questions["intent"]
				q.Criteria = json.RawMessage(`{"PRIVATE_MARKER":null}`)
				req.Questions["intent"] = q
			}
			resp := doJSON(t, server.Handler(), "POST", "/v1/systemone", req, "thk_systemone_test")
			if resp.Code != 403 || calls.Load() != 0 {
				t.Fatalf("status=%d calls=%d body=%s", resp.Code, calls.Load(), resp.Body)
			}
		})
	}
}

func TestGatewaySystemOnePrivacyHookAndImmutableModel(t *testing.T) {
	for _, changeModel := range []bool{false, true} {
		t.Run(map[bool]string{false: "masked state", true: "model substitution rejected"}[changeModel], func(t *testing.T) {
			var calls atomic.Int32
			server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var req SystemOneRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if string(req.State) != `"[masked]"` {
					t.Errorf("unmasked state: %s", req.State)
				}
				writeFixture(t, w, systemOneFixtureResponse)
			})
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-typesafe-privacy", HookID: "mask", Stage: pluginmeta.StagePrivacyPre, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				if input.Envelope.RouteProtocol != "systemone" {
					t.Errorf("protocol=%s", input.Envelope.RouteProtocol)
				}
				var req SystemOneRequest
				if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &req); err != nil {
					t.Fatal(err)
				}
				req.State = json.RawMessage(`"[masked]"`)
				if changeModel {
					req.Model = "other"
				}
				return rawRequestBodyPatch(t, req), nil
			})); err != nil {
				t.Fatal(err)
			}
			resp := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
			if changeModel {
				if resp.Code != 502 || calls.Load() != 0 {
					t.Fatalf("status=%d calls=%d body=%s", resp.Code, calls.Load(), resp.Body)
				}
			} else if resp.Code != 200 || calls.Load() != 1 {
				t.Fatalf("status=%d calls=%d body=%s", resp.Code, calls.Load(), resp.Body)
			}
		})
	}
}

func TestGatewaySystemOneFailoverPreservesClientErrors(t *testing.T) {
	for _, status := range []int{422, 429, 529} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var primaryCalls, backupCalls atomic.Int32
			server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				primaryCalls.Add(1)
				w.WriteHeader(status)
				writeFixture(t, w, `{"error":{"message":"synthetic upstream failure"}}`)
			})
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				backupCalls.Add(1)
				writeFixture(t, w, systemOneFixtureResponse)
			}))
			defer backup.Close()
			provider := store.AddProvider(Provider{ID: "prv_typesafe_backup", Name: "TypeSafe Backup", Type: providerTypeSafe, BaseURL: backup.URL + "/v1", APIKey: "synthetic-backup-key", Status: StatusActive, Healthy: true})
			for _, route := range store.ListRoutes() {
				if route.ModelName == "jev-test" {
					route.Strategy = "priority_only"
					store.AddRoute(route)
				}
			}
			store.AddRoute(ModelRoute{ID: "route_typesafe_backup", ModelName: "jev-test", ProviderID: provider.ID, ProviderModel: "jev-latest", Priority: 2, Weight: 100, Status: StatusActive, Strategy: "priority_only"})
			response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
			if primaryCalls.Load() != 1 {
				t.Fatalf("primary calls=%d", primaryCalls.Load())
			}
			if status == 422 {
				if response.Code != 422 || backupCalls.Load() != 0 {
					t.Fatalf("client error triggered failover: status=%d backups=%d body=%s", response.Code, backupCalls.Load(), response.Body)
				}
			} else if response.Code != 200 || backupCalls.Load() != 1 {
				t.Fatalf("failover status=%d backups=%d body=%s", response.Code, backupCalls.Load(), response.Body)
			}
		})
	}
}

func TestTypeSafeAdminDiscoveryAndCatalogCapabilities(t *testing.T) {
	server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) { writeFixture(t, w, `{"models":[{"name":"jev-latest"}]}`) })
	provider, _ := server.store.GetProvider("prv_typesafe_test")
	discovery := doJSON(t, server.Handler(), "POST", "/api/admin/provider-catalog/custom", ProviderCreateRequest{Type: providerTypeSafe, BaseURL: provider.BaseURL, APIKey: "synthetic-typesafe-key"}, "")
	if discovery.Code != 200 || !strings.Contains(discovery.Body, `"systemone"`) || !strings.Contains(discovery.Body, `"decision"`) {
		t.Fatalf("discovery=%d %s", discovery.Code, discovery.Body)
	}
	catalog := doJSON(t, server.Handler(), "GET", "/api/admin/provider-catalog/typesafe", nil, "")
	if catalog.Code != 200 || !strings.Contains(catalog.Body, `"type":"typesafe"`) {
		t.Fatalf("catalog=%d %s", catalog.Code, catalog.Body)
	}
	normalized := normalizeProviderCatalogModel(map[string]any{"id": "jev-1.13.0", "type": "decision", "capabilities": []any{"systemone"}})
	if normalized.Type != "decision" || strings.Join(normalized.Capabilities, ",") != "systemone" {
		t.Fatalf("normalized=%+v", normalized)
	}
}

func TestGatewaySystemOneProviderHookUsesNativeMetering(t *testing.T) {
	var calls atomic.Int32
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-systemone-call", HookID: "invoke", Stage: pluginmeta.StageProviderCall, Priority: 2000, Metadata: map[string]string{"protocol": "systemone"}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicySkipRoute}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		result := rawProviderCallResult(t, json.RawMessage(systemOneFixtureResponse), Usage{})
		delete(result.Writes, pluginmeta.DataUsage)
		return result, nil
	})); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if response.Code != 200 || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
	}
	for _, record := range store.ListUsageRecords() {
		if record.ModelName == "jev-test" {
			if record.TotalTokens != 491 {
				t.Fatalf("hook usage=%+v", record)
			}
			return
		}
	}
	t.Fatal("missing hook usage record")
}

func TestGatewaySystemOneScopesSupportedLifecycleStages(t *testing.T) {
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) { writeFixture(t, w, systemOneFixtureResponse) })
	store.AddRoute(ModelRoute{ID: "route_typesafe_preview", ModelName: "jev-test", ProviderID: "prv_typesafe_test", ProviderModel: "jev-preview", Priority: 2, Weight: 100, Status: StatusActive})
	stages := []pluginmeta.GatewayHookStage{pluginmeta.StageAuthContext, pluginmeta.StageDecodeNormalize, pluginmeta.StageAdmission, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageRouteCandidates, pluginmeta.StageRouteRank, pluginmeta.StageRequestTransform, pluginmeta.StageProviderCall, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost, pluginmeta.StageUsageAttribution, pluginmeta.StageTraceExport}
	seen := map[pluginmeta.GatewayHookStage]bool{}
	for _, stage := range stages {
		policy, _ := pluginmeta.GatewayStagePolicy(stage)
		hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-systemone-scope", HookID: string(stage), Stage: stage, Priority: 2000, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"systemone"}}, FailurePolicy: policy.DefaultFailurePolicy}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatal(err)
		}
		if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			seen[stage] = true
			if input.Envelope.RouteProtocol != "systemone" {
				t.Errorf("stage=%s protocol=%s", stage, input.Envelope.RouteProtocol)
			}
			return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	for _, stage := range stages {
		if !seen[stage] {
			t.Errorf("System One scope skipped stage %s", stage)
		}
	}
}

func TestGatewaySystemOneScopedAuthPolicyBlocksUpstream(t *testing.T) {
	var calls atomic.Int32
	server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeFixture(t, w, systemOneFixtureResponse)
	})
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-systemone-auth", HookID: "deny", Stage: pluginmeta.StageAuthContext, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"systemone"}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, errors.New("synthetic authentication policy rejected request")
	})); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if response.Code == 200 || calls.Load() != 0 {
		t.Fatalf("scoped auth policy was bypassed: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body)
	}
}
