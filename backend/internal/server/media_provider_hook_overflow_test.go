package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaProviderHookOverflowRetainsKnownUsage(t *testing.T) {
	for _, tc := range []struct {
		name, explicitUsage string
		wantTokens          int64
	}{
		{name: "event usage fallback", wantTokens: 7},
		{name: "explicit usage overrides events", explicitUsage: `{"completion_tokens":3,"total_tokens":3}`, wantTokens: 3},
		{name: "explicit zero overrides events", explicitUsage: `{"total_tokens":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := classificationGateway(t, "http://127.0.0.1:1/v1", "http://127.0.0.1:2/v1")
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			hookCalls := 0
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "overflow", Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents, pluginmeta.DataUsage}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				hookCalls++
				events, err := json.Marshal([]gatewayStreamEventView{
					{Data: `{"usage":{"completion_tokens":0,"output_tokens":7,"total_tokens":7}}`},
					{Data: `{"audio":"` + strings.Repeat("a", 1024) + `"}`},
				})
				if err != nil {
					t.Fatal(err)
				}
				writes := map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataStreamEvents: {Value: events}}
				if tc.explicitUsage != "" {
					writes[pluginmeta.DataUsage] = pluginmeta.RawPatch{Value: json.RawMessage(tc.explicitUsage)}
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: writes}, nil
			})
			request := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			project, apiKey, err := server.authenticate(request)
			if err != nil {
				t.Fatal(err)
			}
			call, err := server.admitRoutedCall(httptest.NewRecorder(), request, project, apiKey, "classified-model", true, 0)
			if err != nil {
				t.Fatal(err)
			}
			routed, err := server.prepareAdmittedRoutedCall(request.Context(), call, call.Model.Name)
			if err != nil {
				t.Fatal(err)
			}
			_, _, usage, attempts, err := executeRoutedWithStore(request.Context(), store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (mediaResponse, Usage, error) {
				return server.invokeMediaRouteWithResponseLimit(ctx, call, route, "/audio/speech", mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"classified-model"`)}}, 128)
			})
			if err == nil || providerErrorDisposition(err) != ProviderErrorPolicy || AsHTTPError(err).Code != "gateway_hook_response_invalid" || hookCalls != 1 {
				t.Fatalf("overflow failure changed or retried: calls=%d error=%v", hookCalls, err)
			}
			if usage.TotalTokens != tc.wantTokens || usage.CompletionTokens != tc.wantTokens || !usage.MeteringInvalid {
				t.Fatalf("overflow metering = %+v, want %d known tokens and invalid metering", usage, tc.wantTokens)
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
			if tokens != tc.wantTokens || len(logged) != 1 || logged[0].TotalTokens != tc.wantTokens || logged[0].OutputTokens != tc.wantTokens {
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
