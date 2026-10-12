package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaJSONErrorEnvelopeIsNotSuccessful(t *testing.T) {
	for _, source := range []string{"adapter", "provider hook", "response hook", "guardrail hook"} {
		for _, errorValue := range []string{`{"message":"upstream-fixture-secret","type":"server_error"}`, `"upstream-fixture-secret"`} {
			t.Run(source+"/"+errorValue, func(t *testing.T) {
				failure := `{"error":` + errorValue + `,"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					if source == "adapter" {
						_, _ = io.WriteString(w, failure)
					} else {
						_, _ = io.WriteString(w, `{"data":[],"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`)
					}
				}, "image")
				store.AddRoute(ModelRoute{ID: "fallback-json-error", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
				if source == "provider hook" {
					registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "error-envelope", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						calls++
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(failure)}}}, nil
					})
				} else if source != "adapter" {
					stage := pluginmeta.StageResponsePost
					if source == "guardrail hook" {
						stage = pluginmeta.StageGuardrailPost
					}
					hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-json-error", HookID: "error-envelope", Stage: stage, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
					if err := server.gatewayChain.RegisterHook(hook); err != nil {
						t.Fatal(err)
					}
					if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						return rawProviderResponsePatch(t, json.RawMessage(failure)), nil
					})); err != nil {
						t.Fatal(err)
					}
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "prompt": "fixture"}, key)
				if response.Code != http.StatusBadGateway || calls != 1 {
					t.Errorf("JSON error accepted or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				logs := store.ListRequestLogs()
				if len(logs) != 1 || logs[0].StatusCode != http.StatusBadGateway {
					t.Errorf("JSON error recorded as success: %+v", logs)
				}
				encodedLogs, _ := json.Marshal(logs)
				if strings.Contains(response.Body+string(encodedLogs), "upstream-fixture-secret") {
					t.Error("JSON error leaked provider credential")
				}
				if usage := store.ListUsageRecords(); len(usage) != 1 || usage[0].TotalTokens != 10 {
					t.Errorf("JSON error lost known usage: %+v", usage)
				}
			})
		}
	}
}

func TestMediaJSONNullErrorPreservesSuccessfulResponse(t *testing.T) {
	body := `{"error":null,"id":9007199254740993,"data":[{"url":"https://example.com/error.png"}],"usage":{"total_tokens":10,"output_tokens":10}}`
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}, "image")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{"model": "public-media", "prompt": "fixture"}, key)
	if response.Code != http.StatusOK || response.Body != body {
		t.Fatalf("null error changed success: status=%d body=%s", response.Code, response.Body)
	}
	if usage := store.ListUsageRecords(); len(usage) != 1 || usage[0].TotalTokens != 10 {
		t.Fatalf("success usage changed: %+v", usage)
	}
}
