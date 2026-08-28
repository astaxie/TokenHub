package server

import (
	"encoding/json"
	"sort"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type providerModelCategoryDefinition struct {
	Key               string   `json:"key"`
	Label             string   `json:"label,omitempty"`
	Order             int      `json:"order,omitempty"`
	Aliases           []string `json:"aliases,omitempty"`
	FamilyPrefixes    []string `json:"family_prefixes,omitempty"`
	CanonicalPrefixes []string `json:"canonical_prefixes,omitempty"`
}

func fallbackProviderModelCategoryDefinitions() []providerModelCategoryDefinition {
	return []providerModelCategoryDefinition{
		{Key: "codex", Label: "OpenAI Codex", Order: 10, Aliases: []string{"codex"}, FamilyPrefixes: []string{"codex"}},
		{Key: "openai", Label: "OpenAI", Order: 20, Aliases: []string{"gpt", "openai", "o1", "o3", "o4"}, FamilyPrefixes: []string{"gpt"}, CanonicalPrefixes: []string{"gpt"}},
		{Key: "claude", Label: "Claude", Order: 30, Aliases: []string{"anthropic", "claude"}, FamilyPrefixes: []string{"claude"}, CanonicalPrefixes: []string{"claude"}},
		{Key: "deepseek", Label: "DeepSeek", Order: 40, Aliases: []string{"deepseek"}, FamilyPrefixes: []string{"deepseek"}, CanonicalPrefixes: []string{"deepseek"}},
		{Key: "gemini", Label: "Gemini", Order: 50, Aliases: []string{"gemini", "google", "google/"}, FamilyPrefixes: []string{"gemini"}, CanonicalPrefixes: []string{"gemini"}},
		{Key: "qwen", Label: "Qwen", Order: 60, Aliases: []string{"alibaba", "dashscope", "qwen"}, FamilyPrefixes: []string{"qwen"}, CanonicalPrefixes: []string{"qwen"}},
		{Key: "glm", Label: "GLM", Order: 70, Aliases: []string{"glm", "zhipu"}, FamilyPrefixes: []string{"glm"}, CanonicalPrefixes: []string{"glm"}},
		{Key: "kimi", Label: "Kimi", Order: 80, Aliases: []string{"kimi", "moonshot"}, FamilyPrefixes: []string{"kimi"}},
		{Key: "doubao", Label: "Doubao", Order: 90, Aliases: []string{"doubao", "volcengine"}, FamilyPrefixes: []string{"doubao"}},
		{Key: "ernie", Label: "ERNIE", Order: 100, Aliases: []string{"ernie"}},
		{Key: "baichuan", Label: "Baichuan", Order: 110, Aliases: []string{"baichuan"}},
		{Key: "minimax", Label: "MiniMax", Order: 120, Aliases: []string{"hailuo", "minimax"}},
		{Key: "stepfun", Label: "StepFun", Order: 130, Aliases: []string{"step-", "stepaudio"}},
		{Key: "wanx", Label: "WanX", Order: 140, Aliases: []string{"wanx"}},
		{Key: "grok", Label: "Grok", Order: 150, Aliases: []string{"grok", "xai", "xai/"}},
		{Key: "paddlepaddle", Label: "PaddlePaddle", Order: 160, Aliases: []string{"paddleocr"}},
		{Key: "microsoft", Label: "Microsoft", Order: 170, Aliases: []string{"phi-"}},
		{Key: "llama", Label: "Llama", Order: 180, Aliases: []string{"llama", "meta", "meta/"}, FamilyPrefixes: []string{"llama"}},
		{Key: "mistral", Label: "Mistral", Order: 190, Aliases: []string{"mistral"}, FamilyPrefixes: []string{"mistral"}},
		{Key: "custom", Label: "自定义", Order: 1000, Aliases: []string{"custom"}, FamilyPrefixes: []string{"custom"}, CanonicalPrefixes: []string{"custom"}},
	}
}

func providerModelCategoryDefinitionsFromRegistry(registry *AdapterRegistry) []providerModelCategoryDefinition {
	definitions := fallbackProviderModelCategoryDefinitions()
	if registry == nil {
		return normalizeProviderModelCategoryDefinitions(definitions)
	}
	for _, plugin := range registry.ListPlugins() {
		definitions = append(definitions, providerModelCategoryDefinitionsFromPlugin(plugin)...)
	}
	return normalizeProviderModelCategoryDefinitions(definitions)
}

func providerModelCategoryDefinitionsFromPlugin(plugin pluginmeta.Descriptor) []providerModelCategoryDefinition {
	definitions := []providerModelCategoryDefinition{}
	for _, capability := range plugin.Capabilities {
		if capability.Kind != "provider_catalog" || capability.Name != "model_category" || strings.TrimSpace(capability.Value) == "" {
			continue
		}
		var definition providerModelCategoryDefinition
		if err := json.Unmarshal([]byte(capability.Value), &definition); err != nil {
			continue
		}
		definitions = append(definitions, definition)
	}
	return definitions
}

func providerModelCategoryDefinitionsForKeys(keys []string) []providerModelCategoryDefinition {
	definitionsByKey := map[string]providerModelCategoryDefinition{}
	for _, definition := range fallbackProviderModelCategoryDefinitions() {
		definition = normalizeProviderModelCategoryDefinition(definition)
		definitionsByKey[definition.Key] = definition
	}
	result := make([]providerModelCategoryDefinition, 0, len(keys))
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if definition, ok := definitionsByKey[key]; ok {
			result = append(result, definition)
			continue
		}
		result = append(result, providerModelCategoryDefinition{Key: key, Label: key, Aliases: []string{key}})
	}
	return normalizeProviderModelCategoryDefinitions(result)
}

