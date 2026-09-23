package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestProviderCallModePreservesAdaptersAndObservers(t *testing.T) {
	for _, endpoint := range []string{"chat", "chat-stream", "embeddings", "responses", "responses-stream"} {
		t.Run(endpoint, func(t *testing.T) {
			server, store, secret := newBackgroundResponseTestServer(t)
			if endpoint == "responses-stream" {
				server.adapterRegistry.Register(ProviderMock, responsesStreamTransformAdapter{}, AdapterCapabilityResponses, AdapterCapabilityResponseStream)
			}
			stream := strings.HasSuffix(endpoint, "-stream")
			path := "/v1/responses"
			payload := map[string]any{"model": "gpt-background", "input": "hello", "stream": stream}
			if strings.HasPrefix(endpoint, "chat") {
				path = "/v1/chat/completions"
				payload = map[string]any{"model": "gpt-background", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
			} else if endpoint == "embeddings" {
				path = "/v1/embeddings"
			}
			wrongOutput := pluginmeta.DataStreamEvents
			if stream {
				wrongOutput = pluginmeta.DataProviderResponse
			}
			observerCalls := 0
			for _, id := range []string{"observer", "wrong-mode"} {
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.provider-mode", HookID: id, Stage: pluginmeta.StageProviderCall, Priority: 2000}
				if id == "wrong-mode" {
					hook.Writes = []pluginmeta.GatewayDataClass{wrongOutput}
				}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					if id == "wrong-mode" {
						t.Error("incompatible response hook ran before the adapter")
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
					}
					observerCalls++
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
				})); err != nil {
					t.Fatal(err)
				}
			}
			response := doJSON(t, server.Handler(), http.MethodPost, path, payload, secret)
			if response.Code != http.StatusOK || observerCalls != 1 {
				t.Fatalf("adapter or observer failed: calls=%d status=%d body=%s", observerCalls, response.Code, response.Body)
			}
			if got := resourceFailureCount(t, store, "rsrc_background"); got != 0 {
				t.Fatalf("resource failure count = %d", got)
			}
		})
	}
}

func TestChatAdmissionPreservesBridgeWithoutChatCapability(t *testing.T) {
	for _, mode := range []string{"nonstream", "stream"} {
		t.Run(mode, func(t *testing.T) {
			const providerType = "plugin_declared_chat_admission_bridge"
			server, store, secret := newCodexCompatibilityRouteTestServerForProvider(t, providerType, roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return codexCompatibilityRouteResponse(t, request)
			}))
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			routes, err := store.SelectRouteCandidates(codexCompatibilityRouteModel)
			if err != nil {
				t.Fatal(err)
			}
			if server.routeSupportsAdapterCapability(routes[0], AdapterCapabilityChat) || server.routeSupportsAdapterCapability(routes[0], AdapterCapabilityChatStream) {
				t.Fatal("bridge fixture unexpectedly has native chat capability")
			}
			response := doCodexCompatibilityRouteJSON(t, server.Handler(), "/v1/chat/completions", codexCompatibilityChatPayload(mode == "stream"), secret, "admission-bridge-session")
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "bridge text") {
				t.Fatalf("bridge route rejected: %d %s", response.Code, response.Body)
			}
		})
	}
}
