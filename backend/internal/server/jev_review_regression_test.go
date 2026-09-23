package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestJevCandidateBoundarySurvivesMixedRouteStrategies(t *testing.T) {
	for _, responses := range []bool{false, true} {
		server, routed, request, _ := jevFixture(t)
		unconfigured := routed.Routes[0]
		unconfigured.ProviderModel = "unconfigured-new-model"
		unconfigured.Route.Strategy = RouteStrategyBalanced
		routed.Routes = []RouteSelection{unconfigured}
		var err error
		if responses {
			req := ResponsesRequest{Model: request.Model, Input: "task"}
			err = server.applyJevResponsesRouting(context.Background(), &routed, &req, nil)
		} else {
			err = server.applySemanticRouting(context.Background(), &routed, request, nil)
		}
		if err == nil || AsHTTPError(err).Code != "jev_no_eligible_candidates" {
			t.Fatalf("unconfigured route admitted (responses=%v): %v", responses, err)
		}
	}
}

func TestJevScopedStrategyOverrideRetainsCandidateBoundary(t *testing.T) {
	server, routed, request, _ := jevFixture(t)
	routed.Call.RoutingStrategyOverride = RouteStrategyQuality
	routed.Routes[0], routed.Routes[2] = routed.Routes[2], routed.Routes[0]
	for i := range routed.Routes {
		routed.Routes[i].Route.Strategy = RouteStrategyQuality
	}
	extra := routed.Routes[0]
	extra.ProviderModel = "not-a-candidate"
	routed.Routes = append([]RouteSelection{extra}, routed.Routes...)
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		t.Fatal("scoped override classified a request")
		return semanticDecision{}, nil
	})
	if err := server.applySemanticRouting(context.Background(), &routed, request, nil); err != nil {
		t.Fatal(err)
	}
	if len(routed.Routes) != 3 || routed.Routes[0].ProviderModel != "model_2" {
		t.Fatalf("override order or candidate set changed: %+v", routed.Routes)
	}
}

func TestJevContinuationOwnershipAfterStrategyChange(t *testing.T) {
	server, routed, _, policy := jevFixture(t)
	hits := jevHTTPUpstreams(t, server, false)
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
	})
	first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", jevHTTPBody(true, false), "thk_semantic_test")
	if first.Code != 200 {
		t.Fatal(first.Body)
	}
	policy.Strategy = RouteStrategyQuality
	policy.SemanticRouting = nil
	if _, err := server.store.UpdateModelRoutePolicy("auto-chat", policy); err != nil {
		t.Fatal(err)
	}
	var model Model
	if err := server.store.(*GormStore).db.First(&model, "name = ?", "auto-chat").Error; err != nil {
		t.Fatal(err)
	}
	saved := modelSemanticRoutingPolicy(model)
	if saved.Mode != "off" || !saved.ResponseBindingRequired {
		t.Fatalf("older client failed to disable classification while retaining binding protection: %+v", saved)
	}
	// Even an explicit minimal off policy cannot erase the server-managed marker.
	policy.SemanticRouting = &SemanticRoutingPolicy{Mode: "off", MinConfidence: 0.65}
	if _, err := server.store.UpdateModelRoutePolicy("auto-chat", policy); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.store.CreateAPIKey("prj_semantic", APIKey{ID: "other-key", Name: "Other", Status: StatusActive}, "thk_other_binding"); err != nil {
		t.Fatal(err)
	}
	body := jevHTTPBody(true, false)
	body["previous_response_id"] = "resp_model_1"
	denied := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_other_binding")
	if denied.Code != 409 || hits.Load() != 1 {
		t.Fatalf("foreign continuation reached provider: %d %s hits=%d", denied.Code, denied.Body, hits.Load())
	}
	allowed := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if allowed.Code != 200 || hits.Load() != 2 {
		t.Fatalf("owner lost continuation after strategy change: %d %s", allowed.Code, allowed.Body)
	}
	if err := server.store.(*GormStore).db.Model(&jevResponseBinding{}).Where("key_hash = ?", server.jevResponseKey(routed.Call, "resp_model_1")).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	expired := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if expired.Code != 409 || hits.Load() != 2 {
		t.Fatalf("expired continuation reached provider: %d %s", expired.Code, expired.Body)
	}
}

