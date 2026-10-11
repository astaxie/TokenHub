package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJevContractHTTPAdmissionAndDiagnostics(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]ProviderCatalogModel{}
	for _, entry := range entries {
		if entry.ID == "openai" {
			for _, m := range entry.Models {
				models[m.ID] = m
			}
		}
	}
	for _, id := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		for _, tc := range []struct{ name, endpoint, extra, reason string }{
			{"tools disabled", "chat/completions", `"tool_choice":"none"`, ""},
			{"protocol controls", "responses", `"include":["reasoning.encrypted_content"],"truncation":"disabled","service_tier":"auto"`, ""},
			{"comma separated deny", "responses", `"service_tier":"priority"`, "parameter_explicitly_denied"},
			{"default sampling", "responses", `"temperature":0.5`, "sampling_reasoning_conflict"},
			{"invalid effort", "responses", `"reasoning":{"effort":"minimal"}`, "reasoning_effort_unsupported"},
			{"wrong endpoint field", "responses", `"response_format":{"type":"json_object"}`, "parameter_endpoint_mismatch"},
			{"unknown secret field", "responses", `"private-field-name":"private-field-value"`, "parameter_not_declared"},
			{"invalid include", "responses", `"include":["private-field-value"]`, "include_value_unsupported"},
			{"reasoning tools", "chat/completions", `"reasoning_effort":"low","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`, "tools_reasoning_conflict"},
		} {
			t.Run(id+"/"+tc.name, func(t *testing.T) {
				model := models[id]
				model.Metadata = cloneStringMap(model.Metadata)
				if tc.name == "comma separated deny" {
					model.Metadata["request_parameter_denied"] = "temperature, service_tier"
				}
				if model.ID == "" {
					t.Fatal("model missing")
				}
				// Protocol controls are intentionally absent from the legacy flat list.
				model.SupportedParameters = []string{"temperature", "tools", "reasoning", "reasoning_effort", "response_format"}
				server, _, _, policy := jevFixture(t)
				var hits, evaluations atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
						return
					}
					if tc.name == "protocol controls" && (got["service_tier"] != "auto" || got["truncation"] != "disabled") {
						t.Errorf("wire controls changed: %+v", got)
					}
					if tc.endpoint == "chat/completions" {
						if tc.reason == "" && got["tool_choice"] != "none" {
							t.Errorf("tool disabling sentinel changed: %+v", got)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_contract", "model": id, "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "resp_contract", "object": "response", "status": "completed", "model": id, "output": []any{}})
					}
				}))
				defer upstream.Close()
				store := server.store.(*GormStore)
				for i, candidate := range policy.SemanticRouting.Candidates {
					imported := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-models/import", ProviderModelImportRequest{ProviderID: candidate.ProviderID, Models: []ProviderCatalogModel{model}}, "")
					if imported.Code != 200 && imported.Code != 201 {
						t.Fatalf("import: %d %s", imported.Code, imported.Body)
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
					evaluations.Add(1)
					return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
				})
				prefix := `{"model":"auto-chat","input":"private-prompt",`
				if tc.endpoint == "chat/completions" {
					prefix = `{"model":"auto-chat","messages":[{"role":"user","content":"private-prompt"}],`
				}
				var body map[string]any
				if err := json.Unmarshal([]byte(prefix+tc.extra+`}`), &body); err != nil {
					t.Fatal(err)
				}
				result := doJSON(t, server.Handler(), http.MethodPost, "/v1/"+tc.endpoint, body, "thk_semantic_test")
				if tc.reason == "" {
					if result.Code != 200 || hits.Load() != 1 {
						t.Fatalf("admission: %d %s hits=%d", result.Code, result.Body, hits.Load())
					}
					return
				}
				if result.Code != 503 || hits.Load() != 0 || evaluations.Load() != 0 {
					t.Fatalf("rejection: %d %s hits=%d evaluations=%d", result.Code, result.Body, hits.Load(), evaluations.Load())
				}
				if strings.Contains(result.Body, "choice_") || strings.Contains(result.Body, "provider_") {
					t.Fatal("public error exposes candidate inventory")
				}
				reason := tc.reason
				if id == "gpt-6.1-sol" && tc.name == "reasoning tools" {
					reason = "tools_endpoint_unsupported"
				}
				found := false
				for _, event := range store.ListAuditEvents() {
					if event.Action != "routing.jev_admission" {
						continue
					}
					found = true
					var snapshot struct {
						Rejections []jevCandidateRejection `json:"rejections"`
					}
					if err := json.Unmarshal([]byte(event.AfterSnapshot), &snapshot); err != nil {
						t.Fatal(err)
					}
					if len(snapshot.Rejections) != len(policy.SemanticRouting.Candidates) {
						t.Fatalf("missing candidate diagnostics: %s", event.AfterSnapshot)
					}
					for _, rejection := range snapshot.Rejections {
						if rejection.Reason != reason {
							t.Fatalf("reason=%s want=%s", rejection.Reason, reason)
						}
					}
					for _, secret := range []string{"private-prompt", "private-field-name", "private-field-value", "provider-secret", "thk_semantic_test"} {
						if strings.Contains(event.AfterSnapshot, secret) {
							t.Fatalf("audit exposed %s", secret)
						}
					}
				}
				if !found {
					t.Fatal("no admission diagnostic recorded")
				}
			})
		}
	}
}

