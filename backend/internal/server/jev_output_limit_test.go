package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestJevOutputLimitExcludesIncompatibleDefault(t *testing.T) {
	for _, budgetField := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		t.Run(budgetField, func(t *testing.T) {
			server, routed, _, _ := jevFixture(t)
			store := server.store.(*GormStore)
			for _, model := range store.ListProviderModels() {
				limit := int64(131072)
				if model.ProviderID == "provider_0" {
					limit = 65536
				}
				if _, err := server.importProviderModels(ProviderModelImportRequest{ProviderID: model.ProviderID, Models: []ProviderCatalogModel{{
					ID: model.UpstreamModel, Type: "chat", ContextWindow: 1048576, MaxOutputTokens: limit, SupportedParameters: []string{budgetField},
				}}}); err != nil {
					t.Fatal(err)
				}
			}
			server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, candidates []semanticCandidate, _ string) (semanticDecision, error) {
				for _, candidate := range candidates {
					if candidate.Model == "model_0" {
						t.Error("output-incompatible default reached evaluator")
					}
				}
				return semanticDecision{Choice: "no_preference", Confidence: 1}, nil
			})
			var err error
			if budgetField == "max_output_tokens" {
				var req ResponsesRequest
				if err = json.Unmarshal([]byte(`{"model":"auto-chat","input":"task","max_output_tokens":65537}`), &req); err != nil {
					t.Fatal(err)
				}
				err = server.applyJevResponsesRouting(context.Background(), &routed, &req, nil)
			} else {
				var req ChatCompletionRequest
				if err = json.Unmarshal([]byte(fmt.Sprintf(`{"model":"auto-chat","messages":[{"role":"user","content":"task"}],"%s":65537}`, budgetField)), &req); err != nil {
					t.Fatal(err)
				}
				err = server.applySemanticRouting(context.Background(), &routed, req, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(routed.Routes) == 0 {
				t.Fatal("compatible candidates were lost")
			}
			for _, route := range routed.Routes {
				if route.ProviderModel == "model_0" {
					t.Fatal("oversized request retained incompatible default route")
				}
			}
		})
	}
}

func TestCatalogOutputLimitBoundaryAndUnknownMetadata(t *testing.T) {
	for _, tc := range []struct {
		limit  string
		budget int
		want   bool
	}{
		{"65536", 65536, true}, {"65536", 65537, false}, {"65536", 0, true}, {"", 65537, true}, {"invalid", 65537, true}, {"0", 65537, true}, {"-1", 65537, true},
	} {
		if got := catalogOutputBudgetFits(ProviderModel{Metadata: map[string]string{"max_output_tokens": tc.limit}}, tc.budget); got != tc.want {
			t.Errorf("limit=%q budget=%d got=%t want=%t", tc.limit, tc.budget, got, tc.want)
		}
	}
}
