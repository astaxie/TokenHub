package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaStructuredJSONUsesValidationAndMetering(t *testing.T) {
	for _, source := range []string{"adapter", "provider hook"} {
		for _, tc := range []struct {
			name, body, contentType string
			status                  int
		}{
			{"success", `{"id":9007199254740993,"usage":{"output_tokens":7,"total_tokens":7}}`, "", http.StatusOK},
			{"error envelope", `{"error":{"message":"upstream-fixture-secret"},"usage":{"output_tokens":7,"total_tokens":7}}`, "", http.StatusBadGateway},
			{"trailing value", `{"usage":{"output_tokens":7,"total_tokens":7}}{}`, "", http.StatusBadGateway},
			{"invalid JSON parameters", `{"error":{"message":"upstream-fixture-secret"},"usage":{"output_tokens":7,"total_tokens":7}}`, "application/json; charset=", http.StatusBadGateway},
			{"invalid structured JSON parameters", `{"error":{"message":"upstream-fixture-secret"},"usage":{"output_tokens":7,"total_tokens":7}}`, "application/vnd.fixture+json; charset=", http.StatusBadGateway},
		} {
			if source == "provider hook" && tc.contentType != "" {
				// Provider hooks reject malformed MIME types at envelope validation.
				continue
			}
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				contentType := firstNonEmpty(tc.contentType, "application/vnd.fixture+json; charset=utf-8")
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", contentType)
					_, _ = io.WriteString(w, tc.body)
				}, "audio")
				store.AddRoute(ModelRoute{ID: "fallback-structured-json", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				if source == "provider hook" {
					registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "structured-json", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls++
						payload, err := json.Marshal(map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte(tc.body)), "content_type": contentType})
						if err != nil {
							t.Fatal(err)
						}
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: payload}}}, nil
					})
				}
				response := serveMediaContentTypeRequest(server.Handler(), key)
				if response.Code != tc.status || calls != 1 || strings.Contains(response.Body, "upstream-fixture-secret") {
					t.Errorf("structured JSON outcome: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				if tc.status == http.StatusOK && (response.Body != tc.body || response.Header.Get("Content-Type") != contentType) {
					t.Error("structured JSON bytes or content type changed")
				}
				if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 7 {
					t.Errorf("structured JSON usage lost: %+v", records)
				}
			})
		}
	}
}

func TestMediaJSONContentTypeControlsPostHookPayload(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		jsonResponse            bool
	}{
		{"structured JSON", "Application/Vnd.Fixture+Json; charset=utf-8", `{"id":9007199254740993,"text":"fixture","usage":{"output_tokens":7,"total_tokens":7}}`, true},
		{"JSON parameter in text", `text/plain; profile="application/json"`, "fixture transcript", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = io.WriteString(w, tc.body)
			}, "audio")
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-content-type", HookID: "response", Stage: pluginmeta.StageResponsePost, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			calls := 0
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				calls++
				var payload map[string]json.RawMessage
				if err := json.Unmarshal(input.Data[pluginmeta.DataProviderResponse], &payload); err != nil {
					t.Fatal(err)
				}
				_, binary := payload["data_base64"]
				if binary == tc.jsonResponse {
					t.Errorf("wrong hook payload format: binary=%t want JSON=%t", binary, tc.jsonResponse)
				}
				return rawProviderResponsePatch(t, json.RawMessage(input.Data[pluginmeta.DataProviderResponse])), nil
			})); err != nil {
				t.Fatal(err)
			}
			response := serveMediaContentTypeRequest(server.Handler(), key)
			if response.Code != http.StatusOK || calls != 1 || response.Body != tc.body || response.Header.Get("Content-Type") != tc.contentType {
				t.Errorf("post-hook content changed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			if tc.jsonResponse {
				if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 7 {
					t.Errorf("post-hook JSON lost usage: %+v", records)
				}
			}
		})
	}
}

func serveMediaContentTypeRequest(handler http.Handler, key string) responseBody {
	request := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(`{"model":"public-media"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return responseBody{Code: recorder.Code, Body: recorder.Body.String(), Header: recorder.Header()}
}
