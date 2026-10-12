package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGatewaySystemOneRejectsUnsafeJSONBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeFixture(t, w, systemOneFixtureResponse)
	})
	for _, tc := range []struct {
		name, state string
		status      int
	}{
		{"duplicate PII", `{"contact":"13812345678","contact":"safe"}`, 400},
		{"wide array", `[` + strings.Repeat(`null,`, systemOneMaxJSONNodes) + `null]`, 400},
		{"large exponent", `[1e` + strings.Repeat("9", 1_000_000) + `]`, 400},
		{"target budget", `[` + strings.Repeat(`1e1,`, systemOneMaxJSONNodes/2) + `1e1]`, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := `{"model":"jev-test","state":` + tc.state + `,"questions":{"urgent":{"type":"noul"}}}`
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", json.RawMessage(payload), "thk_systemone_test")
			if response.Code != tc.status || calls.Load() != 0 {
				t.Fatalf("status=%d upstream calls=%d", response.Code, calls.Load())
			}
		})
	}
}

func TestSystemOneGuardrailBatchSerializesEachChangedEntryOnce(t *testing.T) {
	const scalar = `9007199254740993.000`
	raw := json.RawMessage(`{"id":` + scalar + `,"contacts":[` + strings.Repeat(`"sensitive",`, 4095) + `"sensitive"]}`)
	batch := &systemOneGuardrailBatch{}
	commits, unsafe := 0, false
	if err := batch.entry(raw, "state", systemOneMaxJSONDepth, func(next json.RawMessage) { raw = next; commits++ }, &unsafe); err != nil {
		t.Fatal(err)
	}
	for _, target := range batch.targets {
		if target.fragment.Text == "sensitive" {
			target.replace("masked")
		}
	}
	if commits != 0 {
		t.Fatal("a leaf replacement serialized its ancestors")
	}
	if err := batch.apply(); err != nil {
		t.Fatal(err)
	}
	if commits != 1 || unsafe || strings.Count(string(raw), `"masked"`) != 4096 || !strings.Contains(string(raw), scalar) {
		t.Fatalf("commits=%d unsafe=%v; replacements or numeric precision lost", commits, unsafe)
	}
}

func TestGatewaySystemOneDuplicateResponseAndHookPayloads(t *testing.T) {
	payload := strings.Replace(systemOneFixtureResponse, `"choice":"refund"`, `"choice":"other","choice":"refund"`, 1)
	for _, stage := range []pluginmeta.GatewayHookStage{"", pluginmeta.StageProviderCall, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		t.Run(string(stage), func(t *testing.T) {
			server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if stage == "" {
					writeFixture(t, w, payload)
				} else {
					writeFixture(t, w, systemOneFixtureResponse)
				}
			})
			if stage != "" {
				readClass := pluginmeta.DataProviderResponse
				if stage == pluginmeta.StageProviderCall {
					readClass = pluginmeta.DataProviderRequest
				}
				registerSystemOneTestHook(t, server, stage, readClass, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					if stage == pluginmeta.StageProviderCall {
						return rawProviderCallResult(t, json.RawMessage(payload), Usage{}), nil
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
						pluginmeta.DataProviderResponse: {Value: json.RawMessage(payload)},
					}}, nil
				})
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "provider_invalid_response") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestGatewaySystemOneRejectsUnsafeRequestHookPatches(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StagePrivacyPre, pluginmeta.StageGuardrailPre, pluginmeta.StageContextOptimize, pluginmeta.StageRequestTransform} {
		t.Run(string(stage), func(t *testing.T) {
			var calls atomic.Int32
			server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
			dataClass := pluginmeta.DataRequestBody
			if stage == pluginmeta.StageRequestTransform {
				dataClass = pluginmeta.DataProviderRequest
			}
			registerSystemOneTestHook(t, server, stage, dataClass, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
					dataClass: {Value: json.RawMessage(strings.Replace(systemOneFixtureRequest, `"order_id":9007199254740993`, `"x":"blocked","x":"safe"`, 1))},
				}}, nil
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
			if response.Code != http.StatusBadGateway || calls.Load() != 0 {
				t.Fatalf("status=%d upstream calls=%d body=%s", response.Code, calls.Load(), response.Body)
			}
		})
	}
}

