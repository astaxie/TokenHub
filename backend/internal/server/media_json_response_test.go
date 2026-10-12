package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaJSONResponseRejectsTrailingDataWithoutRetry(t *testing.T) {
	const valid = `{"text":"fixture","id":9007199254740993,"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`
	for _, tc := range []struct {
		name, body string
		status     int
		tokens     int64
	}{
		{"valid whitespace", valid + "\n \t", http.StatusOK, 10},
		{"trailing object", valid + `{}`, http.StatusBadGateway, 10},
		{"trailing garbage", valid + "upstream-fixture-secret", http.StatusBadGateway, 10},
		{"null object", `null`, http.StatusBadGateway, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-json-media", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
			if response.Code != tc.status || calls != 1 || strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Fatalf("JSON outcome: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			if tc.status == http.StatusOK && response.Body != tc.body {
				t.Errorf("valid provider JSON changed: %s", response.Body)
			}
			var tokens int64
			for _, usage := range store.ListUsageRecords() {
				tokens += usage.TotalTokens
			}
			if tokens != tc.tokens {
				t.Errorf("reported usage lost: tokens=%d want=%d", tokens, tc.tokens)
			}
			if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].StatusCode != tc.status {
				t.Fatalf("JSON result accounted incorrectly: %+v", logs)
			}
		})
	}
}

func TestMediaProviderHookJSONUsageFallbackAndOverride(t *testing.T) {
	const body = `{"text":"fixture","id":9007199254740993,"usage":{"input_tokens":3,"input_token_details":{"audio_tokens":2},"output_tokens":7,"total_tokens":10}}`
	for _, binary := range []bool{false, true} {
		for _, tc := range []struct {
			name, explicitUsage string
			tokens, audioTokens int64
		}{
			{"body fallback", "", 10, 2},
			{"explicit override", `{"completion_tokens":3,"total_tokens":3}`, 3, 0},
			{"explicit zero", `{"total_tokens":0}`, 0, 0},
		} {
			t.Run(map[bool]string{false: "JSON", true: "binary JSON"}[binary]+"/"+tc.name, func(t *testing.T) {
				server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("handled JSON reached upstream") }, "audio")
				registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "json-usage", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					payload := json.RawMessage(body)
					if binary {
						var err error
						payload, err = json.Marshal(map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte(body)), "content_type": "application/json"})
						if err != nil {
							t.Fatal(err)
						}
					}
					writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: payload}}
					if tc.explicitUsage != "" {
						writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(tc.explicitUsage)}
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
				})
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
				if response.Code != http.StatusOK || !strings.Contains(response.Body, `"id":9007199254740993`) {
					t.Fatalf("plugin JSON response changed: %d %s", response.Code, response.Body)
				}
				var tokens, audioTokens int64
				for _, usage := range store.ListUsageRecords() {
					tokens += usage.TotalTokens
					audioTokens += usage.InputAudioTokens
				}
				if tokens != tc.tokens || audioTokens != tc.audioTokens {
					t.Errorf("JSON hook usage lost or overridden: tokens=%d audio=%d want=%d/%d", tokens, audioTokens, tc.tokens, tc.audioTokens)
				}
			})
		}
	}
}

func TestMediaProviderHookRejectsInvalidJSONEnvelopeAndRetainsUsage(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "body usage", true: "explicit usage"}[explicit], func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid plugin response reached adapter") }, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-json-hook", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			calls := 0
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "invalid-json", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				calls++
				body := `{"usage":{"output_tokens":7,"total_tokens":7}}upstream-fixture-secret`
				payload, err := json.Marshal(map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte(body)), "content_type": "application/json"})
				if err != nil {
					t.Fatal(err)
				}
				writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: payload}}
				if explicit {
					writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(`{"completion_tokens":3,"total_tokens":3}`)}
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/transcriptions", map[string]any{"model": "public-media"}, key)
			if response.Code != http.StatusBadGateway || calls != 1 || !strings.Contains(response.Body, "gateway_hook_response_invalid") || strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Fatalf("invalid plugin JSON accepted or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			wantTokens := int64(7)
			if explicit {
				wantTokens = 3
			}
			if usage := store.ListUsageRecords(); len(usage) != 1 || usage[0].TotalTokens != wantTokens {
				t.Fatalf("rejected plugin JSON lost usage: %+v", usage)
			}
		})
	}
}
