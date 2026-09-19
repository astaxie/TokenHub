package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func systemOneResponseForCriteria(t *testing.T, req SystemOneRequest) SystemOneResponse {
	t.Helper()
	var response SystemOneResponse
	if err := json.Unmarshal([]byte(systemOneFixtureResponse), &response); err != nil {
		t.Fatal(err)
	}
	var levels []json.RawMessage
	if err := json.Unmarshal(req.Questions["severity"].Criteria, &levels); err != nil {
		t.Fatal(err)
	}
	legend := map[string]json.RawMessage{}
	probabilities := map[string]float64{}
	for i, level := range levels {
		legend[strconv.Itoa(i)] = level
		probabilities[strconv.Itoa(i)] = 0
	}
	probabilities["0"] = 1
	response.Answers["severity"], _ = json.Marshal(map[string]any{"type": "score", "score": 0, "confidence": 1, "legend": legend, "probabilities": probabilities})
	return response
}

func registerSystemOneTestHook(t *testing.T, server *Server, stage pluginmeta.GatewayHookStage, data pluginmeta.GatewayDataClass, handler pluginmeta.GatewayHookHandlerFunc) {
	t.Helper()
	policy, _ := pluginmeta.GatewayStagePolicy(stage)
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-systemone-legend", HookID: string(stage), Stage: stage, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{data}, Writes: []pluginmeta.GatewayDataClass{data}, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"systemone"}}, FailurePolicy: policy.DefaultFailurePolicy}
	if stage == pluginmeta.StageProviderCall {
		hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, handler); err != nil {
		t.Fatal(err)
	}
}

func TestGatewaySystemOneLegendUsesEffectiveCriteria(t *testing.T) {
	for _, hookProvider := range []bool{false, true} {
		for _, tamper := range []bool{false, true} {
			name := "adapter"
			if hookProvider {
				name = "provider hook"
			}
			if tamper {
				name += " with response tampering"
			}
			t.Run(name, func(t *testing.T) {
				server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
					if hookProvider {
						t.Error("provider hook was bypassed")
					}
					var req SystemOneRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					writeJSON(w, http.StatusOK, systemOneResponseForCriteria(t, req))
				})
				registerSystemOneTestHook(t, server, pluginmeta.StagePrivacyPre, pluginmeta.DataRequestBody, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					var req SystemOneRequest
					if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &req); err != nil {
						t.Fatal(err)
					}
					q := req.Questions["severity"]
					q.Criteria = json.RawMessage(`["[privacy masked]","high"]`)
					req.Questions["severity"] = q
					return rawRequestBodyPatch(t, req), nil
				})
				registerSystemOneTestHook(t, server, pluginmeta.StageRequestTransform, pluginmeta.DataProviderRequest, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					var req SystemOneRequest
					if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &req); err != nil {
						t.Fatal(err)
					}
					q := req.Questions["severity"]
					if !strings.Contains(string(q.Criteria), "[privacy masked]") {
						t.Error("privacy transform was lost")
					}
					q.Criteria = json.RawMessage(`["[route masked]","high"]`)
					req.Questions["severity"] = q
					return rawProviderRequestPatch(t, req), nil
				})
				if hookProvider {
					registerSystemOneTestHook(t, server, pluginmeta.StageProviderCall, pluginmeta.DataProviderRequest, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						var req SystemOneRequest
						if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &req); err != nil {
							t.Fatal(err)
						}
						return rawProviderCallResult(t, systemOneResponseForCriteria(t, req), Usage{}), nil
					})
				}
				registerSystemOneTestHook(t, server, pluginmeta.StageResponsePost, pluginmeta.DataProviderResponse, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					data := input.Data[pluginmeta.DataProviderResponse]
					if strings.Contains(string(data), `"questions"`) || !strings.Contains(string(data), `"answers"`) {
						t.Errorf("unexpected hook response: %s", data)
					}
					if tamper {
						data = json.RawMessage(strings.Replace(string(data), "[route masked]", "changed legend", 1))
					}
					return rawProviderResponsePatch(t, data), nil
				})
				response := doJSON(t, server.Handler(), "POST", "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
				if tamper {
					if response.Code != 502 || !strings.Contains(response.Body, "provider_invalid_response") || strings.Contains(response.Body, "changed legend") {
						t.Fatalf("status=%d body=%s", response.Code, response.Body)
					}
				} else if response.Code != 200 || !strings.Contains(response.Body, "[route masked]") || strings.Contains(response.Body, "[privacy masked]") || strings.Contains(response.Body, `"questions"`) {
					t.Fatalf("status=%d body=%s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestGatewaySystemOneLegendUsesWinningRoute(t *testing.T) {
	primaryCalls, backupCalls := 0, 0
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		w.Header().Set("x-typesafe-request-id", "failed-route-id")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		w.Header().Set("x-typesafe-request-id", "winning-route-id")
		var req SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		writeJSON(w, http.StatusOK, systemOneResponseForCriteria(t, req))
	}))
	defer backup.Close()
	provider := store.AddProvider(Provider{ID: "prv_legend_backup", Name: "Legend Backup", Type: providerTypeSafe, BaseURL: backup.URL + "/v1", APIKey: "synthetic-key", Status: StatusActive, Healthy: true})
	for _, route := range store.ListRoutes() {
		if route.ModelName == "jev-test" {
			route.Strategy = "priority_only"
			store.AddRoute(route)
		}
	}
	store.AddRoute(ModelRoute{ID: "route_legend_backup", ModelName: "jev-test", ProviderID: provider.ID, ProviderModel: "jev-latest", Priority: 2, Weight: 100, Status: StatusActive, Strategy: "priority_only"})
	transforms := 0
	registerSystemOneTestHook(t, server, pluginmeta.StageRequestTransform, pluginmeta.DataProviderRequest, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		transforms++
		var req SystemOneRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &req); err != nil {
			t.Fatal(err)
		}
		q := req.Questions["severity"]
		if string(q.Criteria) != `["low","high"]` {
			t.Errorf("failed route leaked its criteria: %s", q.Criteria)
		}
		q.Criteria = json.RawMessage(`["route ` + strconv.Itoa(transforms) + `","high"]`)
		req.Questions["severity"] = q
		return rawProviderRequestPatch(t, req), nil
	})
	response := doGuardrailProtocolRequest(t, server.Handler(), "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if response.Code != 200 || primaryCalls != 1 || backupCalls != 1 || transforms != 2 || !strings.Contains(response.Body.String(), `"0":"route 2"`) {
		t.Fatalf("status=%d primary=%d backup=%d transforms=%d body=%s", response.Code, primaryCalls, backupCalls, transforms, response.Body)
	}
	if response.Header().Get("x-typesafe-request-id") != "winning-route-id" {
		t.Fatalf("winning request ID was not forwarded: %v", response.Header())
	}
}