func standardModelCategorySet() map[string]bool {
	categories := map[string]bool{}
	for _, definition := range fallbackProviderModelCategoryDefinitions() {
		if key := strings.ToLower(strings.TrimSpace(definition.Key)); key != "" {
			categories[key] = true
		}
	}
	return categories
}

func normalizeProviderModelCategoryDefinitions(definitions []providerModelCategoryDefinition) []providerModelCategoryDefinition {
	byKey := map[string]providerModelCategoryDefinition{}
	for _, definition := range definitions {
		definition = normalizeProviderModelCategoryDefinition(definition)
		if definition.Key == "" {
			continue
		}
		if existing, ok := byKey[definition.Key]; ok {
			definition.Aliases = catalogUniqueStrings(append(existing.Aliases, definition.Aliases...))
			definition.FamilyPrefixes = catalogUniqueStrings(append(existing.FamilyPrefixes, definition.FamilyPrefixes...))
			definition.CanonicalPrefixes = catalogUniqueStrings(append(existing.CanonicalPrefixes, definition.CanonicalPrefixes...))
			if definition.Label == "" {
				definition.Label = existing.Label
			}
			if definition.Order == 0 {
				definition.Order = existing.Order
			}
		}
		byKey[definition.Key] = definition
	}
	result := make([]providerModelCategoryDefinition, 0, len(byKey))
	for _, definition := range byKey {
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].Key < result[j].Key
	})
	return result
}

func normalizeProviderModelCategoryDefinition(definition providerModelCategoryDefinition) providerModelCategoryDefinition {
	definition.Key = strings.ToLower(strings.TrimSpace(definition.Key))
	definition.Label = strings.TrimSpace(definition.Label)
	definition.Aliases = lowerUniqueStrings(definition.Aliases)
	definition.FamilyPrefixes = lowerUniqueStrings(definition.FamilyPrefixes)
	definition.CanonicalPrefixes = lowerUniqueStrings(definition.CanonicalPrefixes)
	if definition.Key != "" && len(definition.Aliases) == 0 {
		definition.Aliases = []string{definition.Key}
	}
	return definition
}

func lowerUniqueStrings(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			normalized = append(normalized, value)
		}
	}
	return catalogUniqueStrings(normalized)
}

func adapterModelCategoriesFromDefinitions(definitions []providerModelCategoryDefinition) []AdapterModelCategory {
	definitions = normalizeProviderModelCategoryDefinitions(definitions)
	categories := make([]AdapterModelCategory, 0, len(definitions))
	for _, definition := range definitions {
		categories = append(categories, AdapterModelCategory{
			Key:               definition.Key,
			Label:             definition.Label,
			Order:             definition.Order,
			Aliases:           append([]string(nil), definition.Aliases...),
			FamilyPrefixes:    append([]string(nil), definition.FamilyPrefixes...),
			CanonicalPrefixes: append([]string(nil), definition.CanonicalPrefixes...),
		})
	}
	return categories
}

