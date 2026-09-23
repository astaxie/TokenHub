package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestSemanticRoutingOutputBudgetParameterSupport(t *testing.T) {
	for _, test := range []struct {
		name       string
		parameters []string
		want       bool
	}{
		{"undeclared", nil, true},
		{"empty declaration", []string{}, true},
		{"chat budget", []string{"max_tokens"}, true},
		{"chat and completion budgets", []string{"max_tokens", "max_completion_tokens"}, true},
		{"chat and responses budgets", []string{"max_tokens", "max_output_tokens"}, true},
		{"completion budget only", []string{"max_completion_tokens"}, false},
		{"responses budget only", []string{"max_output_tokens"}, false},
		{"both alternative budgets", []string{"max_completion_tokens", "max_output_tokens"}, false},
		{"unrelated parameter", []string{"temperature"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := ProviderModel{SupportedParameters: test.parameters}
			if got := semanticModelFitsRequest(model, ChatCompletionRequest{MaxTokens: 128}, 100); got != test.want {
				t.Errorf("max_tokens compatibility = %v, want %v", got, test.want)
			}
			if !semanticModelFitsRequest(model, ChatCompletionRequest{}, 100) {
				t.Error("omitted output budget must not require max_tokens support")
			}
		})
	}
}

func TestSemanticRoutingHTTPOutputBudgetCompatibility(t *testing.T) {
	for _, alternative := range []string{"max_completion_tokens", "max_output_tokens"} {
		for _, stream := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream_%v/enabled_%v", alternative, stream, enabled), func(t *testing.T) {
					server, routed, req := semanticFixture(t)
					server.config.SemanticRoutingEnabled = enabled
					req.MaxTokens, req.Stream = 128, stream
					var hits [3]atomic.Int32
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var payload map[string]any
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Errorf("decode upstream request: %v", err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						model, _ := payload["model"].(string)
						switch model {
						case "model_0":
							hits[0].Add(1)
						case "model_1":
							hits[1].Add(1)
						case "model_2":
							hits[2].Add(1)
						default:
							t.Errorf("unexpected upstream model: %q", model)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						if payload["max_tokens"] != float64(128) || payload["max_completion_tokens"] != nil || payload["max_output_tokens"] != nil {
							t.Errorf("unexpected forwarded output budget: %#v", payload)
						}
						if model == "model_1" {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusBadRequest)
							_, _ = fmt.Fprint(w, `{"error":{"message":"Unsupported parameter: max_tokens","type":"invalid_request_error","code":"unsupported_parameter"}}`)
							return
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprintf(w, "data: {\"id\":\"synthetic\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n", model)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"id":"synthetic","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, model)
					}))
					defer upstream.Close()
					store := server.store.(*GormStore)
					for _, route := range routed.Routes {
						if err := store.db.Model(&Provider{}).Where("id = ?", route.Provider.ID).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
							t.Fatal(err)
						}
					}
					models, err := store.SemanticProviderModels(context.Background(), routed.Routes)
					if err != nil {
						t.Fatal(err)
					}
					for _, model := range models {
						parameters := []string{"max_tokens"}
						if model.UpstreamModel == "model_1" {
							parameters = []string{alternative}
						}
						if _, err := store.UpdateProviderModel(model.ID, ProviderModel{SupportedParameters: parameters}); err != nil {
							t.Fatal(err)
						}
					}
					calls := 0
					var offered []string
					server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, candidates []semanticCandidate, instructions string) (semanticDecision, error) {
						calls++
						choice := "no_preference"
						for _, candidate := range candidates {
							offered = append(offered, candidate.Model)
							// Prefer the incompatible model if offered, reproducing the
							// regression; otherwise promote the compatible second choice.
							if candidate.Model == "model_1" {
								return semanticDecision{Choice: candidate.ID, Confidence: 1}, nil
							}
							if candidate.Model == "model_2" {
								choice = candidate.ID
							}
						}
						return semanticDecision{Choice: choice, Confidence: 1}, nil
					})
					response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", req, "thk_semantic_test")
					if response.Code != http.StatusOK {
						t.Fatalf("code=%d body=%s", response.Code, response.Body)
					}
					wantCalls, wantModel := 0, 0
					if enabled {
						wantCalls, wantModel = 1, 2
						if !reflect.DeepEqual(offered, []string{"model_0", "model_2"}) {
							t.Errorf("unexpected semantic candidates: %v", offered)
						}
					}
					if calls != wantCalls {
						t.Errorf("evaluations=%d, want %d", calls, wantCalls)
					}
					for index := range hits {
						var want int32
						if index == wantModel {
							want = 1
						}
						if got := hits[index].Load(); got != want {
							t.Errorf("model_%d attempts=%d, want %d", index, got, want)
						}
					}
				})
			}
		}
	}
}
