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

func TestMediaResponsesVendorStreamCompletionAndUsage(t *testing.T) {
	usageFrame := `"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}`
	for _, tc := range []struct{ name, stream, modality string }{
		{"wan image interleave", "data: {\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"text\",\"text\":\"landscape\"}]}]," + usageFrame + "}\n\ndata: [DONE]\n\n", "image"},
		{"minimax legacy music", "data: {\"data\":{\"audio\":\"010203\",\"status\":1}}\n\ndata: {\"data\":{\"audio\":\"040506\",\"status\":2}," + usageFrame + "}\n\n", "audio"},
		{"named responses completion", "event: response.completed\ndata: {\"response\":{\"id\":9007199254740993," + usageFrame + "}}\n\n", "video"},
		{"multiline responses completion", "event: response.completed\r\ndata: {\"response\":{\"id\":9007199254740993,\r\ndata: " + usageFrame + "}}\r\n\r\n", "video"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.stream)
			}, tc.modality)
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "landscape", "stream": true}, key)
			if response.Code != http.StatusOK || response.Body != tc.stream {
				t.Fatalf("vendor media stream changed: %d %q", response.Code, response.Body)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].StatusCode != http.StatusOK {
				t.Fatalf("successful stream recorded as failed: %+v", logs)
			}
			usage := store.ListUsageRecords()
			if len(usage) != 1 || usage[0].TotalTokens != 10 {
				t.Fatalf("vendor stream usage lost: %+v", usage)
			}
		})
	}
}

func TestMediaResponsesStreamsRejectTruncationAndRetainFailedUsage(t *testing.T) {
	for name, terminal := range map[string]string{"truncation": "", "invalid completion": "event: response.completed\ndata: invalid\n\n", "terminal failure": "event: response.failed\ndata: {\"error\":{\"message\":\"upstream-fixture-secret\"}}\n\n"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":7,\"total_tokens\":10}}\n\n"+terminal)
			}, "image")
			store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "landscape", "stream": true}, key)
			if calls != 1 || strings.Contains(response.Body, "upstream-fixture-secret") {
				t.Fatalf("failed stream retried or leaked credential: calls=%d response=%s", calls, response.Body)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].StatusCode == http.StatusOK || strings.Contains(logs[0].ErrorCode, "upstream-fixture-secret") {
				t.Fatalf("failed stream recorded incorrectly: %+v", logs)
			}
			usage := store.ListUsageRecords()
			if len(usage) != 1 || usage[0].TotalTokens != 10 {
				t.Fatalf("partial usage lost: %+v", usage)
			}
		})
	}
}

func TestMediaResponsesStreamPreservesNumbersAndTextCompletionRules(t *testing.T) {
	stream := "data: {\"type\":\"response.completed\",\"response\":{\"id\":9007199254740993}}\n\n"
	response, _, _, err := consumeMediaResponsesStream(Provider{}, strings.NewReader(stream), nil)
	if err != nil || response["id"] != json.Number("9007199254740993") {
		t.Fatalf("vendor stream response ID changed: %v, error: %v", response, err)
	}
	_, _, _, err = consumeRoutedResponsesStream(CallContext{Model: Model{Modality: "chat"}}, Provider{}, strings.NewReader("data: [DONE]\n\n"), nil)
	if err == nil || AsHTTPError(err).Code != "codex_stream_incomplete" {
		t.Fatalf("text Responses completion contract changed: %v", err)
	}
}

func TestMediaResponsesStreamHooksAllowLargeImageEvents(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"output\":\""+strings.Repeat("A", maxSSEEventBytes+1)+"\",\"usage\":{\"output_tokens\":7,\"total_tokens\":7}}\n\ndata: [DONE]\n\n")
	}, "image")
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-responses-stream", HookID: "image-event", Stage: pluginmeta.StageStreamTransform, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{"model": "public-media", "input": "landscape", "stream": true}, key)
	if response.Code != http.StatusOK || !strings.HasSuffix(response.Body, "data: [DONE]\n\n") {
		t.Fatalf("large media event failed: status=%d bytes=%d", response.Code, len(response.Body))
	}
	if records := store.ListUsageRecords(); len(records) != 1 || records[0].TotalTokens != 7 {
		t.Fatalf("large media event lost usage: %+v", records)
	}
}