func TestGatewaySystemOneMasksAllNestedStrings(t *testing.T) {
	server, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request SystemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if strings.Contains(string(request.State), "13812345678") || strings.Count(string(request.State), "[REDACTED]") != 3 {
			t.Error("batched redaction did not reach every nested string")
		}
		writeFixture(t, w, systemOneFixtureResponse)
	})
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name:           "Mask nested phone numbers",
		DetectionItems: []guardrails.DetectionItem{{Name: "Phone", DetectorType: guardrails.DetectorSensitiveData, Action: guardrails.ActionMask, Config: map[string]any{"data_types": []string{"phone"}}}},
		Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	req := systemOneTestRequest(t)
	req.State = json.RawMessage(`{"a":["13812345678",{"b":"13812345678"}],"c":"13812345678"}`)
	if response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", req, "thk_systemone_test"); response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestGatewaySystemOnePluginDisableAndRestore(t *testing.T) {
	var calls atomic.Int32
	_, store := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeFixture(t, w, systemOneFixtureResponse)
	})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	for _, tc := range []struct {
		status string
		code   int
		calls  int32
	}{
		{"enabled", http.StatusOK, 1},
		{"disabled", http.StatusNotImplemented, 1},
		{"enabled", http.StatusOK, 2},
	} {
		patched := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.provider.typesafe/state", map[string]any{"status": tc.status}, "dev_admin_token")
		if patched.Code != http.StatusOK {
			t.Fatalf("plugin %s: status=%d body=%s", tc.status, patched.Code, patched.Body)
		}
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
		if response.Code != tc.code || calls.Load() != tc.calls {
			t.Fatalf("plugin %s: status=%d upstream calls=%d body=%s", tc.status, response.Code, calls.Load(), response.Body)
		}
		if tc.status == "disabled" && !strings.Contains(response.Body, "provider_capability_not_supported") {
			t.Fatal("unexpected disabled-provider error")
		}
	}
}

func TestSystemOneGuardrailDepthMatchesEntryValidation(t *testing.T) {
	for _, depth := range []int{64, 65} {
		entry := strings.Repeat("[", depth) + `"safe"` + strings.Repeat("]", depth)
		for _, primitive := range []string{"choice", "noul", "score"} {
			t.Run(primitive+"/"+strconv.Itoa(depth), func(t *testing.T) {
				criteria := `{"true":` + entry + `}`
				if primitive == "score" {
					criteria = `[` + entry + `,"high"]`
				}
				req := SystemOneRequest{Model: "jev-test", State: json.RawMessage(`null`), Questions: map[string]SystemOneQuestion{
					"q": {Type: primitive, Criteria: json.RawMessage(criteria)},
				}}
				_, err := systemOneGuardrailTargets(&req)
				if (err == nil) != (depth == 64) {
					t.Fatalf("depth=%d error=%v", depth, err)
				}
			})
		}
	}
}

func TestGatewaySystemOneRejectsDuplicatePatchBeforeNextHookStage(t *testing.T) {
	server, _ := newSystemOneTestServer(t, func(w http.ResponseWriter, r *http.Request) { writeFixture(t, w, systemOneFixtureResponse) })
	bad := strings.Replace(systemOneFixtureResponse, `"choice":"refund"`, `"choice":"other","choice":"refund"`, 1)
	registerSystemOneTestHook(t, server, pluginmeta.StageResponsePost, pluginmeta.DataProviderResponse, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(bad)}}}, nil
	})
	called := false
	registerSystemOneTestHook(t, server, pluginmeta.StageGuardrailPost, pluginmeta.DataProviderResponse, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		called = true
		var value map[string]any
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderResponse], &value); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return pluginmeta.GatewayHookResult{Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: encoded}}}, nil
	})
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/systemone", systemOneTestRequest(t), "thk_systemone_test")
	if response.Code != http.StatusBadGateway || called {
		t.Fatalf("status=%d subsequent hook called=%v", response.Code, called)
	}
}
