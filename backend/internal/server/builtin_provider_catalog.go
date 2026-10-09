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
		[]string{"deepseek-flash", "deepseek-v4-flash", "deepseek-v4-pro"},
	)
	for index := range entry.Models {
		model := &entry.Models[index]
		model.ContextWindow = 1000000
		model.MaxOutputTokens = 393216
		model.InputModalities = []string{"text"}
		model.OutputModalities = []string{"text"}
		model.Capabilities = []string{"chat", "reasoning", "tools", "structured_outputs"}
		model.SupportedParameters = []string{"tools", "tool_choice", "response_format", "reasoning", "max_tokens", "max_completion_tokens", "max_output_tokens", "top_logprobs"}
		model.Metadata = map[string]string{
			"source": "builtin", "upstream_source": "https://api-docs.deepseek.com/quick_start/pricing/",
			"catalog_reviewed_at": "2026-10-10", "verified_at": "2026-10-10",
			"endpoints":                "responses,chat/completions,anthropic",
			"reasoning_effort_options": "none,low,high,max", "reasoning_default": "true",
			"pricing_status": "unverified", "pricing_note": "Configure current peak/off-peak prices and the holiday calendar before publishing.",
			"features":           "function-calling,structured-outputs,reasoning,apply-patch,web-search",
			"top_logprobs_range": "0,20", "responses_stateful": "false",
			"prompt_cache_mode": "automatic", "custom_tool_names": "apply_patch",
		}
		if model.ID == "deepseek-v4-pro" {
			model.DisplayName = "DeepSeek V4 Pro 0813"
		} else {
			model.DisplayName = "DeepSeek V4.1 Flash"
			model.InputModalities = append(model.InputModalities, "image")
			model.Capabilities = append(model.Capabilities, "vision")
			model.SupportedParameters = append(model.SupportedParameters, "image_input")
			if model.ID == "deepseek-v4-flash" {
				model.Metadata["lifecycle_status"] = "redirected"
				model.Metadata["replacement_model"] = "deepseek-flash"
			}
		}
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
