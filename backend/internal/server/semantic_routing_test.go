package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type semanticTestEvaluator func(context.Context, string, []semanticCandidate, string) (semanticDecision, error)

func (f semanticTestEvaluator) Evaluate(ctx context.Context, text string, candidates []semanticCandidate, instructions string) (semanticDecision, error) {
	return f(ctx, text, candidates, instructions)
}

func semanticFixture(t *testing.T) (*Server, RoutedCall, ChatCompletionRequest) {
	t.Helper()
	store := NewMemoryStore()
	policy, _ := json.Marshal(SemanticRoutingPolicy{Mode: "enforce", MinConfidence: 0.65})
	model := store.AddModel(Model{Name: "auto-chat", Modality: "chat", Status: StatusActive, Metadata: map[string]string{semanticRoutingMetadataKey: string(policy), "owner": "synthetic"}})
	project := store.CreateProject(Project{ID: "prj_semantic", Name: "Semantic test", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_semantic", Name: "Test", Status: StatusActive}, "thk_semantic_test")
	if err != nil {
		t.Fatal(err)
	}
	routes := []RouteSelection{}
	for index := range 3 {
		id := fmt.Sprintf("%d", index)
		provider := store.AddProvider(Provider{ID: "provider_" + id, Name: "Provider " + id, Type: ProviderMock, Healthy: true, Status: StatusActive, APIKey: "provider-secret"})
		store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "model_" + id, Status: StatusActive, Capabilities: []string{"text"}, Metadata: map[string]string{"routing_description": "Synthetic coding capability", "secret": "must-not-egress"}})
		route := store.AddRoute(ModelRoute{ID: "route_" + id, ModelName: model.Name, ProviderID: provider.ID, ProviderModel: "model_" + id, Status: StatusActive, Priority: 1, Weight: 100, QualityScore: 90 - index*10, CostScore: 50, Strategy: RouteStrategyQuality})
		routes = append(routes, RouteSelection{Provider: provider, ProviderModel: route.ProviderModel, Route: route})
	}
	config := Config{AdminToken: "dev_admin_token", SemanticRoutingEnabled: true, SemanticRoutingProjects: []string{project.ID}, TypeSafeAPIKey: "test-evaluator-secret", SemanticRoutingTimeoutMS: 1000}
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	routed := RoutedCall{Call: CallContext{RequestID: "req_semantic", Project: project, Key: key, Model: model}, Routes: routes}
	req := ChatCompletionRequest{Model: model.Name, Messages: []ChatMessage{{Role: "system", Content: "private-system-text"}, {Role: "user", Content: "synthetic coding question"}}}
	return server, routed, req
}

func TestSemanticRoutingModesAndFailureFallback(t *testing.T) {
	for _, test := range []struct {
		name, mode, choice string
		confidence         float64
		failure            bool
		wantFirst          string
		wantReason         string
	}{
		{"enforce", "enforce", "candidate_2", 0.8, false, "route_1", "applied"},
		{"shadow", "shadow", "candidate_2", 0.8, false, "route_0", "shadow"},
		{"off", "off", "candidate_2", 0.8, false, "route_0", ""},
		{"threshold boundary", "enforce", "candidate_2", 0.65, false, "route_1", "applied"},
		{"low confidence", "enforce", "candidate_2", 0.64, false, "route_0", "low_confidence"},
		{"no preference", "enforce", "no_preference", 0.9, false, "route_0", "no_preference"},
		{"unknown choice", "enforce", "arbitrary_provider", 0.9, false, "route_0", "invalid_decision"},
		{"upstream error", "enforce", "", 0, true, "route_0", "evaluator_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, routed, req := semanticFixture(t)
			raw, _ := json.Marshal(SemanticRoutingPolicy{Mode: test.mode, MinConfidence: 0.65})
			routed.Call.Model.Metadata[semanticRoutingMetadataKey] = string(raw)
			calls := 0
			server.semanticRouter = semanticTestEvaluator(func(_ context.Context, text string, c []semanticCandidate, _ string) (semanticDecision, error) {
				calls++
				if text != "synthetic coding question" || len(c) != 3 {
					t.Errorf("unexpected egress payload")
				}
				data, _ := json.Marshal(c)
				if strings.Contains(string(data), "secret") {
					t.Error("credential included in candidates")
				}
				if test.failure {
					return semanticDecision{}, errors.New("secret upstream prompt text")
				}
				return semanticDecision{Choice: test.choice, Confidence: test.confidence, Model: "jev-1.13.0"}, nil
			})
			if err := server.applySemanticRouting(context.Background(), &routed, req, nil); err != nil {
				t.Fatal(err)
			}
			if routed.Routes[0].Route.ID != test.wantFirst || len(routed.Routes) != 3 {
				t.Fatalf("unexpected route order: %+v", routed.Routes)
			}
			wantCalls := 1
			if test.mode == "off" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d", calls)
			}
			audits := server.store.ListAuditEvents()
			if test.wantReason == "" {
				if len(audits) != 0 {
					t.Fatal("disabled routing generated audit")
				}
				return
			}
			if len(audits) != 1 || audits[0].Status != test.wantReason {
				t.Fatalf("unexpected audits: %+v", audits)
			}
			encoded, _ := json.Marshal(audits)
			for _, secret := range []string{"synthetic coding question", "private-system-text", "provider-secret", "test-evaluator-secret", "secret upstream"} {
				if strings.Contains(string(encoded), secret) {
					t.Errorf("audit contains private value")
				}
			}
		})
	}
}

