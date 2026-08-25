package server

import (
	"strconv"
	"strings"
	"time"
)

const xaiGrokProviderCatalogID = "xai-grok"

type xaiGrokCatalogModel struct {
	ID               string
	DisplayName      string
	Description      string
	ContextWindow    int64
	MaxOutputTokens  int64
	ReasoningLevels  []string
	ReasoningDefault string
	InputModalities  []string
}

func xaiGrokStaticCatalogModels() []xaiGrokCatalogModel {
	return []xaiGrokCatalogModel{
		{
			ID: "grok-4.6", DisplayName: "Grok 4.6",
			Description:   "Super Grok subscription model for long-running agents and interactive work.",
			ContextWindow: 500000, MaxOutputTokens: 65536,
			ReasoningLevels: []string{"low", "medium", "high", "xhigh"}, ReasoningDefault: "high",
			InputModalities: []string{"text", "image"},
		},
		{
			ID: "grok-4.5", DisplayName: "Grok 4.5",
			Description:   "Super Grok subscription model for coding, agents, and knowledge work.",
			ContextWindow: 500000, MaxOutputTokens: 65536,
			ReasoningLevels: []string{"low", "medium", "high"}, ReasoningDefault: "high",
			InputModalities: []string{"text", "image"},
		},
		{
			ID: "grok-4.3", DisplayName: "Grok 4.3",
			Description:   "Super Grok subscription model with optional reasoning.",
			ContextWindow: 1000000, MaxOutputTokens: 65536,
			ReasoningLevels: []string{"none", "low", "medium", "high"}, ReasoningDefault: "low",
			InputModalities: []string{"text", "image"},
		},
		{
			ID: "grok-composer-2.5-fast", DisplayName: "Composer 2.5 Fast",
			Description:   "Super Grok Composer model for fast coding turns.",
			ContextWindow: 200000, MaxOutputTokens: 32768,
			InputModalities: []string{"text"},
		},
		{
			ID: "grok-build-0.1", DisplayName: "Grok Build 0.1",
			Description:   "Grok Build coding model available through a Super Grok CLI login.",
			ContextWindow: 256000, MaxOutputTokens: 256000,
			InputModalities: []string{"text", "image"},
		},
	}
}

func xaiGrokProviderCatalog() ProviderCatalogEntry {
	models := make([]ProviderCatalogModel, 0, 8)
	for _, remote := range xaiGrokStaticCatalogModels() {
		supportedParameters := []string{"tools", "reasoning"}
		capabilities := []string{"chat", "reasoning", "streaming", "tools"}
		if len(remote.InputModalities) > 1 {
			capabilities = append(capabilities, "vision")
			supportedParameters = append(supportedParameters, "image_input")
		}
		models = append(models, ProviderCatalogModel{
			ID:                  remote.ID,
			Name:                remote.ID,
			DisplayName:         remote.DisplayName,
			CanonicalName:       remote.ID,
			Category:            "grok",
			Family:              "grok",
			Type:                "chat",
			ContextWindow:       remote.ContextWindow,
			InputModalities:     append([]string(nil), remote.InputModalities...),
			OutputModalities:    []string{"text"},
			Capabilities:        capabilities,
			SupportedParameters: supportedParameters,
			LastUpdated:         time.Now().UTC().Format(time.RFC3339),
			Metadata: map[string]string{
				"source":                     "xai-grok-subscription",
				"description":                remote.Description,
				"supported_in_api":           "true",
				"display_name":               remote.DisplayName,
				"default_reasoning_level":    remote.ReasoningDefault,
				"supported_reasoning_levels": strings.Join(remote.ReasoningLevels, ","),
				"max_output_tokens":          strconv.FormatInt(remote.MaxOutputTokens, 10),
				"billing_mode":               "subscription",
				"provider_type":              ProviderXAIGrok,
			},
		})
	}
	categories, counts := catalogCategorySummary(models)
	return ProviderCatalogEntry{
		ID:             xaiGrokProviderCatalogID,
		Name:           "Super Grok",
		DisplayName:    "Super Grok",
		Type:           ProviderXAIGrok,
		BaseURL:        xaiCLIChatProxyBaseURL,
		DocURL:         "https://x.ai/grok",
		Categories:     categories,
		CategoryCounts: counts,
		ModelsCount:    len(models),
		Source:         "xai-grok-subscription",
		Models:         models,
	}
}

func (s *Server) xaiGrokProviderCatalogFromStandardModels(selected []string) ProviderCatalogEntry {
	catalog := xaiGrokProviderCatalog()
	if len(selected) == 0 {
		return catalog
	}
	wanted := map[string]bool{}
	for _, name := range selected {
		if name = strings.TrimSpace(name); name != "" {
			wanted[name] = true
		}
	}
	filtered := catalog.Models[:0]
	for _, model := range catalog.Models {
		if wanted[model.ID] || wanted[model.Name] {
			filtered = append(filtered, model)
		}
	}
	catalog.Models = filtered
	catalog.ModelsCount = len(filtered)
	catalog.Categories, catalog.CategoryCounts = catalogCategorySummary(filtered)
	return catalog
}

func xaiGrokProviderCatalogFromModels(models []ProviderCatalogModel) ProviderCatalogEntry {
	catalog := xaiGrokProviderCatalog()
	if len(models) > 0 {
		catalog.Models = models
		catalog.ModelsCount = len(models)
		catalog.Categories, catalog.CategoryCounts = catalogCategorySummary(models)
	}
	return catalog
}
