package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaPostHookRejectsDataAfterBase64Padding(t *testing.T) {
	for _, separator := range []string{"", "\n", strings.Repeat("\r\n", 1024)} {
		for _, tail := range []string{"AAAA", "Yg=="} {
			data, err := encodeMediaPostHookResponse(map[string]any{"data_base64": "YQ==" + separator + tail}, false, 64)
			if err == nil || AsHTTPError(err).Code != "gateway_hook_response_invalid" || len(data) != 0 {
				t.Errorf("trailing base64 data accepted: separator bytes=%d tail=%q data=%v error=%v", len(separator), tail, data, err)
			}
		}
	}
	separator := strings.Repeat("\r\n", 1024)
	for _, encoded := range []string{"YQ==" + separator, "YQ" + separator + "==", "YQ=" + separator + "="} {
		data, err := encodeMediaPostHookResponse(map[string]any{"data_base64": encoded}, false, 1)
		if err != nil || string(data) != "a" {
			t.Errorf("valid line-wrapped padding rejected: bytes=%d data=%v error=%v", len(encoded), data, err)
		}
	}
}

func TestMediaPostHookRejectsPaddedBinarySegments(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		t.Run(string(stage), func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = io.WriteString(w, "audio")
			}, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-media-route", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "other-media", Status: StatusActive, Priority: 2, Weight: 100})
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-base64", HookID: "invalid-binary", Stage: stage, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				return rawProviderResponsePatch(t, map[string]any{"data_base64": "YQ==" + strings.Repeat("\r\n", 1024) + "AAAA"}), nil
			})); err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/audio/speech", map[string]any{"model": "public-media", "input": "Hello"}, key)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body, "gateway_hook_response_invalid") || calls != 1 {
				t.Fatalf("invalid binary output accepted or retried: status=%d calls=%d body=%q", response.Code, calls, response.Body)
			}
			if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].StatusCode != http.StatusBadGateway {
				t.Fatalf("invalid binary output recorded as success: %+v", logs)
			}
		})
	}
}
