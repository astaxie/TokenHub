package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionLocalBoundsPreserveResourceHealth(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, transformation := range []string{"guardrail", "hook patch"} {
			for _, health := range []string{"healthy", "half-open"} {
				t.Run(path+"/"+transformation+"/"+health, func(t *testing.T) {
					upstreamCalls, providerHookCalls := 0, 0
					app, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {
						upstreamCalls++
					}, "audio")
					store.failureThreshold = 2
					for _, id := range []string{"audio-bounds-primary", "audio-bounds-fallback"} {
						if _, err := store.AddProviderResource(ProviderResource{
							ID: id, Name: id, ProviderID: "media-provider", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100,
						}); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := store.UpdateRoute("media-route", ModelRoute{ProviderResourceID: "audio-bounds-primary", ProviderModel: "longer-upstream-model"}); err != nil {
						t.Fatal(err)
					}
					store.AddRoute(ModelRoute{ID: "audio-bounds-fallback", ModelName: "public-media", ProviderID: "media-provider", ProviderResourceID: "audio-bounds-fallback", ProviderModel: "vendor-media", Status: StatusActive, Priority: 2, Weight: 100})
					if health == "half-open" {
						tripBreaker(t, store, "audio-bounds-primary")
						expireCooldown(t, store, "audio-bounds-primary")
					} else {
						store.FinishProviderResourceAttempt(context.Background(), "audio-bounds-primary", "", AttemptFailed, Usage{})
					}
					before := findResource(t, store, "audio-bounds-primary")
					registerMediaProviderTestHook(t, app, pluginmeta.GatewayHookDescriptor{HookID: "observe-bounds"}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						providerHookCalls++
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
					})
					body, contentType := transcriptionGuardrailMultipart(t, strings.Repeat("x", 100)+" a@b.co", "")
					app.config.MaxMultimodalRequestBytes = int64(len(body))
					status, code := http.StatusBadGateway, "gateway_hook_patch_invalid"
					if transformation == "guardrail" {
						createTranscriptionMaskPolicy(t, store)
						app.config.MaxMultimodalRequestBytes += 4 // Masking expands the email from six bytes to ten.
						status, code = http.StatusRequestEntityTooLarge, "request_too_large"
					} else {
						registerTranscriptionBoundsPatch(t, app)
					}
					response := sendTranscriptionGuardrailMultipart(app, key, path, body, contentType)
					if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) || upstreamCalls != 0 || providerHookCalls != 0 {
						t.Fatalf("local bounds rejection changed or reached invocation: status=%d upstream=%d hooks=%d body=%s", response.Code, upstreamCalls, providerHookCalls, response.Body)
					}
					var attempts []RouteAttemptLog
					if err := store.db.Find(&attempts).Error; err != nil {
						t.Fatal(err)
					}
					if len(attempts) != 1 || attempts[0].ProviderResourceID != "audio-bounds-primary" {
						t.Fatalf("local bounds rejection retried or bypassed the primary: %+v", attempts)
					}
					after := findResource(t, store, "audio-bounds-primary")
					if after.FailureCount != before.FailureCount || after.Healthy != before.Healthy {
						t.Fatalf("local rejection changed resource health: failures=%d want=%d healthy=%t want=%t", after.FailureCount, before.FailureCount, after.Healthy, before.Healthy)
					}
					if health == "half-open" {
						if after.CooldownUntil == nil || !after.CooldownUntil.After(*before.CooldownUntil) {
							t.Fatalf("half-open trial was not claimed or its cooldown was cleared: before=%v after=%v", before.CooldownUntil, after.CooldownUntil)
						}
					} else if after.CooldownUntil != nil {
						t.Fatalf("local rejection cooled down a healthy resource: %v", after.CooldownUntil)
					}
					if failures := resourceFailureCount(t, store, "audio-bounds-fallback"); failures != 0 {
						t.Fatalf("local rejection penalized the fallback: failures=%d", failures)
					}
				})
			}
		}
	}
}

func registerTranscriptionBoundsPatch(t *testing.T, app *Server) {
	t.Helper()
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID: "test.audio-bounds-health", HookID: "preserve-request", Stage: pluginmeta.StageDecodeNormalize, Priority: 2000,
		Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody}, FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataRequestBody: {Value: input.Data[pluginmeta.DataRequestBody]},
		}}, nil
	})); err != nil {
		t.Fatal(err)
	}
}