func providerModelCategoryDefinitionsFromAdapter(categories []AdapterModelCategory) []providerModelCategoryDefinition {
	definitions := make([]providerModelCategoryDefinition, 0, len(categories))
	for _, category := range categories {
		definitions = append(definitions, providerModelCategoryDefinition{
			Key:               category.Key,
			Label:             category.Label,
			Order:             category.Order,
			Aliases:           append([]string(nil), category.Aliases...),
			FamilyPrefixes:    append([]string(nil), category.FamilyPrefixes...),
			CanonicalPrefixes: append([]string(nil), category.CanonicalPrefixes...),
		})
	}
	return normalizeProviderModelCategoryDefinitions(definitions)
}

func standardModelCategory(category string) string {
	return standardModelCategoryWithDefinitions(category, nil)
}

func standardModelCategoryWithDefinitions(category string, definitions []providerModelCategoryDefinition) string {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		return "custom"
	}
	for _, definition := range providerCategoryDefinitionsOrFallback(definitions) {
		if category == definition.Key {
			return definition.Key
		}
	}
	return inferModelCategoryWithDefinitions(category, "", definitions)
}

func inferModelFamily(id string) string {
	return inferModelFamilyWithDefinitions(id, nil)
}

func inferModelFamilyWithDefinitions(id string, definitions []providerModelCategoryDefinition) string {
	normalized := strings.ToLower(id)
	for _, definition := range providerCategoryDefinitionsOrFallback(definitions) {
		for _, family := range definition.FamilyPrefixes {
			if strings.Contains(normalized, family) {
				return family
			}
		}
	}
	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '-' || r == '/' || r == '_' || r == '.'
	})
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "custom"
}

func inferModelCategory(id string, displayName string) string {
	return inferModelCategoryWithDefinitions(id, displayName, nil)
}

func inferModelCategoryWithDefinitions(id string, displayName string, definitions []providerModelCategoryDefinition) string {
	normalized := strings.ToLower(strings.Join([]string{id, displayName}, " "))
	for _, definition := range providerCategoryDefinitionsOrFallback(definitions) {
		if categoryAliasesMatch(normalized, definition.Aliases) {
			return definition.Key
		}
	}
	return "custom"
}

func categoryAliasesMatch(value string, aliases []string) bool {
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		if len(alias) <= 2 && isAlphaNumeric(alias) {
			if categoryAliasTokenMatches(value, alias) {
				return true
			}
			continue
		}
		if strings.Contains(value, alias) {
			return true
		}
	}
	return false
}

func categoryAliasTokenMatches(value string, alias string) bool {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for _, part := range parts {
		if part == alias {
			return true
		}
	}
	return false
}

func isAlphaNumeric(value string) bool {
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return value != ""
}

func canonicalModelName(id string, displayName string) string {
	return canonicalModelNameWithDefinitions(id, displayName, nil)
}

func canonicalModelNameWithDefinitions(id string, displayName string, definitions []providerModelCategoryDefinition) string {
	value := strings.TrimSpace(id)
	if idx := strings.LastIndex(value, "/"); idx >= 0 && idx < len(value)-1 {
		value = value[idx+1:]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(displayName)
	}
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	value = strings.Trim(value, "-")
	for _, definition := range providerCategoryDefinitionsOrFallback(definitions) {
		for _, prefix := range definition.CanonicalPrefixes {
			value = normalizeCompactModelVersion(value, prefix)
		}
	}
	if value == "" {
		return "custom-model"
	}
	return value
}

func normalizeCompactModelVersion(value string, prefix string) string {
	compact := prefix + "v"
	if strings.HasPrefix(value, compact) && len(value) > len(compact) {
		next := value[len(compact)]
		if next >= '0' && next <= '9' {
			return prefix + "-v" + value[len(compact):]
		}
	}
	if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
		next := value[len(prefix)]
		if next >= '0' && next <= '9' {
			return prefix + "-" + value[len(prefix):]
		}
	}
	return value
}

func providerCategoryDefinitionsOrFallback(definitions []providerModelCategoryDefinition) []providerModelCategoryDefinition {
	if len(definitions) > 0 {
		return normalizeProviderModelCategoryDefinitions(definitions)
	}
	return normalizeProviderModelCategoryDefinitions(fallbackProviderModelCategoryDefinitions())
}
