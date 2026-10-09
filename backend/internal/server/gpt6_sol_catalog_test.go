package server

import (
	"slices"
	"testing"
)

func TestGPT6SolCatalogConsistency(t *testing.T) {
	standard, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	providers, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna"} {
		t.Run(id, func(t *testing.T) {
			si := slices.IndexFunc(standard, func(m Model) bool { return m.Name == id })
			if si < 0 {
				t.Fatalf("%s missing from standard catalog", id)
			}
			s := standard[si]
			for _, provider := range providers {
				if provider.ID != "openai" {
					continue
				}
				for _, p := range provider.Models {
					if p.ID != id {
						continue
					}
					if s.ContextWindow != p.ContextWindow || s.ContextWindow != 1050000 || p.MaxOutputTokens != 128000 || s.Metadata["max_output_tokens"] != "128000" {
						t.Fatal("context or output limits differ between catalogs")
					}
					if s.InputPriceUSDPer1M != p.InputPriceUSDPer1M || s.OutputPriceUSDPer1M != p.OutputPriceUSDPer1M || s.CacheReadPriceUSDPer1M != p.CacheReadPriceUSDPer1M || s.CacheWritePriceUSDPer1M != p.CacheWritePriceUSDPer1M {
						t.Fatal("prices differ between catalogs")
					}
					if s.Metadata["reasoning_effort_options"] != p.Metadata["reasoning_effort_options"] || s.Metadata["endpoints"] != p.Metadata["endpoints"] {
						t.Fatal("reasoning or endpoint metadata differs between catalogs")
					}
					wantEfforts := "none,low,medium,high,xhigh,max"
					if id == "gpt-6.1-sol" {
						wantEfforts = "low,medium,high,xhigh,max"
						if s.CacheReadPriceUSDPer1M != 0.1 || slices.Contains(s.SupportedParameters, "temperature") {
							t.Fatal("GPT-6.1 Sol must use its own cached input rate and reasoning parameter restrictions")
						}
					}
					if s.Metadata["reasoning_effort_options"] != wantEfforts || s.Metadata["endpoints"] != "responses,chat/completions" {
						t.Fatal("unexpected reasoning or endpoint metadata")
					}
					return
				}
			}
			t.Fatalf("%s missing from OpenAI Provider catalog", id)
		})
	}
}
