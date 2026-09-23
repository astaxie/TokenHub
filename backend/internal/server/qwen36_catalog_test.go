package server

import (
	"slices"
	"strings"
	"testing"
)

func TestStandardCatalogIncludesQwen36Family(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Model{}
	for _, model := range models {
		byName[strings.ToLower(model.Name)] = model
	}

	tests := []struct {
		name            string
		contextWindow   int64
		maxOutputTokens string
		inputPrice      float64
		cacheReadPrice  float64
		outputPrice     float64
		inputModalities []string
	}{
		{name: "qwen/qwen3.6-flash", contextWindow: 1000000, maxOutputTokens: "65536", inputPrice: 0.1875, outputPrice: 1.125, inputModalities: []string{"text", "image", "video"}},
		{name: "qwen/qwen3.6-35b-a3b", contextWindow: 262144, maxOutputTokens: "262144", inputPrice: 0.15, cacheReadPrice: 0.05, outputPrice: 1, inputModalities: []string{"text", "image", "video"}},
		{name: "qwen/qwen3.6-max-preview", contextWindow: 262144, maxOutputTokens: "65536", inputPrice: 1.027, outputPrice: 6.162, inputModalities: []string{"text"}},
		{name: "qwen/qwen3.6-27b", contextWindow: 262144, maxOutputTokens: "262144", inputPrice: 0.6, cacheReadPrice: 0.12, outputPrice: 3.6, inputModalities: []string{"text", "image", "video"}},
		{name: "qwen/qwen3.6-plus", contextWindow: 1000000, maxOutputTokens: "65536", inputPrice: 0.325, outputPrice: 1.95, inputModalities: []string{"text", "image", "video"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, ok := byName[test.name]
			if !ok {
				t.Fatalf("expected %s in standard catalog", test.name)
			}
			if model.Family != "qwen3.6" || model.ContextWindow != test.contextWindow ||
				model.InputPriceUSDPer1M != test.inputPrice || model.CacheReadPriceUSDPer1M != test.cacheReadPrice ||
				model.OutputPriceUSDPer1M != test.outputPrice || model.Metadata["max_output_tokens"] != test.maxOutputTokens ||
				model.Metadata["upstream_source"] != "openrouter" || !slices.Equal(model.InputModalities, test.inputModalities) {
				t.Fatalf("unexpected Qwen3.6 catalog metadata: %+v", model)
			}
			for _, capability := range []string{"chat", "reasoning", "tools", "structured_outputs"} {
				if !slices.Contains(model.Capabilities, capability) {
					t.Fatalf("%s missing capability %q: %v", test.name, capability, model.Capabilities)
				}
			}
		})
	}
}
