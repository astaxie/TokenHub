package server

import (
	"slices"
	"strings"
	"testing"
)

func TestStandardCatalogIncludesGLM53(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var glm53 Model
	for _, model := range models {
		if strings.EqualFold(model.Name, "glm-5.3") {
			glm53 = model
			break
		}
	}
	if glm53.Name == "" {
		t.Fatal("expected glm-5.3 in standard catalog")
	}
	if glm53.Family != "glm" || glm53.ContextWindow != 1000000 ||
		glm53.InputPriceUSDPer1M != 0 || glm53.OutputPriceUSDPer1M != 0 ||
		glm53.Metadata["max_output_tokens"] != "131072" ||
		glm53.Metadata["billing_mode"] != "subscription" ||
		glm53.Metadata["reasoning_mode"] != "always_on" ||
		glm53.Metadata["reasoning_effort_options"] != "low,high,max" {
		t.Fatalf("unexpected GLM-5.3 catalog metadata: %+v", glm53)
	}
	if !slices.Equal(glm53.InputModalities, []string{"text"}) || !slices.Equal(glm53.OutputModalities, []string{"text"}) {
		t.Fatalf("unexpected GLM-5.3 modalities: input=%v output=%v", glm53.InputModalities, glm53.OutputModalities)
	}
	for _, capability := range []string{"chat", "reasoning", "tools", "structured_outputs"} {
		if !slices.Contains(glm53.Capabilities, capability) {
			t.Fatalf("GLM-5.3 missing capability %q: %v", capability, glm53.Capabilities)
		}
	}
}

func TestTrackedProviderCatalogOffersGLM53OnlyForCodingPlans(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]ProviderCatalogEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}

	for _, providerID := range []string{"zhipuai-coding-plan", "zai-coding-plan"} {
		entry, ok := byID[providerID]
		if !ok {
			t.Fatalf("expected provider catalog entry %s", providerID)
		}
		var glm53 ProviderCatalogModel
		for _, model := range entry.Models {
			if model.ID == "glm-5.3" {
				glm53 = model
				break
			}
		}
		if glm53.ID == "" {
			t.Fatalf("expected %s to offer glm-5.3", providerID)
		}
		if glm53.ContextWindow != 1000000 || glm53.MaxOutputTokens != 131072 ||
			glm53.Family != "glm" || glm53.Metadata["release_date"] != "2026-08-14" ||
			glm53.Metadata["reasoning_effort_options"] != "low,high,max" {
			t.Fatalf("unexpected %s GLM-5.3 metadata: %+v", providerID, glm53)
		}
		for _, capability := range []string{"reasoning", "tool_call", "structured_output"} {
			if !slices.Contains(glm53.Capabilities, capability) {
				t.Fatalf("%s GLM-5.3 missing capability %q: %v", providerID, capability, glm53.Capabilities)
			}
		}
	}

	for _, providerID := range []string{"zhipuai", "zai"} {
		for _, model := range byID[providerID].Models {
			if model.ID == "glm-5.3" {
				t.Fatalf("%s must not advertise glm-5.3 before the model API is available", providerID)
			}
		}
	}
}
