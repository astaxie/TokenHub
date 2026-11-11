package server

import (
	"slices"
	"testing"
)

func TestCatalogModelParametersDeriveExplicitCapabilityFlags(t *testing.T) {
	raw := map[string]any{
		"endpoints":           "chat/completions",
		"tool_call":           true,
		"tool_choice":         true,
		"parallel_tool_calls": true,
		"structured_output":   true,
		"temperature":         true,
		"top_p":               true,
		"stop":                true,
		"seed":                true,
	}
	model := ProviderCatalogModel{Type: "chat", Metadata: map[string]string{}}
	got := catalogModelParameters(raw, model)
	for _, want := range []string{"max_tokens", "tools", "tool_choice", "parallel_tool_calls", "response_format", "temperature", "top_p", "stop", "seed"} {
		if !slices.Contains(got, want) {
			t.Fatalf("derived parameters %v do not include %q", got, want)
		}
	}
}

func TestCatalogModelParametersDoNotInferOptionalFlags(t *testing.T) {
	raw := map[string]any{"endpoints": "chat/completions", "tool_call": true}
	model := ProviderCatalogModel{Type: "chat", Metadata: map[string]string{}}
	got := catalogModelParameters(raw, model)
	for _, unwanted := range []string{"tool_choice", "parallel_tool_calls", "top_p", "stop", "seed"} {
		if slices.Contains(got, unwanted) {
			t.Fatalf("derived parameters %v unexpectedly include %q", got, unwanted)
		}
	}
}
