package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func jevFixture(t *testing.T) (*Server, RoutedCall, ChatCompletionRequest, ModelRoutePolicy) {
	t.Helper()
	server, routed, request := semanticFixture(t)
	policy := ModelRoutePolicy{Strategy: RouteStrategyJev, SemanticRouting: &SemanticRoutingPolicy{Mode: "enforce", MinConfidence: 0.65, Instructions: "Use the configured task criteria.", DefaultCandidateID: "choice_0"}}
	for i, route := range routed.Routes {
		policy.Routes = append(policy.Routes, ModelRoutePolicyRoute{RouteID: route.Route.ID, Weight: 100, QualityScore: 50, CostScore: 50})
		policy.SemanticRouting.Candidates = append(policy.SemanticRouting.Candidates, SemanticRoutingCandidate{ID: fmt.Sprintf("choice_%d", i), ProviderID: route.Provider.ID, ProviderModel: route.ProviderModel, Criteria: fmt.Sprintf("Handle synthetic task category %d", i)})
	}
	result := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/model-routing-policies/auto-chat", policy, "")
	if result.Code != 200 {
		t.Fatalf("configure Jev: %d %s", result.Code, result.Body)
	}
	for i := range routed.Routes {
		routed.Routes[i].Route.Strategy = RouteStrategyJev
	}
	for _, model := range server.store.ListModels() {
		if model.Name == request.Model {
			routed.Call.Model = model
		}
	}
	return server, routed, request, policy
}

func TestJevStrategySelectsAcrossPriorityTiers(t *testing.T) {
	server, routed, req, _ := jevFixture(t)
	routed.Routes[1].Route.Priority = 9
	routed.Routes[1].Resource = &ProviderResource{ID: "primary", Priority: 8}
	duplicate := routed.Routes[1]
	duplicate.Resource = &ProviderResource{ID: "backup", Priority: 10}
	routed.Routes = append(routed.Routes, duplicate)
	server.semanticRouter = semanticTestEvaluator(func(_ context.Context, text string, candidates []semanticCandidate, instructions string) (semanticDecision, error) {
		if text != "synthetic coding question" || instructions != "Use the configured task criteria." || len(candidates) != 3 || candidates[1].Criteria != "Handle synthetic task category 1" {
			t.Fatalf("incorrect classification input: %q %q %+v", text, instructions, candidates)
		}
		return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
	})
	if err := server.applySemanticRouting(context.Background(), &routed, req, nil); err != nil {
		t.Fatal(err)
	}
	if got := []string{routeResourceID(routed.Routes[0]), routeResourceID(routed.Routes[1]), routed.Routes[2].ProviderModel}; !reflect.DeepEqual(got, []string{"primary", "backup", "model_0"}) {
		t.Fatalf("incorrect selected model/resource order: %v", got)
	}
}

func TestJevStrategyFallbackAndPermissions(t *testing.T) {
	for _, kind := range []string{"timeout", "low confidence", "unknown", "no preference", "disabled", "filtered default", "incompatible default"} {
		t.Run(kind, func(t *testing.T) {
			server, routed, req, _ := jevFixture(t)
			want := "model_0"
			if kind == "disabled" {
				server.config.SemanticRoutingEnabled = false
			}
			if kind == "filtered default" {
				routed.Routes = routed.Routes[1:]
				want = "model_1"
			}
			if kind == "incompatible default" {
				req.MaxTokens = 128
				want = "model_1"
				if err := server.store.(*GormStore).db.Model(&ProviderModel{}).Where("provider_id = ?", "provider_0").Update("supported_parameters", `["max_output_tokens"]`).Error; err != nil {
					t.Fatal(err)
				}
			}
			server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, candidates []semanticCandidate, _ string) (semanticDecision, error) {
				if kind == "disabled" {
					t.Fatal("disabled strategy made an external call")
				}
				if want == "model_1" {
					for _, candidate := range candidates {
						if candidate.Model == "model_0" {
							t.Fatal("filtered candidate sent to Jev")
						}
					}
				}
				switch kind {
				case "low confidence":
					return semanticDecision{Choice: "choice_2", Confidence: 0.1}, nil
				case "unknown":
					return semanticDecision{Choice: "injected", Confidence: 1}, nil
				case "no preference":
					return semanticDecision{Choice: "no_preference", Confidence: 1}, nil
				}
				return semanticDecision{}, errors.New("evaluator unavailable")
			})
			if err := server.applySemanticRouting(context.Background(), &routed, req, nil); err != nil {
				t.Fatal(err)
			}
			if routed.Routes[0].ProviderModel != want {
				t.Fatalf("fallback=%s, want %s", routed.Routes[0].ProviderModel, want)
			}
		})
	}
}

func TestJevStrategyPolicyRejectsInvalidCandidatesAtomically(t *testing.T) {
	for _, kind := range []string{"missing policy", "empty criteria", "unknown model", "duplicate model", "unknown default", "empty instructions"} {
		t.Run(kind, func(t *testing.T) {
			server, _, _, policy := jevFixture(t)
			before := server.store.ListRoutes()
			switch kind {
			case "missing policy":
				policy.SemanticRouting = nil
			case "empty criteria":
				policy.SemanticRouting.Candidates[0].Criteria = " "
			case "unknown model":
				policy.SemanticRouting.Candidates[0].ProviderModel = "unconfigured"
			case "duplicate model":
				policy.SemanticRouting.Candidates[1].ProviderID = policy.SemanticRouting.Candidates[0].ProviderID
				policy.SemanticRouting.Candidates[1].ProviderModel = policy.SemanticRouting.Candidates[0].ProviderModel
			case "unknown default":
				policy.SemanticRouting.DefaultCandidateID = "missing"
			case "empty instructions":
				policy.SemanticRouting.Instructions = ""
			}
			policy.Routes[0].Weight = 777
			result := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/model-routing-policies/auto-chat", policy, "")
			if result.Code != 400 || !reflect.DeepEqual(before, server.store.ListRoutes()) {
				t.Fatalf("invalid update was not atomic: %d %s", result.Code, result.Body)
			}
		})
	}
}

