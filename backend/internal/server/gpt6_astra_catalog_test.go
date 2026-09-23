package server

import (
	"slices"
	"testing"
)

func TestStandardCatalogIncludesGPT6Astra(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.Name != "gpt-6-astra" {
			continue
		}
		if model.ContextWindow != 1050000 || model.InputPriceUSDPer1M != 10 || model.OutputPriceUSDPer1M != 50 || model.CacheReadPriceUSDPer1M != 1 || model.Metadata["max_output_tokens"] != "128000" || model.Metadata["reasoning_effort_options"] != "low,medium,high,xhigh,max" {
			t.Fatalf("unexpected Astra metadata: %+v", model)
		}
		if !slices.Contains(model.Capabilities, "vision") || slices.Contains(model.SupportedParameters, "temperature") {
			t.Fatalf("unexpected Astra capabilities: %+v", model)
		}
		return
	}
	t.Fatal("gpt-6-astra missing from standard catalog")
}

func TestProviderCatalogIncludesGPT6Astra(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.ID != "openai" {
			continue
		}
		for _, model := range entry.Models {
			if model.ID != "gpt-6-astra" {
				continue
			}
			if model.ContextWindow != 1050000 || model.MaxOutputTokens != 128000 || model.Metadata["reasoning_effort_options"] != "low,medium,high,xhigh,max" {
				t.Fatalf("unexpected Astra Provider metadata: %+v", model)
			}
			return
		}
	}
	t.Fatal("gpt-6-astra missing from OpenAI Provider catalog")
}

func TestAnthropicCodexReasoningPreservesMaxEffort(t *testing.T) {
	for _, raw := range []map[string]any{
		{"effort": "max"},
		{"output_config": map[string]any{"effort": "max"}},
		{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "max"}},
	} {
		if got := anthropicCodexReasoning(raw)["effort"]; got != "max" {
			t.Fatalf("max effort became %v for %v", got, raw)
		}
	}
	if got := codexReasoningEffort("invalid"); got != "" {
		t.Fatalf("invalid effort accepted: %q", got)
	}
}
