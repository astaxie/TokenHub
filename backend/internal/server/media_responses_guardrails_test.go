package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaResponsesVendorTextPoliciesRunBeforeUpstream(t *testing.T) {
	for _, tc := range []struct{ name, fields string }{
		{"wan message", `"input":{"messages":[{"role":"user","content":[{"text":"contact demo@example.com"},{"image":"https://example.com/demo@example.com"}]}],"file_id":9007199254740993}`},
		{"vendor input prompt", `"input":{"prompt":"contact demo@example.com","task_id":"demo@example.com","file":"ZGVtb0BleGFtcGxlLmNvbQ==","file_id":9007199254740993}`},
		{"prompt", `"input":{"task_id":"demo@example.com"},"prompt":"contact demo@example.com"`},
		{"lyrics", `"input":{"task_id":"demo@example.com"},"lyrics":"contact demo@example.com"`},
		{"voice prompt", `"input":[{"file_id":9007199254740993}],"clone_prompt":{"prompt_text":"contact demo@example.com","prompt_audio":9007199254740993}`},
	} {
		for _, action := range []string{guardrails.ActionMask, guardrails.ActionBlock} {
			t.Run(tc.name+" "+action, func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					var actual, expected map[string]json.RawMessage
					decodeFixtureRequest(t, r.Body, &actual)
					masked := strings.ReplaceAll(tc.fields, "contact demo@example.com", "contact [REDACTED]")
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
							t.Errorf("%s = %s, want %s", name, actual[name], want)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"task_fixture"}`)
				}, "video")
				if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
					Name: "Protect email in media prompts",
					DetectionItems: []guardrails.DetectionItem{{
						Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: action,
						Config: map[string]any{"data_types": []string{"email"}},
					}},
					Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
				}); err != nil {
					t.Fatal(err)
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(`{"model":"public-media",`+tc.fields+`}`), key)
				if action == guardrails.ActionBlock {
					if response.Code != http.StatusForbidden || calls != 0 || !strings.Contains(response.Body, "guardrail_blocked") {
						t.Fatalf("blocked prompt reached upstream: calls=%d response=%d %s", calls, response.Code, response.Body)
					}
				} else if response.Code != http.StatusOK || calls != 1 {
					t.Fatalf("masked request failed: calls=%d response=%d %s", calls, response.Code, response.Body)
				}
			})
		}
	}
}

func TestMediaResponsesGuardrailPluginSeesVendorText(t *testing.T) {
	calls := 0
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls++ }, "audio")
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "tokenhub.test-media-text", HookID: "deny", Stage: pluginmeta.StageGuardrailPre, Priority: 1000,
		Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataNormalizedText}, FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
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
		want := map[string]string{"input.messages.0.content.0.text": "edit this", "lyrics": "sing this", "prompt": "draw this"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("normalized text = %v, want %v", got, want)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", json.RawMessage(`{"model":"public-media","input":{"messages":[{"role":"user","content":[{"text":"edit this"},{"image":"https://example.com/input.png"}]}],"task_id":"opaque"},"lyrics":"sing this","prompt":"draw this"}`), key)
	if response.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("plugin denial failed: calls=%d response=%d %s", calls, response.Code, response.Body)
	}
}

func TestResponsesVendorTextTargetsLeaveTextModelsUnchanged(t *testing.T) {
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"text-model","input":[{"type":"text","text":"standard prompt"}],"prompt":"opaque extension"}`), &request); err != nil {
		t.Fatal(err)
	}
	for _, modality := range []string{"text", "video"} {
		targets := routedResponsesGuardrailTargets(CallContext{Model: Model{Modality: modality}}, &request)
		want := 1
		if modality == "video" {
			want = 2
		}
		if len(targets) != want {
			t.Errorf("%s targets = %d, want %d", modality, len(targets), want)
		}
	}
}