func TestJevProfileStreamingContinuation(t *testing.T) {
	server, _, _, _ := jevFixture(t)
	hits := jevHTTPUpstreams(t, server, false, func(body map[string]any) {
		options, ok := body["stream_options"].(map[string]any)
		if !ok || options["include_obfuscation"] != false || body["stream"] != true {
			t.Errorf("required stream controls missing or changed: %+v", body)
		}
	})
	store := server.store.(*GormStore)
	metadata := `{"request_parameter_profile":"openai-chat-responses-v1","endpoints":"responses"}`
	if err := store.db.Model(&ProviderModel{}).Where("1 = 1").Update("metadata", metadata).Error; err != nil {
		t.Fatal(err)
	}
	var evaluations atomic.Int32
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		evaluations.Add(1)
		return semanticDecision{Choice: "choice_1", Confidence: .9}, nil
	})
	body := jevHTTPBody(true, true)
	body["stream_options"] = map[string]any{"include_obfuscation": false}
	first := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if first.Code != 200 {
		t.Fatalf("initial stream: %d %s", first.Code, first.Body)
	}
	body["previous_response_id"] = "resp_model_1"
	second := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if second.Code != 200 || hits.Load() != 2 || evaluations.Load() != 1 {
		t.Fatalf("continuation: %d %s hits=%d evaluations=%d", second.Code, second.Body, hits.Load(), evaluations.Load())
	}
	metadata = `{"request_parameter_profile":"openai-chat-responses-v1","endpoints":"responses","request_parameter_denied":"temperature, service_tier"}`
	if err := store.db.Model(&ProviderModel{}).Where("provider_id = ?", "provider_1").Update("metadata", metadata).Error; err != nil {
		t.Fatal(err)
	}
	body["service_tier"] = "priority"
	denied := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", body, "thk_semantic_test")
	if denied.Code != 409 || hits.Load() != 2 || evaluations.Load() != 1 {
		t.Fatalf("denied bound request reached upstream: %d hits=%d evaluations=%d", denied.Code, hits.Load(), evaluations.Load())
	}
	found := false
	for _, event := range store.ListAuditEvents() {
		if event.Action == "routing.jev_admission" && strings.Contains(event.AfterSnapshot, `"parameter":"service_tier"`) && strings.Contains(event.AfterSnapshot, `"reason":"parameter_explicitly_denied"`) {
			found = true
		}
	}
	if !found {
		t.Fatal("bound denial missing safe diagnostic")
	}

}
