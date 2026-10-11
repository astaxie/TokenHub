package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJevSkipsModelsWithIncompatibleDeclaredEffort(t *testing.T) {
	server, routed, _, policy := jevFixture(t)
	const claude = "claude-sonnet-5-5"
	policy.SemanticRouting.Candidates[0].ProviderModel = claude
	data, err := json.Marshal(policy.SemanticRouting)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]string{semanticRoutingMetadataKey: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	store := server.store.(*GormStore)
	if err := store.db.Model(&Model{}).Where("name = ?", "auto-chat").Update("metadata", string(metadata)).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&ModelRoute{}).Where("id = ?", "route_0").Update("provider_model", claude).Error; err != nil {
		t.Fatal(err)
	}
	for index, route := range routed.Routes {
		upstream, efforts := route.ProviderModel, "none,high,max"
		if index == 0 {
			upstream, efforts = claude, "low,medium,high,xhigh,max"
		}
		store.AddProviderModel(ProviderModel{ProviderID: route.Provider.ID, UpstreamModel: upstream, Modality: "chat", Status: StatusActive,
			SupportedParameters: []string{"max_tokens", "tools", "reasoning", "reasoning_effort"}, Metadata: map[string]string{"reasoning_effort_options": efforts}})
	}
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "model_1" || body["reasoning_effort"] != "none" {
			t.Errorf("wrong candidate or changed effort reached upstream: %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chat_effort", "model": "model_1", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}})
	}))
	t.Cleanup(upstream.Close)
	if err := store.db.Model(&Provider{}).Where("id IN ?", []string{"provider_0", "provider_1", "provider_2"}).Updates(map[string]any{"type": ProviderOpenAICompatible, "base_url": upstream.URL}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&Provider{}).Where("id = ?", "provider_0").Update("type", ProviderAnthropic).Error; err != nil {
		t.Fatal(err)
	}
	server.semanticRouter = semanticTestEvaluator(func(_ context.Context, _ string, candidates []semanticCandidate, _ string) (semanticDecision, error) {
		for _, candidate := range candidates {
			if candidate.ID == "choice_0" {
				return semanticDecision{Choice: "choice_0", Confidence: 0.9}, nil
			}
		}
		return semanticDecision{Choice: "choice_1", Confidence: 0.9}, nil
	})
	body := jevHTTPBody(false, false)
	body["reasoning_effort"] = "none"
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", body, "thk_semantic_test")
	if response.Code != http.StatusOK || hits != 1 {
		t.Fatalf("incompatible effort stopped a request with a compatible candidate: %d hits=%d %s", response.Code, hits, response.Body)
	}
}

func TestDeclaredEffortsAndAliasesKeepChatAndResponsesCompatible(t *testing.T) {
	model := ProviderModel{Modality: "chat", Status: StatusActive, SupportedParameters: []string{"reasoning", "max_output_tokens"}, Metadata: map[string]string{
		"reasoning_effort_options": "none,low,high,max", "reasoning_effort_aliases": `{"minimal":"low","medium":"high","xhigh":"high","ultra":"max"}`,
	}}
	for _, test := range []struct {
		effort string
		want   bool
	}{{"none", true}, {"high", true}, {"medium", true}, {"minimal", true}, {"xhigh", true}, {"ultra", true}, {"unsupported", false}} {
		if catalogReasoningEffortFits(model, &test.effort) != test.want {
			t.Fatalf("incorrect declared Chat effort compatibility: %s", test.effort)
		}
		req := ResponsesRequest{Input: "task", Reasoning: &ResponsesReasoning{Effort: &test.effort}}
		if jevResponsesModelFits(model, req) != test.want {
			t.Fatalf("incorrect declared Responses effort compatibility: %s", test.effort)
		}
	}
	unknown := "legacy-unknown"
	if !catalogReasoningEffortFits(model, nil) || !catalogReasoningEffortFits(ProviderModel{}, &unknown) {
		t.Fatal("effort filtering narrowed requests without an explicit declaration")
	}
}