func jevHTTPUpstreams(t *testing.T, server *Server, failSelected bool) *atomic.Int32 {
	t.Helper()
	hits := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		model, _ := body["model"].(string)
		if model == "model_1" {
			hits.Add(1)
			if failSelected {
				w.WriteHeader(429)
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/responses") {
			if previous, ok := body["previous_response_id"]; ok && previous != "resp_model_1" {
				t.Errorf("incorrect upstream continuation ID: %v", previous)
			}
			if body["max_output_tokens"] != float64(128) || body["max_tokens"] != nil || body["instructions"] != "private instructions" || body["tools"] == nil {
				t.Errorf("Responses request changed: %+v", body)
			}
			response := map[string]any{"id": "resp_" + model, "object": "response", "model": model, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7}}
			if body["stream"] == true {
				w.Header().Set("Content-Type", "text/event-stream")
				data, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
				_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", data)
			} else {
				_ = json.NewEncoder(w).Encode(response)
			}
		} else {
			if body["max_tokens"] != float64(128) || body["tools"] == nil {
				t.Errorf("Chat parameters changed: %+v", body)
			}
			if body["stream"] == true {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"id\":\"chat_test\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n", model)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_test", "model": model, "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}})
			}
		}
	}))
	t.Cleanup(upstream.Close)
	if err := server.store.(*GormStore).db.Model(&Provider{}).Where("id IN ?", []string{"provider_0", "provider_1", "provider_2"}).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
		t.Fatal(err)
	}
	return hits
}

func jevHTTPBody(responses, stream bool) map[string]any {
	if responses {
		return map[string]any{"model": "auto-chat", "input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "synthetic task"}}}}, "instructions": "private instructions", "max_output_tokens": 128, "stream": stream, "tools": []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}}}}
	}
	return map[string]any{"model": "auto-chat", "messages": []any{map[string]any{"role": "system", "content": "private instructions"}, map[string]any{"role": "user", "content": "synthetic task"}}, "max_tokens": 128, "stream": stream, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}}
}

func TestJevHTTPChatResponsesStreamAndFailover(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("responses_%v_stream_%v_failover_%v", responses, stream, fail), func(t *testing.T) {
					server, _, _, _ := jevFixture(t)
					hits := jevHTTPUpstreams(t, server, fail)
					calls := 0
					server.semanticRouter = semanticTestEvaluator(func(_ context.Context, text string, _ []semanticCandidate, _ string) (semanticDecision, error) {
						calls++
						if text != "synthetic task" {
							t.Errorf("unexpected classifier text %q", text)
						}
						return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
					})
					path := "/v1/chat/completions"
					if responses {
						path = "/v1/responses"
					}
					result := doJSON(t, server.Handler(), http.MethodPost, path, jevHTTPBody(responses, stream), "thk_semantic_test")
					if result.Code != 200 || calls != 1 || hits.Load() != 1 {
						t.Fatalf("status=%d calls=%d hits=%d: %s", result.Code, calls, hits.Load(), result.Body)
					}
					want := "model_1"
					if fail {
						want = "model_0"
					}
					logs := server.store.ListRequestLogs()
					if len(logs) != 1 || logs[0].ProviderModel != want {
						t.Fatalf("wrong executed model: %+v", logs)
					}
				})
			}
		}
	}
}

func TestJevResponsesContinuationPinsSelectedRoute(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("initial_stream_%v", stream), func(t *testing.T) {
			server, _, _, _ := jevFixture(t)
			hits := jevHTTPUpstreams(t, server, false)
			calls := 0
			server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
				calls++
				return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
			})
			first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", jevHTTPBody(true, stream), "thk_semantic_test")
			if first.Code != 200 {
				t.Fatal(first.Body)
			}
			body := jevHTTPBody(true, false)
			body["previous_response_id"] = "resp_model_1"
			second := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
			if second.Code != 200 || calls != 1 || hits.Load() != 2 {
				t.Fatalf("continuation reclassified or switched: %d calls=%d hits=%d %s", second.Code, calls, hits.Load(), second.Body)
			}
			_, _, err := server.store.CreateAPIKey("prj_semantic", APIKey{ID: "key_other", Name: "Other", Status: StatusActive}, "thk_other_test")
			if err != nil {
				t.Fatal(err)
			}
			denied := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_other_test")
			if denied.Code != 409 || hits.Load() != 2 {
				t.Fatalf("cross-key continuation reached upstream: %d %s", denied.Code, denied.Body)
			}
			if err := server.store.(*GormStore).db.Model(&Provider{}).Where("id = ?", "provider_1").Update("status", StatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
			unavailable := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
			if unavailable.Code != 409 || calls != 1 {
				t.Fatalf("unavailable continuation silently switched: %d %s", unavailable.Code, unavailable.Body)
			}
		})
	}
}
