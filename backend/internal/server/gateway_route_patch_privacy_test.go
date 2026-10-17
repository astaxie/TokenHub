package server

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestSensitiveRoutePatchFailuresDoNotLogPayload(t *testing.T) {
	for _, endpoint := range []string{"audio/transcriptions", "audio/translations", "rerank"} {
		for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageRouteCandidates, pluginmeta.StageRouteRank} {
			t.Run(endpoint+"/"+string(stage), func(t *testing.T) {
				const private = "private-routing-patch-payload"
				upstreamCalls := 0
				handler := func(w http.ResponseWriter, _ *http.Request) {
					upstreamCalls++
					if endpoint == "rerank" {
						writeJSON(w, http.StatusOK, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.9}}})
						return
					}
					writeJSON(w, http.StatusOK, map[string]string{"text": "fixture"})
				}
				var app *Server
				var store *GormStore
				var key, model, provider string
				if endpoint == "rerank" {
					app, store, key = newRerankCompletionFixture(t, handler)
					model, provider = "rank-complete", "rank-complete"
					store.AddProviderModel(ProviderModel{ProviderID: provider, UpstreamModel: "alternate", Modality: "rerank", InputPriceUSDPer1M: 1})
				} else {
					app, store, key = newMediaGatewayFixture(t, handler, "audio")
					model, provider = "public-media", "media-provider"
				}
				store.AddRoute(ModelRoute{ID: "route-patch-alternate", ModelName: model, ProviderID: provider, ProviderModel: "alternate", Status: StatusActive, Priority: 2, Weight: 100})
				hook := pluginmeta.GatewayHookDescriptor{
					PluginID: "test.route-patch-privacy", HookID: "invalid", Stage: stage, Priority: 2000,
					Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataRouteCandidates}, FailurePolicy: pluginmeta.FailurePolicyFailOpen,
					Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{endpoint}},
				}
				if err := app.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				hookCalls := 0
				if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					hookCalls++
					// A valid JSON patch reaches host validation after the hook returns.
					return routeRankPatchResult(t, private, "route-patch-alternate"), nil
				})); err != nil {
					t.Fatal(err)
				}
				var logs bytes.Buffer
				original := log.Writer()
				log.SetOutput(&logs)
				t.Cleanup(func() { log.SetOutput(original) })
				var status int
				if endpoint == "rerank" {
					status = doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": model, "query": private, "documents": []string{private}}, key).Code
				} else {
					status = doMediaMultipartReviewRequest(t, app, key, "/v1/"+endpoint, [][2]string{{"model", model}, {"prompt", private}}).Code
				}
				if status != http.StatusOK || hookCalls != 1 || upstreamCalls != 1 {
					t.Fatalf("invalid fail-open patch changed routing: status=%d hooks=%d upstream=%d", status, hookCalls, upstreamCalls)
				}
				if !strings.Contains(logs.String(), "gateway "+string(stage)+" hook") {
					t.Fatal("invalid route patch did not produce a diagnostic")
				}
				if strings.Contains(logs.String(), private) {
					t.Fatal("invalid route patch leaked private content into diagnostics")
				}
			})
		}
	}
}