func TestJevResponsesBypassUnboundCache(t *testing.T) {
	for _, background := range []bool{false, true} {
		server, _, _, _ := jevFixture(t)
		hits := jevHTTPUpstreams(t, server, false)
		server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
			return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
		})
		hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-cache", HookID: "unbound-hit", Stage: pluginmeta.StageCacheLookup, Priority: 1000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatal(err)
		}
		if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			t.Error("binding-sensitive Responses reached cache lookup")
			return rawProviderCallResult(t, map[string]any{"id": "unbound-cache-id"}, Usage{}), nil
		})); err != nil {
			t.Fatal(err)
		}
		body := jevHTTPBody(true, false)
		body["background"] = background
		response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
		if response.Code != 200 {
			t.Fatal(response.Body)
		}
		if background {
			var envelope struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal([]byte(response.Body), &envelope)
			waitForResponseJobStatus(t, server.Handler(), "thk_semantic_test", envelope.ID, "completed")
		}
		if hits.Load() != 1 {
			t.Fatal("cache bypass did not reach provider")
		}
		body = jevHTTPBody(true, false)
		body["previous_response_id"] = "unknown-id"
		denied := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
		if denied.Code != 409 {
			t.Fatalf("cache bypassed continuation validation: %d %s", denied.Code, denied.Body)
		}
	}
}

func TestJevResponseHookIDsMapToOriginalUpstream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		server, _, _, _ := jevFixture(t)
		hits := jevHTTPUpstreams(t, server, false)
		server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
			return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
		})
		hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-alias", HookID: "response-alias", Stage: pluginmeta.StageResponsePost, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
		if stream {
			hook.Stage = pluginmeta.StageStreamTransform
			hook.Reads = []pluginmeta.GatewayDataClass{pluginmeta.DataStreamEvents}
			hook.Writes = hook.Reads
		}
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatal(err)
		}
		if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
			if stream {
				var event gatewayStreamEventView
				_ = json.Unmarshal(input.Data[pluginmeta.DataStreamEvents], &event)
				return streamEventPatchResult(t, map[string]any{"data": strings.ReplaceAll(event.Data, "resp_model_1", "resp_public_alias")}), nil
			}
			var response map[string]any
			if err := json.Unmarshal(input.Data[pluginmeta.DataProviderResponse], &response); err != nil {
				t.Fatal(err)
			}
			response["id"] = "resp_public_alias"
			return rawProviderResponsePatch(t, response), nil
		})); err != nil {
			t.Fatal(err)
		}
		first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", jevHTTPBody(true, stream), "thk_semantic_test")
		if first.Code != 200 || !strings.Contains(first.Body, "resp_public_alias") {
			t.Fatalf("hook alias not returned: %d %s", first.Code, first.Body)
		}
		body := jevHTTPBody(true, stream)
		body["previous_response_id"] = "resp_public_alias"
		second := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
		if second.Code != 200 || hits.Load() != 2 {
			t.Fatalf("alias continuation failed: %d %s", second.Code, second.Body)
		}
	}
}

func TestJevProtectedIDCannotBeReplayedThroughOrdinaryAlias(t *testing.T) {
	server, routed, _, _ := jevFixture(t)
	hits := jevHTTPUpstreams(t, server, false)
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
	})
	first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", jevHTTPBody(true, false), "thk_semantic_test")
	if first.Code != 200 {
		t.Fatal(first.Body)
	}
	ordinary := server.store.AddModel(Model{Name: "ordinary-chat", Modality: "chat", Status: StatusActive})
	server.store.AddRoute(ModelRoute{ModelName: ordinary.Name, ProviderID: "provider_1", ProviderModel: "model_1", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyQuality})
	if _, _, err := server.store.CreateAPIKey("prj_semantic", APIKey{ID: "other-alias-key", Name: "Other alias", Status: StatusActive}, "thk_other_alias"); err != nil {
		t.Fatal(err)
	}
	for _, expired := range []bool{false, true} {
		if expired {
			if err := server.store.(*GormStore).db.Where("upstream_id <> ?", "").Delete(&jevResponseBinding{}).Error; err != nil {
				t.Fatal(err)
			}
		}
		for _, key := range []string{"thk_semantic_test", "thk_other_alias"} {
			body := jevHTTPBody(true, false)
			body["model"] = ordinary.Name
			body["previous_response_id"] = "resp_model_1"
			result := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, key)
			if result.Code != 409 || hits.Load() != 1 {
				t.Fatalf("cross-alias replay reached provider (expired=%v): %d %s", expired, result.Code, result.Body)
			}
		}
	}
	// Native continuations on an ordinary alias still pass through when the ID
	// has never been protected by TokenHub.
	routed.Call.Model = ordinary
	for i := range routed.Routes {
		routed.Routes[i].Route.Strategy = RouteStrategyQuality
	}
	var req ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"ordinary-chat","input":"continue","previous_response_id":"native-untracked-response"}`), &req); err != nil {
		t.Fatal(err)
	}
	if err := server.applyJevResponsesRouting(context.Background(), &routed, &req, nil); err != nil {
		t.Fatalf("ordinary native continuation rejected: %v", err)
	}
}
