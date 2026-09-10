package server

import "strings"

func builtinProviderCatalog(includeModels bool) []ProviderCatalogEntry {
	entries := builtinProviderPluginCatalogSeedEntries()
	if !providerCatalogHasEntry(entries, "custom") {
		entries = append(entries, customProviderCatalogEntry())
	}
	sortCatalogEntries(entries)
	if includeModels {
		return entries
	}
	return cloneCatalogEntries(entries, false)
}

func builtinProviderCatalogRequiredProviderIDs() []string {
	return []string{"openai", "anthropic", "google"}
}

func builtinProviderCatalogNormalizeBaseURL(id string, raw string) string {
	if raw == "" {
		return raw
	}
	normalizedID := strings.ToLower(strings.TrimSpace(id))
	normalizedRaw := strings.ToLower(raw)
	if normalizedID == "dmxapi" || normalizedRaw == "https://www.dmxapi.cn" || normalizedRaw == "https://api.dmxapi.cn" {
		return raw + "/v1"
	}
	if normalizedID == "302ai" || strings.Contains(normalizedRaw, "api.highwayapi.ai/openai") {
		if strings.HasSuffix(normalizedRaw, "/openai") {
			return raw + "/v1"
		}
	}
	return raw
}

func deepSeekBuiltinCatalogEntry() ProviderCatalogEntry {
	entry := builtinCatalogEntry(
		"deepseek",
		"DeepSeek",
		"deepseek",
		"https://api.deepseek.com",
		"https://api-docs.deepseek.com",
		[]string{"deepseek-v4-flash", "deepseek-v4-pro"},
	)
	for index := range entry.Models {
		model := &entry.Models[index]
		switch model.ID {
		case "deepseek-v4-flash":
			model.DisplayName = "DeepSeek V4 Flash"
			model.ContextWindow = 1048576
			model.MaxOutputTokens = 393216
			model.InputPriceUSDPer1M = 0.14
			model.CacheReadPriceUSDPer1M = 0.0028
			model.OutputPriceUSDPer1M = 0.28
			model.Metadata = map[string]string{
				"source":                   "builtin",
				"upstream_source":          "deepseek-api",
				"endpoints":                "responses,chat/completions,anthropic",
				"features":                 "function-calling,structured-outputs,reasoning,apply-patch,web-search",
				"top_logprobs_range":       "0,20",
				"responses_stateful":       "false",
				"prompt_cache_mode":        "automatic",
				"custom_tool_names":        "apply_patch",
				"reasoning_effort_options": "low,high,max",
				"reasoning_default":        "true",
				"tool_call":                "true",
				"vision":                   "false",
			}
		case "deepseek-v4-pro":
			model.DisplayName = "DeepSeek V4 Pro"
			model.ContextWindow = 1048576
			model.MaxOutputTokens = 393216
			model.InputPriceUSDPer1M = 0.435
			model.CacheReadPriceUSDPer1M = 0.003625
			model.OutputPriceUSDPer1M = 0.87
			model.Metadata = map[string]string{
				"source":                   "builtin",
				"upstream_source":          "deepseek-api",
				"endpoints":                "responses,chat/completions,anthropic",
				"features":                 "function-calling,structured-outputs,reasoning,apply-patch,web-search",
				"top_logprobs_range":       "0,20",
				"responses_stateful":       "false",
				"prompt_cache_mode":        "automatic",
				"custom_tool_names":        "apply_patch",
				"reasoning_effort_options": "low,high,max",
				"reasoning_default":        "true",
				"tool_call":                "true",
				"vision":                   "false",
			}
		default:
			continue
		}
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"text"}
		model.Capabilities = []string{"chat", "reasoning", "tools", "structured_outputs"}
		model.SupportedParameters = []string{"temperature", "top_p", "top_logprobs", "tools", "tool_choice", "response_format", "reasoning"}
	}
	return entry
}

func builtinCatalogEntry(id string, name string, providerType string, baseURL string, docURL string, modelIDs []string) ProviderCatalogEntry {
	models := make([]ProviderCatalogModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		models = append(models, ProviderCatalogModel{
			ID:            modelID,
			Name:          modelID,
			DisplayName:   modelID,
			CanonicalName: modelID,
			Category:      inferModelCategory(modelID, modelID),
			Family:        inferModelFamily(modelID),
			Type:          "chat",
			Capabilities:  []string{"chat"},
			Metadata:      map[string]string{"source": "builtin"},
		})
	}
	categories, categoryCounts := catalogCategorySummary(models)
	return ProviderCatalogEntry{
		ID:             id,
		Name:           name,
		DisplayName:    name,
		Type:           providerType,
		BaseURL:        baseURL,
		DocURL:         docURL,
		Categories:     categories,
		CategoryCounts: categoryCounts,
		ModelsCount:    len(models),
		Source:         "builtin",
		Models:         models,
	}
}
