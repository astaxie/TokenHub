package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

func TestJevImportedCatalogBudgetRequests(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var astra ProviderCatalogModel
	for _, entry := range entries {
		if entry.ID == "openai" {
			for _, model := range entry.Models {
				if model.ID == "gpt-6-astra" {
					astra = model
				}
			}
		}
	}
	if astra.ID == "" {
		t.Fatal("bundled Astra catalog model missing")
	}
	for _, enabled := range []bool{false, true} {
		for _, budget := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			t.Run(fmt.Sprintf("enabled_%v_%s", enabled, budget), func(t *testing.T) {
				server, _, _, policy := jevFixture(t)
				server.config.SemanticRoutingEnabled = enabled
				var hits, evaluations atomic.Int32
				responses := budget == "max_output_tokens"
				path := "/v1/chat/completions"
				body := map[string]any{"model": "auto-chat", "messages": []any{map[string]any{"role": "user", "content": "task"}}}
				if responses {
					path = "/v1/responses"
					body = map[string]any{"model": "auto-chat", "input": "task"}
				}
				body[budget] = 128
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
						return
					}
					if got[budget] != float64(128) || got["model"] != astra.ID {
						t.Errorf("wire request changed: %+v", got)
					}
					for _, other := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
						if other != budget && got[other] != nil {
							t.Errorf("unexpected budget translation to %s: %+v", other, got)
						}
					}
					hits.Add(1)
					if responses {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "resp_catalog", "object": "response", "status": "completed", "model": astra.ID, "output": []any{}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_catalog", "model": astra.ID, "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
					}
				}))
				defer upstream.Close()
				store := server.store.(*GormStore)
				for i, candidate := range policy.SemanticRouting.Candidates {
					imported := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-models/import", ProviderModelImportRequest{ProviderID: candidate.ProviderID, Models: []ProviderCatalogModel{astra}}, "")
					if imported.Code != 200 && imported.Code != 201 {
						t.Fatalf("catalog import: %d %s", imported.Code, imported.Body)
					}
					if err := store.db.Model(&Provider{}).Where("id = ?", candidate.ProviderID).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
						t.Fatal(err)
					}
					if err := store.db.Model(&ModelRoute{}).Where("id = ?", policy.Routes[i].RouteID).Update("provider_model", astra.ID).Error; err != nil {
						t.Fatal(err)
					}
					policy.SemanticRouting.Candidates[i].ProviderModel = astra.ID
				}
				if _, err := store.UpdateModelRoutePolicy("auto-chat", policy); err != nil {
					t.Fatal(err)
				}
				server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
					evaluations.Add(1)
					return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
				})
				result := doJSON(t, server.Handler(), http.MethodPost, path, body, "thk_semantic_test")
				wantEvaluations := int32(0)
				if enabled {
					wantEvaluations = 1
				}
				if result.Code != 200 || hits.Load() != 1 || evaluations.Load() != wantEvaluations {
					t.Fatalf("budgeted import: %d %s hits=%d evaluations=%d", result.Code, result.Body, hits.Load(), evaluations.Load())
				}
				logs := store.ListRequestLogs()
				wantProvider := "provider_0"
				if enabled {
					wantProvider = "provider_1"
				}
				if len(logs) != 1 || logs[0].ProviderID != wantProvider {
					t.Fatalf("wrong selected/default provider: %+v", logs)
				}
			})
		}
	}
}

func TestCatalogBudgetDeclarationsKeepWireNames(t *testing.T) {
	for _, tc := range []struct {
		name, endpoints string
		declared, want  []string
	}{
		{"chat", "chat/completions", nil, []string{"max_tokens"}},
		{"responses", "responses", nil, []string{"max_output_tokens"}},
		{"both", "responses,chat/completions", nil, []string{"max_tokens", "max_output_tokens"}},
		{"unknown", "", nil, nil},
		{"other protocol", "anthropic", nil, nil},
		{"explicit completion budget", "chat/completions", []string{"max_completion_tokens"}, []string{"max_tokens", "max_completion_tokens"}},
		{"both with declared output budget", "chat/completions,responses", []string{"max_output_tokens"}, []string{"max_tokens", "max_output_tokens"}},
		{"responses with declared completion budget", "responses", []string{"max_completion_tokens"}, []string{"max_completion_tokens", "max_output_tokens"}},
		{"chat with declared output budget", "chat/completions", []string{"max_output_tokens"}, []string{"max_tokens", "max_output_tokens"}},
		{"unknown with declared completion budget", "", []string{"max_completion_tokens"}, []string{"max_completion_tokens"}},
		{"prefixed endpoints", " /v1/chat/completions, /v1/responses ", nil, []string{"max_tokens", "max_output_tokens"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"id": "synthetic", "endpoints": tc.endpoints, "tool_call": true}
			parameters := []any{}
			for _, name := range tc.declared {
				parameters = append(parameters, name)
			}
			raw["supported_parameters"] = parameters
			model := normalizeProviderCatalogModel(raw)
			for _, parameter := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
				if slices.Contains(model.SupportedParameters, parameter) != slices.Contains(tc.want, parameter) {
					t.Fatalf("incorrect budget declaration: %+v", model.SupportedParameters)
				}
			}
		})
	}
}
