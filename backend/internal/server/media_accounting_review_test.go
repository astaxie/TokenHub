package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaUsageSurvivesAttributionFailure(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: transcript.text.done\ndata: {\"text\":\"fixture\",\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"fixture","usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`)
			}, "audio")
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-attribution", HookID: "deny", Stage: pluginmeta.StageUsageAttribution, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media", "stream": stream}, key)
			if response.Code < http.StatusBadRequest {
				t.Fatalf("attribution failure was accepted: %d %s", response.Code, response.Body)
			}
			usage := store.ListUsageRecords()
			if len(usage) != 1 || usage[0].TotalTokens != 10 {
				t.Fatalf("upstream usage lost after attribution failure: %+v", usage)
			}
		})
	}
}

func TestMediaGuardrailAuditRetainsDecisionWithoutContent(t *testing.T) {
	for _, action := range []string{guardrails.ActionMask, guardrails.ActionBlock, guardrails.ActionAudit} {
		t.Run(action, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if action == guardrails.ActionBlock {
					t.Error("blocked request reached upstream")
				}
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = io.WriteString(w, "fixture audio")
			}, "audio")
			if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
				Name:           "Protect customer email",
				DetectionItems: []guardrails.DetectionItem{{Name: "Email", DetectorType: guardrails.DetectorSensitiveData, Action: action, Config: map[string]any{"data_types": []string{"email"}}}},
				Bindings:       []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
			}); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "email fixture@example.com"}, key)
			wantStatus := http.StatusOK
			if action == guardrails.ActionBlock {
				wantStatus = http.StatusForbidden
			}
			if response.Code != wantStatus {
				t.Fatalf("status=%d, want %d: %s", response.Code, wantStatus, response.Body)
			}
			var payloads []RequestPayloadLog
			if err := store.db.Find(&payloads).Error; err != nil {
				t.Fatal(err)
			}
			if len(payloads) != 1 {
				t.Fatalf("audit records=%d, want 1", len(payloads))
			}
			var summary guardrailAuditSummary
			if err := json.Unmarshal([]byte(payloads[0].RequestBody), &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Guardrail.Action != action || len(summary.Guardrail.Findings) == 0 {
				t.Fatalf("guardrail audit decision lost: %+v", summary)
			}
			if len(summary.Guardrail.Replacements) != 0 || strings.Contains(payloads[0].RequestBody, "fixture@example.com") || strings.Contains(payloads[0].ResponseBody, "fixture audio") {
				t.Fatalf("media audit retained content: %+v", payloads[0])
			}
		})
	}
}
