package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestChatGPTSubscriptionCatalogPreservesReasoningEligibility(t *testing.T) {
	for _, test := range []struct {
		name, declaration, options string
	}{
		{"single effort", `,"supported_reasoning_levels":[{"effort":"high"}]`, "high"},
		{"multiple efforts", `,"supported_reasoning_levels":[{"effort":"high"},{"effort":"xhigh"}]`, "high,xhigh"},
		{"missing declaration", "", ""},
		{"empty declaration", `,"supported_reasoning_levels":[]`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models" {
					t.Errorf("Unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = fmt.Fprintf(w, `{"models":[{"slug":"reasoning-model"%s}]}`, test.declaration)
			})
			catalog, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
			if err != nil || len(catalog.Models) != 1 {
				t.Fatalf("Model discovery failed: models=%d error=%v", len(catalog.Models), err)
			}
			model := providerModelFromCatalog("provider", catalog.Models[0])
			if model.Metadata["supported_reasoning_levels"] != test.options || model.Metadata["reasoning_effort_options"] != test.options {
				t.Fatalf("Imported reasoning declaration was lost: %v", model.Metadata)
			}
			for _, effort := range []string{"high", "low"} {
				request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "Hello"}}, ReasoningEffort: &effort}
				responses := ResponsesRequest{Model: "public", Input: "Hello", Reasoning: &ResponsesReasoning{Effort: &effort}}
				want := ""
				if test.options != "" && effort == "low" {
					want = "reasoning_effort_unsupported"
				}
				for _, compatibility := range []jevCompatibility{jevChatCompatibility(model, request), jevResponsesCompatibility(model, responses)} {
					if compatibility.Reason != want {
						t.Fatalf("Incorrect reasoning eligibility for %s: %+v", effort, compatibility)
					}
				}
			}
			if !catalogReasoningEffortFits(model, nil) {
				t.Fatal("Omitted reasoning effort must retain provider defaults")
			}
		})
	}
}
