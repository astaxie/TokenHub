package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGatewayHookHeadersRedactGeminiCredentialCaseInsensitively(t *testing.T) {
	for _, name := range []string{"x-goog-api-key", "X-Goog-Api-Key", "X-GOOG-API-KEY", "x-gOoG-aPi-kEy"} {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{name: {"client-fixture-key", "second-fixture-key"}, "X-Correlation-Id": {"first", "second"}}
			sanitized := sanitizedGatewayHookHeaders(headers)
			if !reflect.DeepEqual(sanitized["X-Goog-Api-Key"], []string{"[redacted]"}) {
				t.Fatalf("Gemini credential exposed: %v", sanitized)
			}
			if !reflect.DeepEqual(sanitized["X-Correlation-Id"], headers["X-Correlation-Id"]) || headers[name][0] != "client-fixture-key" {
				t.Fatalf("sanitizer changed harmless headers or the authenticated request: %v", headers)
			}
		})
	}
}

func TestGeminiMediaPreflightHooksReceiveRedactedClientCredential(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageDecodeNormalize, pluginmeta.StageAdmission, pluginmeta.StagePrivacyPre} {
		for _, stream := range []bool{false, true} {
			t.Run(string(stage)+"/"+map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
				upstreamCalls, hookCalls := 0, 0
				body, contentType := geminiMediaFixtureResponse, "application/json"
				if stream {
					body, contentType = "data: "+body+"\n\n", "text/event-stream"
				}
				server, _, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
					upstreamCalls++
					if r.Header.Get("X-Goog-Api-Key") != "upstream-fixture-secret" || r.Header.Get("Authorization") != "" {
						t.Errorf("configured provider authentication changed: %v", r.Header)
					}
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, body)
				})
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.gemini-header-privacy", HookID: string(stage), Stage: stage, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestHeaders}, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolGemini}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					var headers map[string][]string
					if err := json.Unmarshal(input.Data[pluginmeta.DataRequestHeaders], &headers); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(headers["X-Goog-Api-Key"], []string{"[redacted]"}) || strings.Contains(string(input.Data[pluginmeta.DataRequestHeaders]), key) {
						t.Errorf("client credential exposed to %s hook: %v", stage, headers)
					}
					if !reflect.DeepEqual(headers["X-Correlation-Id"], []string{"trace-fixture"}) {
						t.Errorf("harmless request header lost: %v", headers)
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				})); err != nil {
					t.Fatal(err)
				}
				action := "generateContent"
				if stream {
					action = "streamGenerateContent?alt=sse"
				}
				request := httptest.NewRequest(http.MethodPost, "/v1beta/models/public-media:"+action, strings.NewReader(geminiMediaFixtureRequest))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Goog-Api-Key", key)
				request.Header.Set("X-Correlation-Id", "trace-fixture")
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Body.String() != body || hookCalls != 1 || upstreamCalls != 1 {
					t.Fatalf("native authentication or delivery changed: status=%d hooks=%d upstream=%d body=%s", response.Code, hookCalls, upstreamCalls, response.Body)
				}
			})
		}
	}
}
