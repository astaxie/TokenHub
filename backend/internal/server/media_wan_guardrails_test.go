package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

var wanMediaPromptFixtures = []struct {
	name, modality, fields string
	text                   map[string]string
}{
	{
		name: "nested video input", modality: "video",
		fields: `"input":{"input":{"prompt":"contact demo@example.com","media":[{"type":"first_frame","url":"https://example.com/demo@example.com.png","asset_id":9007199254740993}],"task_id":"task@example.com"},"parameters":{"duration":5,"watermark":false,"seed":9007199254740993,"metadata":{"prompt":"opaque@example.com"}}}`,
		text:   map[string]string{"input.input.prompt": "contact demo@example.com"},
	},
	{
		name: "image negative prompt", modality: "image",
		fields: `"input":{"messages":[{"role":"user","content":[{"text":"A landscape"},{"image":"https://example.com/demo@example.com.png"}]}]},"parameters":{"negative_prompt":"contact demo@example.com","size":"1280*1280","n":2,"seed":9007199254740993,"watermark":false,"metadata":{"input":"opaque@example.com"}}`,
		text:   map[string]string{"input.messages.0.content.0.text": "A landscape", "parameters.negative_prompt": "contact demo@example.com"},
	},
}

func TestMediaResponsesWanPromptContainersRespectTextPolicies(t *testing.T) {
	for _, fixture := range wanMediaPromptFixtures {
		for _, mode := range []string{"synchronous", "background"} {
			for _, action := range []string{guardrails.ActionBlock, guardrails.ActionMask} {
				t.Run(fixture.name+"/"+mode+"/"+action, func(t *testing.T) {
					var calls atomic.Int32
					server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						var actual map[string]json.RawMessage
						decodeFixtureRequest(t, r.Body, &actual)
						var expected map[string]json.RawMessage
						masked := strings.ReplaceAll(fixture.fields, "contact demo@example.com", "contact [REDACTED]")
						if err := json.Unmarshal([]byte(`{`+masked+`}`), &expected); err != nil {
							t.Fatal(err)
						}
						for name, want := range expected {
							var gotValue, wantValue any
							if err := decodeResponsesJSON(actual[name], &gotValue); err != nil {
								t.Fatal(err)
							}
							if err := decodeResponsesJSON(want, &wantValue); err != nil {
								t.Fatal(err)
							}
							if !reflect.DeepEqual(gotValue, wantValue) {
								t.Errorf("Wan field %s = %s, want %s", name, actual[name], want)
							}
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"wan_fixture"}`)
					}, fixture.modality)
					if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
						Name:           "Protect Wan prompts",
						DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: action, Config: map[string]any{"data_types": []string{"email"}}}},
						Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
					}); err != nil {
						t.Fatal(err)
					}
					fields := fixture.fields
					if mode == "background" {
						fields += `,"background":true`
					}
					response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(`{"model":"public-media",`+fields+`}`), key)
					if mode == "background" {
						if response.Code != http.StatusOK {
							t.Fatalf("background submission failed: %d %s", response.Code, response.Body)
						}
						var submitted struct {
							ID string `json:"id"`
						}
						if err := json.Unmarshal([]byte(response.Body), &submitted); err != nil || submitted.ID == "" {
							t.Fatalf("background submission has no job ID: %s, error: %v", response.Body, err)
						}
						status := "completed"
						if action == guardrails.ActionBlock {
							status = "failed"
						}
						result := waitForResponseJobStatus(t, server.Handler(), key, submitted.ID, status)
						if action == guardrails.ActionBlock {
							failure, _ := result["error"].(map[string]any)
							if failure["code"] != "guardrail_blocked" {
								t.Fatalf("background prompt failed for another reason: %v", result)
							}
						}
					} else if action == guardrails.ActionBlock {
						if response.Code != http.StatusForbidden || !strings.Contains(response.Body, "guardrail_blocked") {
							t.Fatalf("Wan prompt bypassed policy: %d %s", response.Code, response.Body)
						}
					} else if response.Code != http.StatusOK {
						t.Fatalf("masked Wan request failed: %d %s", response.Code, response.Body)
					}
					wantCalls := int32(1)
					if action == guardrails.ActionBlock {
						wantCalls = 0
					}
					if got := calls.Load(); got != wantCalls {
						t.Fatalf("upstream calls = %d, want %d", got, wantCalls)
					}
				})
			}
		}
	}
}

func TestMediaResponsesWanPromptContainersReachGuardrailPlugins(t *testing.T) {
	for _, fixture := range wanMediaPromptFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			server, _, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("denied request reached provider") }, fixture.modality)
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.wan-prompts", HookID: "text", Stage: pluginmeta.StageGuardrailPre, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataNormalizedText}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				var segments []pluginmeta.TextSegment
				if err := json.Unmarshal(input.Data[pluginmeta.DataNormalizedText], &segments); err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for _, segment := range segments {
					got[segment.ID] = segment.Text
				}
				if len(segments) != len(fixture.text) || !reflect.DeepEqual(got, fixture.text) {
					t.Errorf("normalized Wan text = %v, want each fragment once: %v", segments, fixture.text)
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(`{"model":"public-media",`+fixture.fields+`}`), key)
			if response.Code != http.StatusForbidden {
				t.Fatalf("guardrail plugin denial failed: %d %s", response.Code, response.Body)
			}
		})
	}
}
