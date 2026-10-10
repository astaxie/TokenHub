package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestJevGPT6CatalogCapabilityAdmission(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		for _, tc := range gpt6CapabilityRequests() {
			if id == "gpt-6.1-sol" && tc.nonReasoning {
				continue
			}
			t.Run(id+"/"+tc.name, func(t *testing.T) {
				var catalogModel ProviderCatalogModel
				for _, entry := range entries {
					if entry.ID != "openai" {
						continue
					}
					for _, model := range entry.Models {
						if model.ID == id {
							catalogModel = model
						}
					}
				}
				if catalogModel.ID == "" {
					t.Fatalf("catalog model %s missing", id)
				}
				server, _, _, policy := jevFixture(t)
				var hits atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
						return
					}
					if got["model"] != id {
						t.Errorf("wrong upstream model: %v", got["model"])
					}
					for key, want := range tc.parameters {
						gotJSON, err := json.Marshal(got[key])
						if err != nil {
							t.Error(err)
							return
						}
						wantJSON, err := json.Marshal(want)
						if err != nil {
							t.Error(err)
							return
						}
						if !bytes.Equal(gotJSON, wantJSON) {
							t.Errorf("wire field %s changed: got %s, want %s", key, gotJSON, wantJSON)
						}
					}
					hits.Add(1)
					if tc.responses {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "resp_capability", "object": "response", "status": "completed", "model": id, "output": []any{}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_sampling", "model": id, "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
					}
				}))
				defer upstream.Close()
				store := server.store.(*GormStore)
				for i, candidate := range policy.SemanticRouting.Candidates {
					imported := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-models/import", ProviderModelImportRequest{ProviderID: candidate.ProviderID, Models: []ProviderCatalogModel{catalogModel}}, "")
					if imported.Code != 200 && imported.Code != 201 {
						t.Fatalf("catalog import: %d %s", imported.Code, imported.Body)
					}
					if err := store.db.Model(&Provider{}).Where("id = ?", candidate.ProviderID).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
						t.Fatal(err)
					}
					if err := store.db.Model(&ModelRoute{}).Where("id = ?", policy.Routes[i].RouteID).Update("provider_model", id).Error; err != nil {
						t.Fatal(err)
					}
					policy.SemanticRouting.Candidates[i].ProviderModel = id
				}
				if _, err := store.UpdateModelRoutePolicy("auto-chat", policy); err != nil {
					t.Fatal(err)
				}
				server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
					return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
				})
				path := "/v1/chat/completions"
				body := map[string]any{"model": "auto-chat", "messages": []any{map[string]any{"role": "user", "content": "task"}}}
				if tc.responses {
					path = "/v1/responses"
					body = map[string]any{"model": "auto-chat", "input": "task"}
				}
				for key, value := range tc.parameters {
					body[key] = value
				}
				result := doJSON(t, server.Handler(), http.MethodPost, path, body, "thk_semantic_test")
				if result.Code != http.StatusOK || hits.Load() != 1 {
					t.Fatalf("catalog capability: %d %s hits=%d", result.Code, result.Body, hits.Load())
				}
			})
		}
	}
}

type gpt6CapabilityRequest struct {
	name                    string
	responses, nonReasoning bool
	parameters              map[string]any
}

func gpt6CapabilityRequests() []gpt6CapabilityRequest {
	schema := map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}, "required": []string{"answer"}, "additionalProperties": false}
	function := map[string]any{"name": "get_answer", "parameters": schema, "strict": true}
	return []gpt6CapabilityRequest{
		{name: "chat_top_p", nonReasoning: true, parameters: map[string]any{"reasoning_effort": "none", "top_p": .9}},
		{name: "chat_probabilities", nonReasoning: true, parameters: map[string]any{"reasoning_effort": "none", "logprobs": true, "top_logprobs": 2}},
		{name: "chat_tool_controls", nonReasoning: true, parameters: map[string]any{"reasoning_effort": "none", "tools": []any{map[string]any{"type": "function", "function": function}}, "tool_choice": "required", "parallel_tool_calls": false}},
		{name: "responses_tool_controls", responses: true, parameters: map[string]any{"reasoning": map[string]any{"effort": "low"}, "tools": []any{map[string]any{"type": "function", "name": "get_answer", "parameters": schema, "strict": true}}, "tool_choice": "required", "parallel_tool_calls": false}},
		{name: "responses_json_schema", responses: true, parameters: map[string]any{"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "answer", "schema": schema, "strict": true}}}},
	}
}
