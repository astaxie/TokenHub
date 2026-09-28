package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaProviderHookJSONOverflowRetainsKnownUsage(t *testing.T) {
	for _, protocol := range []string{"audio/speech", providerRouteProtocolGemini} {
		for _, tc := range []struct {
			name, explicitUsage string
			wantTokens          int64
		}{
			{name: "body usage fallback", wantTokens: 15},
			{name: "explicit usage overrides body", explicitUsage: `{"completion_tokens":3,"total_tokens":3}`, wantTokens: 3},
			{name: "explicit zero overrides body", explicitUsage: `{"total_tokens":0}`},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				providerType, path := ProviderOpenAICompatible, "/v1/audio/speech"
				body := map[string]any{"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 0, "output_tokens": 10, "total_tokens": 15}}
				if protocol == providerRouteProtocolGemini {
					providerType, path = ProviderGemini, "/v1beta/models/classified-model:generateContent"
					if err := decodeResponsesJSON([]byte(geminiMediaFixtureResponse), &body); err != nil {
						t.Fatal(err)
					}
					delete(body, "usage")
				}
				body["media"] = strings.Repeat("a", 1024)
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1", providerType)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				hookCalls := 0
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "json-overflow", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{protocol}}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: encoded}}
					if tc.explicitUsage != "" {
						writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(tc.explicitUsage)}
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
				})
				request := httptest.NewRequest(http.MethodPost, path, nil)
				request.Header.Set("Authorization", "Bearer "+key)
				project, apiKey, err := server.authenticate(request)
				if err != nil {
					t.Fatal(err)
				}
				call, err := server.admitRoutedCall(httptest.NewRecorder(), request, project, apiKey, "classified-model", false, 0)
				if err != nil {
					t.Fatal(err)
				}
				routed, err := server.prepareAdmittedRoutedCall(request.Context(), call, call.Model.Name)
				if err != nil {
					t.Fatal(err)
				}
				result, _, usage, attempts, err := executeRoutedWithStore(request.Context(), store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (mediaResponse, Usage, error) {
					if protocol == providerRouteProtocolGemini {
						return server.invokeGeminiMediaWithResponseLimit(ctx, call, route, map[string]any{"contents": []any{}}, 128)
					}
					return server.invokeMediaRouteWithResponseLimit(ctx, call, route, "/audio/speech", mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"classified-model"`)}}, 128)
				})
				if err == nil || providerErrorDisposition(err) != ProviderErrorPolicy || AsHTTPError(err).Code != "gateway_hook_response_invalid" || hookCalls != 1 || len(result.Body) != 0 {
					t.Fatalf("overflow failure changed or retried: calls=%d bytes=%d error=%v", hookCalls, len(result.Body), err)
				}
				if usage.TotalTokens != tc.wantTokens || !usage.MeteringInvalid {
					t.Fatalf("overflow metering = %+v, want %d known tokens and invalid metering", usage, tc.wantTokens)
				}
				if tc.explicitUsage == "" && (usage.PromptTokens != 5 || usage.CompletionTokens != 10) {
					t.Errorf("body token breakdown lost: %+v", usage)
				}
				server.finishFailedRoutedCall(request, routed, attempts, usage, err, nil)
				var tokens int64
				for _, record := range store.ListUsageRecords() {
					tokens += record.TotalTokens
				}
				var logged []RouteAttemptLog
				if err := store.db.Find(&logged).Error; err != nil {
					t.Fatal(err)
				}
				if tokens != tc.wantTokens || len(logged) != 1 || logged[0].TotalTokens != tc.wantTokens || logged[0].StatusCode != http.StatusBadGateway {
					t.Fatalf("overflow usage lost in accounting: tokens=%d attempts=%+v", tokens, logged)
				}
				for _, id := range []string{"rsrc_classified_0", "rsrc_classified_1"} {
					if failures := resourceFailureCount(t, store, id); failures != 0 {
						t.Errorf("plugin overflow penalized %s: failures=%d", id, failures)
					}
				}
			})
		}
	}
}

func TestMediaProviderHookResponseLimit(t *testing.T) {
	for _, tc := range []struct {
		name, payload, contentType string
		body                       []byte
	}{
		{"JSON", `{"id":9007199254740993}`, "application/json", []byte(`{"id":9007199254740993}`)},
		{"binary", `{"data_base64":"AP8q","content_type":"audio/mpeg"}`, "audio/mpeg", []byte{0, 255, 42}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, limit := range []int{len(tc.body) - 1, len(tc.body), len(tc.body) + 1} {
				result, err := mediaProviderHookResponse(json.RawMessage(tc.payload), limit)
				if limit < len(tc.body) {
					if err == nil || providerErrorDisposition(err) != ProviderErrorPolicy || len(result.Body) != 0 {
						t.Errorf("oversized output accepted: limit=%d bytes=%d error=%v", limit, len(result.Body), err)
					}
				} else if err != nil || !bytes.Equal(result.Body, tc.body) || result.ContentType != tc.contentType {
					t.Errorf("bounded output changed: limit=%d result=%+v error=%v", limit, result, err)
				}
			}
		})
	}
}
