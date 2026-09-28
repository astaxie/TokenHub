package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaResponsesFailedStatusRedactsSecretsAndRetainsAccounting(t *testing.T) {
	const prefix = "data: {\"data\":{\"audio\":\"010203\",\"status\":1},\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}\n\n"
	const failed = `data: {"status":"failed","message":"Music failed for upstream-fixture-secret and \u0068eader-fixture-secret","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}` + "\n\n"
	for _, tc := range []struct{ name, before, after string }{
		{"failure only", "", ""},
		{"after audio", prefix, ""},
		{"before done", prefix, "data: [DONE]\n\n"},
		{"before completion", prefix, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
	} {
		for _, withHook := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "direct", true: "response hook"}[withHook], func(t *testing.T) {
				calls, hookCalls := 0, 0
				failure := failed
				if tc.before != "" {
					failure = strings.ReplaceAll(failed, `,"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}`, "")
				}
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Header.Get("X-Tenant-Secret") != "header-fixture-secret" {
						t.Errorf("sensitive provider header was not configured: %q", r.Header.Get("X-Tenant-Secret"))
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, tc.before+failure+tc.after)
				}, "audio")
				provider, ok := store.GetProvider("media-provider")
				if !ok {
					t.Fatal("fixture provider missing")
				}
				provider.Headers = map[string]string{"X-Tenant-Secret": "header-fixture-secret"}
				provider.SensitiveHeaders = []string{"X-Tenant-Secret"}
				if _, err := store.UpdateProvider(provider.ID, provider); err != nil {
					t.Fatal(err)
				}
				store.AddRoute(ModelRoute{ID: "fallback-music-status", ModelName: "public-media", ProviderID: provider.ID, ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				if withHook {
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-music-status", HookID: "observe", Stage: pluginmeta.StageResponsePost, Priority: 1000, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
					if err := server.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						hookCalls++
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
					})); err != nil {
						t.Fatal(err)
					}
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "Folk music", "lyrics": "A calm morning", "stream": true}, key)
				if calls != 1 || withHook && hookCalls == 0 {
					t.Fatalf("failed music was resubmitted or bypassed hooks: calls=%d hooks=%d", calls, hookCalls)
				}
				if !strings.HasPrefix(response.Body, tc.before) || !strings.Contains(response.Body, `"status":"failed"`) || strings.Contains(response.Body, "[DONE]") || strings.Contains(response.Body, "response.completed") {
					t.Fatalf("failure frame or earlier media changed, or completion was forwarded: %s", response.Body)
				}
				logs := store.ListRequestLogs()
				if len(logs) != 1 || logs[0].StatusCode != http.StatusBadGateway || logs[0].ErrorCode != "provider_stream_error" {
					t.Fatalf("documented failure status was not recorded as a provider error: %+v", logs)
				}
				var attempts []RouteAttemptLog
				if err := store.db.Find(&attempts).Error; err != nil {
					t.Fatal(err)
				}
				if len(attempts) != 1 || attempts[0].StatusCode != http.StatusBadGateway || attempts[0].ErrorCode != "provider_stream_error" || attempts[0].ErrorMessage != "Media provider reported a failed stream" || attempts[0].InputTokens != 2 || attempts[0].OutputTokens != 3 || attempts[0].TotalTokens != 5 {
					t.Fatalf("failed attempt lost its generic error or accounting: %+v", attempts)
				}
				if records := store.ListUsageRecords(); len(records) != 1 || records[0].InputTokens != 2 || records[0].OutputTokens != 3 || records[0].TotalTokens != 5 {
					t.Fatalf("failed stream lost reported usage: %+v", records)
				}
				audit, err := json.Marshal(map[string]any{"requests": logs, "attempts": attempts})
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"upstream-fixture-secret", "header-fixture-secret", `\u0068eader-fixture-secret`} {
					if strings.Contains(response.Body, secret) || strings.Contains(string(audit), secret) {
						t.Errorf("failed stream leaked a provider credential: %q", secret)
					}
				}
			})
		}
	}
}

func TestMediaResponsesFailedStatusKeepsValidFramesAndTextSemantics(t *testing.T) {
	for _, tc := range []struct{ name, modality, stream string }{
		{"media in progress", "audio", ": heartbeat\r\n\r\ndata: {\"status\":\"in_progress\",\"message\":\"literal upstream-fixture-secret\"} \r\n\r\ndata: [DONE]\r\n\r\n"},
		{"standard media response", "image", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"error\":null}}\n\n"},
		{"text status event unchanged", "chat", "data: {\"status\":\"failed\",\"message\":\"literal upstream-fixture-secret\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output strings.Builder
			_, _, _, err := consumeRoutedResponsesStream(CallContext{Model: Model{Modality: tc.modality}}, Provider{APIKey: "upstream-fixture-secret"}, strings.NewReader(tc.stream), &output)
			if err != nil || output.String() != tc.stream {
				t.Fatalf("valid stream changed: error=%v output=%q", err, output.String())
			}
		})
	}
}

func TestMediaResponsesNonstreamFailedTaskRemainsProviderData(t *testing.T) {
	const body = `{"status":"failed","message":"The requested vendor task failed","id":"vendor_task_fixture"}`
	server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}, "video")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "vendor_task_fixture"}, key)
	var got, want any
	if err := decodeResponsesJSON([]byte(response.Body), &got); err != nil {
		t.Fatal(err)
	}
	if err := decodeResponsesJSON([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("non-stream task query changed: %d %s", response.Code, response.Body)
	}
}
