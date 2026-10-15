package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestReviewedCatalogParametersReachJevUpstream(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, deepSeekBuiltinCatalogEntry())
	for _, entry := range entries {
		for _, model := range entry.Models {
			if !(entry.ID == "deepseek" && (model.ID == "deepseek-flash" || model.ID == "deepseek-v4-pro")) && !(entry.ID == "anthropic" && model.ID == "claude-sonnet-5-5") && !(entry.ID == "openai" && model.ID == "gpt-6-sol") {
				continue
			}
			parameters := []string{"reasoning_effort"}
			if entry.ID == "deepseek" {
				parameters = append(parameters, "top_p")
			}
			for _, parameter := range parameters {
				t.Run(entry.Source+"/"+entry.ID+"/"+model.ID+"/"+parameter, func(t *testing.T) {
					server, routed, _, _ := jevFixture(t)
					for _, route := range routed.Routes {
						inventory := providerModelFromCatalog(route.Provider.ID, model)
						// Old imported inventories predate the inferred wire parameter.
						// Their existing effort declaration must work without reimport.
						if parameter == "reasoning_effort" {
							inventory.SupportedParameters = slices.DeleteFunc(inventory.SupportedParameters, func(value string) bool { return value == "reasoning_effort" })
						}
						inventory.UpstreamModel = route.ProviderModel
						server.store.AddProviderModel(inventory)
					}
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						want := any("high")
						if parameter == "top_p" {
							want = 0.99
						}
						if body[parameter] != want {
							t.Errorf("wire parameter changed: %s=%v, want %v", parameter, body[parameter], want)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_parameter", "model": "model_0", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}})
					}))
					t.Cleanup(upstream.Close)
					if err := server.store.(*GormStore).db.Model(&Provider{}).Where("id IN ?", []string{"provider_0", "provider_1", "provider_2"}).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
						t.Fatal(err)
					}
					server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
						return semanticDecision{Choice: "choice_0", Confidence: 0.9}, nil
					})
					body := jevHTTPBody(false, false)
					// This test isolates effort/sampling declarations. Tool
					// compatibility is covered by the protocol contract matrix.
					delete(body, "tools")
					if parameter == "top_p" {
						body[parameter] = 0.99
					} else {
						body[parameter] = "high"
					}
					response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", body, "thk_semantic_test")
					if response.Code != http.StatusOK || calls != 1 {
						t.Fatalf("accepted parameter failed admission: %d calls=%d %s", response.Code, calls, response.Body)
					}
				})
			}
		}
	}
}

func TestCatalogEffortDeclarationPreservesOtherEligibilityChecks(t *testing.T) {
	parameters := []string{"max_tokens"}
	model := ProviderModel{Modality: "chat", Status: StatusActive, SupportedParameters: parameters, Metadata: map[string]string{"reasoning_effort_options": "high,max"}}
	effort := "high"
	request := ChatCompletionRequest{MaxTokens: 10, ReasoningEffort: &effort}
	if result := jevChatCompatibility(model, request); result.Reason != "" {
		t.Fatalf("declared legacy effort control rejected: %+v", result)
	}
	penalty := 0.5
	request.FrequencyPenalty = &penalty
	if result := jevChatCompatibility(model, request); result.Reason == "" || len(parameters) != 1 {
		t.Fatal("effort declaration admitted an unrelated parameter or mutated inventory")
	}
	request.FrequencyPenalty = nil
	model.Metadata = nil
	if jevChatCompatibility(model, request).Reason == "" {
		t.Fatal("undeclared effort bypassed the parameter allowlist")
	}
	model.SupportedParameters = nil
	model.Metadata = map[string]string{"reasoning_effort_options": "high,max"}
	request = ChatCompletionRequest{MaxTokens: 10, Temperature: &penalty}
	if jevChatCompatibility(model, request).Reason == "" {
		t.Fatal("empty parameter metadata became an unrestricted wildcard")
	}
}