func TestSemanticRoutingSkipsIneligibleRequests(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Server, *RoutedCall, *ChatCompletionRequest)
	}{
		{"global disabled", func(s *Server, _ *RoutedCall, _ *ChatCompletionRequest) { s.config.SemanticRoutingEnabled = false }},
		{"project denied", func(s *Server, _ *RoutedCall, _ *ChatCompletionRequest) { s.config.SemanticRoutingProjects = nil }},
		{"no key", func(s *Server, _ *RoutedCall, _ *ChatCompletionRequest) { s.config.TypeSafeAPIKey = "" }},
		{"single candidate", func(_ *Server, r *RoutedCall, _ *ChatCompletionRequest) { r.Routes = r.Routes[:1] }},
		{"priority only", func(_ *Server, r *RoutedCall, _ *ChatCompletionRequest) {
			r.Routes[1].Route.Priority = 2
			r.Routes[2].Route.Priority = 3
		}},
		{"affinity", func(_ *Server, r *RoutedCall, _ *ChatCompletionRequest) { r.Affinity = &RequestAffinity{} }},
		{"sticky", func(_ *Server, r *RoutedCall, _ *ChatCompletionRequest) { r.Routes[0].Route.StickySession = true }},
		{"tools", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) {
			r.Tools = []any{map[string]any{"type": "function"}}
		}},
		{"structured output", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) {
			r.ResponseFormat = map[string]any{"type": "json_object"}
		}},
		{"multimodal", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) {
			r.Messages[1].Content = []any{map[string]any{"type": "image_url"}}
		}},
		{"reasoning continuation", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) { r.Messages[1].ReasoningSignature = "opaque" }},
		{"unknown field", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) {
			r.raw = map[string]json.RawMessage{"previous_response_id": json.RawMessage(`"session"`)}
		}},
		{"oversized input", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) {
			r.Messages[1].Content = strings.Repeat("x", 8193)
		}},
		{"empty input", func(_ *Server, _ *RoutedCall, r *ChatCompletionRequest) { r.Messages[1].Content = " " }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, routed, req := semanticFixture(t)
			test.mutate(server, &routed, &req)
			before := append([]RouteSelection(nil), routed.Routes...)
			server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
				t.Fatal("unexpected evaluation")
				return semanticDecision{}, nil
			})
			if err := server.applySemanticRouting(context.Background(), &routed, req, nil); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, routed.Routes) {
				t.Fatal("fallback changed route order")
			}
		})
	}
}

func TestSemanticRoutingPreservesResourcePoolAndPriority(t *testing.T) {
	server, routed, req := semanticFixture(t)
	duplicate := routed.Routes[1]
	duplicate.Resource = &ProviderResource{ID: "resource_b", Priority: 0, Weight: 10}
	routed.Routes[1].Resource = &ProviderResource{ID: "resource_a", Priority: 0, Weight: 90}
	backup := routed.Routes[2]
	backup.Route.Priority = 2
	routed.Routes = []RouteSelection{routed.Routes[0], routed.Routes[1], duplicate, backup}
	before := append([]RouteSelection(nil), routed.Routes...)
	server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, c []semanticCandidate, _ string) (semanticDecision, error) {
		if len(c) != 2 {
			t.Fatalf("resources not grouped or backup included: %+v", c)
		}
		return semanticDecision{Choice: "candidate_2", Confidence: 0.9}, nil
	})
	if err := server.applySemanticRouting(context.Background(), &routed, req, nil); err != nil {
		t.Fatal(err)
	}
	want := []RouteSelection{before[1], before[2], before[0], before[3]}
	if !reflect.DeepEqual(want, routed.Routes) {
		t.Fatalf("pool ordering or backup changed")
	}
}

func TestSemanticRoutingHTTPChatAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%v", stream), func(t *testing.T) {
			server, _, req := semanticFixture(t)
			req.Stream = stream
			calls := 0
			server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, c []semanticCandidate, _ string) (semanticDecision, error) {
				calls++
				for _, candidate := range c {
					if candidate.Model == "model_1" {
						return semanticDecision{Choice: candidate.ID, Confidence: 0.9}, nil
					}
				}
				t.Error("selected model missing")
				return semanticDecision{}, errors.New("missing")
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", req, "thk_semantic_test")
			if response.Code != 200 || calls != 1 {
				t.Fatalf("code=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			logs := server.store.ListRequestLogs()
			if len(logs) != 1 || logs[0].ProviderID != "provider_1" {
				t.Fatalf("wrong executed route: %+v", logs)
			}
		})
	}
}

func TestSemanticRoutingFailoverDoesNotRepeatEvaluation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%v", stream), func(t *testing.T) {
			server, _, req := semanticFixture(t)
			req.Stream = stream
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(429) }))
			defer upstream.Close()
			store := server.store.(*GormStore)
			if err := store.db.Model(&Provider{}).Where("id = ?", "provider_1").Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, c []semanticCandidate, _ string) (semanticDecision, error) {
				calls++
				for _, candidate := range c {
					if candidate.Model == "model_1" {
						return semanticDecision{Choice: candidate.ID, Confidence: 0.9}, nil
					}
				}
				return semanticDecision{}, errors.New("missing")
			})
			response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", req, "thk_semantic_test")
			if response.Code != 200 || calls != 1 || hits.Load() != 1 {
				t.Fatalf("code=%d evaluations=%d selected attempts=%d body=%s", response.Code, calls, hits.Load(), response.Body)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].ProviderID != "provider_0" {
				t.Fatalf("base fallback was not used: %+v", logs)
			}
		})
	}
}

func TestSemanticRoutingCannotRecoverFilteredCandidates(t *testing.T) {
	server, _, req := semanticFixture(t)
	store := server.store.(*GormStore)
	if err := store.db.Model(&ModelRoute{}).Where("id = ?", "route_2").Updates(map[string]any{"project_scope": RouteProjectScopeInclude, "project_ids": `["another_project"]`}).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, c []semanticCandidate, _ string) (semanticDecision, error) {
		calls++
		if len(c) != 2 {
			t.Fatalf("unexpected admitted candidates: %+v", c)
		}
		for _, candidate := range c {
			if candidate.Model == "model_2" {
				t.Error("unauthorized route sent to evaluator")
			}
		}
		return semanticDecision{Choice: "candidate_3", Confidence: 1}, nil
	})
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", req, "thk_semantic_test")
	if response.Code != 200 || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].ProviderID != "provider_0" {
		t.Fatalf("invalid choice changed base route: %+v", logs)
	}
	// Authentication rejection must happen before any external evaluation.
	response = doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", req, "invalid-key")
	if response.Code != 401 || calls != 1 {
		t.Fatal("unauthenticated request reached evaluator")
	}
}

func TestSemanticRoutingCatalogBoundsAndCapabilities(t *testing.T) {
	server, routed, req := semanticFixture(t)
	models, err := server.store.(*GormStore).SemanticProviderModels(context.Background(), routed.Routes)
	if err != nil {
		t.Fatal(err)
	}
	models[0].ContextWindow = 4
	models[1].InputModalities = []string{"image"}
	candidates, _ := buildSemanticCandidates(routed.Routes, models, req)
	if len(candidates) != 1 || candidates[0].Model != "model_2" {
		t.Fatalf("incompatible models selected: %+v", candidates)
	}
	models[2].Metadata["routing_description"] = strings.Repeat("x", 2049)
	candidates, _ = buildSemanticCandidates(routed.Routes, models, req)
	if len(candidates) != 0 {
		t.Fatal("unbounded metadata included")
	}
	models[2].Metadata = nil
	models[2].SupportedParameters = []string{"max_tokens"}
	temperature := 0.5
	req.Temperature = &temperature
	if semanticModelFitsRequest(models[2], req, 100) {
		t.Fatal("unsupported sampling parameter accepted")
	}
	req.Temperature = nil
	req.MaxTokens = 100
	if !semanticModelFitsRequest(models[2], req, 100) {
		t.Fatal("supported output budget rejected")
	}
}

func TestSemanticRoutingRejectsOverflowingOutputBudget(t *testing.T) {
	req := ChatCompletionRequest{MaxTokens: math.MaxInt}
	if semanticModelFitsRequest(ProviderModel{ContextWindow: 4096}, req, 100) {
		t.Fatal("overflowing output budget was accepted")
	}
}
