package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJevRequestBoundaryFallback(t *testing.T) {
	for _, kind := range []string{"single", "long text", "multimodal", "session", "sticky", "deadline", "unlisted routes", "no eligible models"} {
		t.Run(kind, func(t *testing.T) {
			server, routed, req, policy := jevFixture(t)
			headers := map[string][]string{}
			switch kind {
			case "single":
				routed.Routes = routed.Routes[:1]
			case "long text":
				req.Messages[1].Content = strings.Repeat("x", 8193)
			case "multimodal":
				req.Messages[1].Content = []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image"}}}
			case "session":
				headers["X-Tokenhub-Session-Id"] = []string{"session"}
			case "sticky":
				routed.Routes[0].Route.StickySession = true
			case "deadline":
				server.config.SemanticRoutingTimeoutMS = 5
			case "unlisted routes":
				policy.SemanticRouting.Candidates = policy.SemanticRouting.Candidates[:1]
				data, _ := json.Marshal(policy.SemanticRouting)
				routed.Call.Model.Metadata[semanticRoutingMetadataKey] = string(data)
			case "no eligible models":
				if err := server.store.(*GormStore).db.Model(&ProviderModel{}).Where("1 = 1").Update("status", StatusDisabled).Error; err != nil {
					t.Fatal(err)
				}
			}
			server.semanticRouter = semanticTestEvaluator(func(ctx context.Context, _ string, _ []semanticCandidate, _ string) (semanticDecision, error) {
				if kind != "deadline" {
					t.Fatal("request boundary unexpectedly sent text to Jev")
				}
				<-ctx.Done()
				return semanticDecision{}, ctx.Err()
			})
			start := time.Now()
			err := server.applySemanticRouting(context.Background(), &routed, req, headers)
			if kind == "no eligible models" {
				if err == nil || AsHTTPError(err).Code != "jev_no_eligible_candidates" {
					t.Fatalf("missing model eligibility failure: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if routed.Routes[0].ProviderModel != "model_0" || (kind == "unlisted routes" && len(routed.Routes) != 1) {
				t.Fatalf("incorrect fallback: %+v", routed.Routes)
			}
			if kind == "deadline" && time.Since(start) > time.Second {
				t.Fatal("routing deadline was not bounded")
			}
		})
	}
}

func TestJevWireParameterAndModalityCompatibility(t *testing.T) {
	for _, responses := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat", true: "responses"}[responses], func(t *testing.T) {
			server, routed, _, _ := jevFixture(t)
			wrongParameter := "max_output_tokens"
			payload := `{"model":"auto-chat","messages":[{"role":"user","content":"task"}],"max_completion_tokens":128}`
			if responses {
				wrongParameter = "max_tokens"
				payload = `{"model":"auto-chat","input":"task","max_output_tokens":128}`
			}
			if err := server.store.(*GormStore).db.Model(&ProviderModel{}).Where("provider_id = ?", "provider_0").Update("supported_parameters", `["`+wrongParameter+`"]`).Error; err != nil {
				t.Fatal(err)
			}
			server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, candidates []semanticCandidate, _ string) (semanticDecision, error) {
				for _, c := range candidates {
					if c.Model == "model_0" {
						t.Fatal("incompatible token parameter was admitted")
					}
				}
				return semanticDecision{Choice: "no_preference", Confidence: 1}, nil
			})
			if responses {
				var request ResponsesRequest
				if err := json.Unmarshal([]byte(payload), &request); err != nil {
					t.Fatal(err)
				}
				if err := server.applyJevResponsesRouting(context.Background(), &routed, &request, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				var request ChatCompletionRequest
				if err := json.Unmarshal([]byte(payload), &request); err != nil {
					t.Fatal(err)
				}
				if err := server.applySemanticRouting(context.Background(), &routed, request, nil); err != nil {
					t.Fatal(err)
				}
			}
			if routed.Routes[0].ProviderModel != "model_1" {
				t.Fatal("incompatible default was used")
			}
		})
	}
	model := ProviderModel{InputModalities: []string{"text"}}
	if jevInputModalitiesFit(model, []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "https://example.test/image"}}}}) {
		t.Fatal("image admitted to text-only model")
	}
}
