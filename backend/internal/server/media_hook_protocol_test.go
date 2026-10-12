package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaImagePolicyHooksUseEndpointProtocol(t *testing.T) {
	for _, endpoint := range []string{"images/generations", "images/edits", "images/variations"} {
		for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageGuardrailPre, pluginmeta.StageProviderCall, pluginmeta.StageGuardrailPost} {
			t.Run(endpoint+"/"+string(stage), func(t *testing.T) {
				upstreamCalls, hookCalls := 0, 0
				server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					upstreamCalls++
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"data":[]}`)
				}, "image")
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.media-image-protocol", HookID: "deny", Stage: stage, Priority: 1000, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{endpoint}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					if input.Envelope.RouteProtocol != endpoint {
						t.Errorf("hook protocol = %q, want %q", input.Envelope.RouteProtocol, endpoint)
					}
					return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
				})); err != nil {
					t.Fatal(err)
				}
				response := doJSON(t, server.Handler(), http.MethodPost, "/v1/"+endpoint, map[string]any{"model": "public-media", "prompt": "fixture"}, key)
				wantUpstream := 0
				if stage == pluginmeta.StageGuardrailPost {
					wantUpstream = 1
				}
				if response.Code != http.StatusForbidden || hookCalls != 1 || upstreamCalls != wantUpstream {
					t.Fatalf("endpoint policy bypass: status=%d hooks=%d upstream=%d body=%s", response.Code, hookCalls, upstreamCalls, response.Body)
				}
			})
		}
	}
}

func TestMediaImageHookOnlyRoutesUseEndpointProtocol(t *testing.T) {
	for _, endpoint := range []string{"images/generations", "images/edits", "images/variations"} {
		t.Run(endpoint, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("handled request reached upstream") }, "image")
			if _, err := store.UpdateProvider("media-provider", Provider{Type: "media-hook-only", Healthy: true}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "image-response", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{endpoint}}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				calls++
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"data":[{"url":"https://example.com/image.png"}]}`)}}}, nil
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/"+endpoint, map[string]any{"model": "public-media", "prompt": "fixture"}, key)
			if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("endpoint hook route was not admitted: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestManagedImageEditRetainsGenerationHookProtocol(t *testing.T) {
	server, _, key := newNativeCodexImageEditTestServer(t)
	calls := 0
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.managed-image-protocol", HookID: "deny", Stage: pluginmeta.StageGuardrailPre, Priority: 1000, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolImageGeneration}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		calls++
		if input.Envelope.RouteProtocol != providerRouteProtocolImageGeneration {
			t.Errorf("managed edit hook protocol changed: %q", input.Envelope.RouteProtocol)
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
	})); err != nil {
		t.Fatal(err)
	}
	response := doMultipartImageEditWithCount(t, server.Handler(), key, realPNGFixture(t), 1)
	if response.Code != http.StatusForbidden || calls != 1 {
		t.Fatalf("managed edit hook changed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}
