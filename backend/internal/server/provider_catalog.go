package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const publicProviderConfAllURL = "https://raw.githubusercontent.com/ThinkInAIXYZ/PublicProviderConf/main/dist/all.json"

const (
	tencentTokenPlanEnterpriseBaseURL = "https://tokenhub.tencentmaas.com/plan/v3"
	tencentTokenPlanPersonalBaseURL   = "https://api.lkeap.cloud.tencent.com/plan/v3"
	tencentTokenPlanTermsVersion      = "2026-07-23"
	tencentTokenPlanEnterpriseDocURL  = "https://cloud.tencent.com/document/product/1823/130660"
	tencentTokenPlanPersonalDocURL    = "https://cloud.tencent.com/document/product/1823/130060"
)

var catalogCache = struct {
	sync.Mutex
	entries   []ProviderCatalogEntry
	fetchedAt time.Time
}{}

var standardModelCategories = map[string]bool{
	"codex":        true,
	"openai":       true,
	"claude":       true,
	"deepseek":     true,
	"gemini":       true,
	"qwen":         true,
	"glm":          true,
	"kimi":         true,
	"doubao":       true,
	"ernie":        true,
	"baichuan":     true,
	"minimax":      true,
	"stepfun":      true,
	"wanx":         true,
	"paddlepaddle": true,
	"microsoft":    true,
	"llama":        true,
	"mistral":      true,
	"grok":         true,
	"custom":       true,
}

func LoadProviderCatalog(ctx context.Context, client *http.Client, refresh bool) ([]ProviderCatalogEntry, string, error) {
	catalogCache.Lock()
	if !refresh && time.Since(catalogCache.fetchedAt) < time.Hour && len(catalogCache.entries) > 0 {
		entries := cloneCatalogEntries(catalogCache.entries, false)
		catalogCache.Unlock()
		return entries, "cache", nil
	}
	catalogCache.Unlock()

	entries, err := fetchPublicProviderCatalog(ctx, client)
	if err != nil {
		catalogCache.Lock()
		if len(catalogCache.entries) > 0 {
			entries := cloneCatalogEntries(catalogCache.entries, false)
			catalogCache.Unlock()
			return entries, "cache", nil
		}
		catalogCache.Unlock()
		entries := builtinProviderCatalog(true)
		sortCatalogEntries(entries)
		catalogCache.Lock()
		catalogCache.entries = cloneCatalogEntries(entries, true)
		catalogCache.fetchedAt = time.Now()
		catalogCache.Unlock()
		return cloneCatalogEntries(entries, false), "builtin", nil
	}

	entries = mergeProviderCatalogEntries(entries, tencentTokenPlanCatalogEntries())
	entries = append(entries, customProviderCatalogEntry())
	sortCatalogEntries(entries)

	catalogCache.Lock()
	catalogCache.entries = cloneCatalogEntries(entries, true)
	catalogCache.fetchedAt = time.Now()
	catalogCache.Unlock()

	return cloneCatalogEntries(entries, false), "public-provider-conf", nil
}

func GetProviderCatalogEntry(ctx context.Context, client *http.Client, id string, refresh bool) (ProviderCatalogEntry, string, bool, error) {
	id = strings.TrimSpace(id)
	if id == "custom" {
		return customProviderCatalogEntry(), "builtin", true, nil
	}
	_, source, err := LoadProviderCatalog(ctx, client, refresh)
	if err != nil {
		return ProviderCatalogEntry{}, source, false, err
	}
	catalogCache.Lock()
	entries := cloneCatalogEntries(catalogCache.entries, true)
	catalogCache.Unlock()
	for _, entry := range entries {
		if entry.ID == id {
			return entry, source, true, nil
		}
	}
	return ProviderCatalogEntry{}, source, false, nil
}

func fetchPublicProviderCatalog(ctx context.Context, client *http.Client) ([]ProviderCatalogEntry, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicProviderConfAllURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, NewHTTPError(statusForProvider(resp.StatusCode), "provider_catalog_fetch_failed", resp.Status)
	}

	var payload struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	entries := make([]ProviderCatalogEntry, 0, len(payload.Providers))
	for id, raw := range payload.Providers {
		entry := normalizeProviderCatalogEntry(id, raw)
		if entry.ID == "" || entry.Name == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func normalizeProviderCatalogEntry(id string, raw map[string]any) ProviderCatalogEntry {
	entry := ProviderCatalogEntry{
		ID:          firstNonEmpty(catalogStringField(raw, "id"), id),
		Name:        firstNonEmpty(catalogStringField(raw, "name"), catalogStringField(raw, "display_name"), id),
		DisplayName: firstNonEmpty(catalogStringField(raw, "display_name"), catalogStringField(raw, "name"), id),
		BaseURL:     normalizeProviderBaseURL(id, catalogStringField(raw, "api")),
		DocURL:      catalogStringField(raw, "doc"),
		Source:      "public-provider-conf",
	}
	entry.Type = inferProviderType(entry.ID, entry.BaseURL)
	if rawModels, ok := raw["models"].([]any); ok {
		entry.Models = make([]ProviderCatalogModel, 0, len(rawModels))
		for _, rawModel := range rawModels {
			modelMap, ok := rawModel.(map[string]any)
			if !ok {
				continue
			}
			model := normalizeProviderCatalogModel(modelMap)
			if model.ID == "" {
				continue
			}
			entry.Models = append(entry.Models, model)
		}
	}
	entry.ModelsCount = len(entry.Models)
	entry.Categories, entry.CategoryCounts = catalogCategorySummary(entry.Models)
	return entry
}

func normalizeProviderCatalogModel(raw map[string]any) ProviderCatalogModel {
	id := firstNonEmpty(catalogStringField(raw, "id"), catalogStringField(raw, "name"))
	name := firstNonEmpty(catalogStringField(raw, "name"), id)
	displayName := firstNonEmpty(catalogStringField(raw, "display_name"), name)
	modelType := firstNonEmpty(catalogStringField(raw, "type"), "chat")
	cost := catalogObjectField(raw, "cost")
	limit := catalogObjectField(raw, "limit")
	modalities := catalogObjectField(raw, "modalities")
	metadata := map[string]string{
		"source": "public-provider-conf",
	}
	for _, key := range []string{"knowledge", "release_date", "last_updated"} {
		if value := catalogStringField(raw, key); value != "" {
			metadata[key] = value
		}
	}
	model := ProviderCatalogModel{
		ID:                     id,
		Name:                   name,
		DisplayName:            displayName,
		CanonicalName:          canonicalModelName(id, displayName),
		Category:               inferModelCategory(id, displayName),
		Family:                 firstNonEmpty(catalogStringField(raw, "family"), inferModelFamily(id)),
		Type:                   modelType,
		ContextWindow:          int64(catalogNumberField(limit, "context")),
		MaxOutputTokens:        int64(catalogNumberField(limit, "output")),
		InputPriceUSDPer1M:     catalogNumberField(cost, "input"),
		CacheReadPriceUSDPer1M: catalogNumberField(cost, "cache_read"),
		OutputPriceUSDPer1M:    catalogNumberField(cost, "output"),
		InputModalities:        catalogStringSliceField(modalities, "input"),
		OutputModalities:       catalogStringSliceField(modalities, "output"),
		LastUpdated:            catalogStringField(raw, "last_updated"),
		Metadata:               metadata,
	}
	model.Capabilities = catalogModelCapabilities(raw, model)
	model.SupportedParameters = catalogModelParameters(raw, model)
	return model
}

func catalogModelCapabilities(raw map[string]any, model ProviderCatalogModel) []string {
	capabilities := []string{normalizeModelModality(model.Type)}
	if catalogBoolField(raw, "attachment") {
		capabilities = append(capabilities, "attachment")
	}
	if catalogBoolField(raw, "tool_call") {
		capabilities = append(capabilities, "tool_call")
	}
	if catalogBoolField(raw, "structured_output") {
		capabilities = append(capabilities, "structured_output")
	}
	if catalogBoolField(raw, "temperature") {
		capabilities = append(capabilities, "temperature")
	}
	if catalogBoolField(raw, "open_weights") {
		capabilities = append(capabilities, "open_weights")
	}
	reasoning := catalogObjectField(raw, "reasoning")
	if catalogBoolField(reasoning, "supported") {
		capabilities = append(capabilities, "reasoning")
	}
	for _, modality := range model.InputModalities {
		switch strings.ToLower(modality) {
		case "image":
			capabilities = append(capabilities, "vision")
		case "video":
			capabilities = append(capabilities, "video_input")
		case "pdf":
			capabilities = append(capabilities, "pdf_input")
		case "audio":
			capabilities = append(capabilities, "audio_input")
		}
	}
	return catalogUniqueStrings(capabilities)
}

func catalogModelParameters(raw map[string]any, model ProviderCatalogModel) []string {
	parameters := catalogStringSliceField(raw, "supported_parameters")
	parameters = append(parameters, catalogStringSliceField(raw, "parameters")...)
	if catalogBoolField(raw, "temperature") {
		parameters = append(parameters, "temperature")
	}
	if catalogBoolField(raw, "tool_call") {
		parameters = append(parameters, "tools")
	}
	if catalogBoolField(raw, "structured_output") {
		parameters = append(parameters, "response_format")
	}
	reasoning := catalogObjectField(raw, "reasoning")
	if catalogBoolField(reasoning, "supported") {
		parameters = append(parameters, "reasoning")
		if mode := catalogStringField(catalogObjectField(raw, "extra_capabilities"), "reasoning.mode"); mode != "" {
			parameters = append(parameters, "reasoning_"+mode)
		}
	}
	for _, modality := range model.InputModalities {
		if modality != "text" {
			parameters = append(parameters, modality+"_input")
		}
	}
	return catalogUniqueStrings(parameters)
}

func ProviderCatalogModelRoute(providerID string, model ProviderCatalogModel) ModelRoute {
	modelName := firstNonEmpty(model.CanonicalName, canonicalModelName(model.ID, model.DisplayName), model.ID)
	return ModelRoute{
		ID:            stableCatalogRouteID(providerID, model.ID),
		ModelName:     modelName,
		ProviderID:    providerID,
		ProviderModel: model.ID,
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
		QualityScore:  60,
		CostScore:     60,
		Strategy:      RouteStrategyBalanced,
	}
}

func builtinProviderCatalog(includeModels bool) []ProviderCatalogEntry {
	entries := []ProviderCatalogEntry{
		builtinCatalogEntry("openai", "OpenAI", ProviderOpenAI, "https://api.openai.com/v1", "https://platform.openai.com/docs/models", []string{"gpt-5", "gpt-5-mini", "gpt-4.1-mini", "text-embedding-3-small"}),
		builtinCatalogEntry("anthropic", "Anthropic", ProviderAnthropic, "https://api.anthropic.com", "https://docs.anthropic.com", []string{"claude-sonnet-4.5", "claude-haiku-4.5"}),
		builtinCatalogEntry("google", "Google Gemini", ProviderGemini, "https://generativelanguage.googleapis.com/v1beta", "https://ai.google.dev/gemini-api/docs", []string{"gemini-2.5-pro", "gemini-2.5-flash"}),
		builtinCatalogEntry("deepseek", "DeepSeek", "deepseek", "https://api.deepseek.com", "https://platform.deepseek.com/api-docs", []string{"deepseek-chat", "deepseek-reasoner"}),
		builtinCatalogEntry("qwen", "Qwen", "qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1", "https://help.aliyun.com/zh/model-studio", []string{"qwen-max", "qwen-plus"}),
		{ID: "siliconflow", Name: "SiliconFlow", DisplayName: "SiliconFlow", Type: ProviderOpenAICompatible, BaseURL: "https://api.siliconflow.cn/v1", DocURL: "https://cloud.siliconflow.com/models", Source: "builtin"},
		{ID: "ollama", Name: "Ollama", DisplayName: "Ollama", Type: "local", BaseURL: "http://127.0.0.1:11434/v1", DocURL: "https://ollama.com", Source: "builtin"},
	}
	entries = append(entries, tencentTokenPlanCatalogEntries()...)
	entries = append(entries, customProviderCatalogEntry())
	if includeModels {
		return entries
	}
	return cloneCatalogEntries(entries, false)
}

func tencentTokenPlanCatalogEntries() []ProviderCatalogEntry {
	professional := tencentTokenPlanCatalogEntry(
		"tencent-token-plan-enterprise-pro",
		"腾讯云 Token Plan / Token Plan 企业版专业套餐",
		tencentTokenPlanEnterpriseBaseURL,
		tencentTokenPlanEnterpriseDocURL,
		[]string{
			"auto", "glm-5.2", "glm-5", "glm-5.1", "glm-5-turbo",
			"kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6", "kimi-k2.5",
			"minimax-m3", "minimax-m2.7", "minimax-m2.5",
			"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-202605", "deepseek-v4-pro-202606",
		},
	)
	lightweight := tencentTokenPlanCatalogEntry(
		"tencent-token-plan-enterprise-auto",
		"腾讯云 Token Plan / Token Plan 企业版轻享套餐",
		tencentTokenPlanEnterpriseBaseURL,
		tencentTokenPlanEnterpriseDocURL,
		[]string{"auto"},
	)
	general := tencentTokenPlanCatalogEntry(
		"tencent-token-plan-general-personal",
		"腾讯云 Token Plan / 通用 Token Plan（个人版）",
		tencentTokenPlanPersonalBaseURL,
		tencentTokenPlanPersonalDocURL,
		[]string{"tc-code-latest", "glm-5", "glm-5.1", "kimi-k2.5", "minimax-m2.5", "minimax-m2.7", "deepseek-v4-flash-202605", "deepseek-v4-pro-202606"},
	)
	hy := tencentTokenPlanCatalogEntry(
		"tencent-token-plan-hy-personal",
		"腾讯云 Token Plan / Hy Token Plan（个人版）",
		tencentTokenPlanPersonalBaseURL,
		tencentTokenPlanPersonalDocURL,
		[]string{"hy3", "hy3-preview"},
	)

	for _, entry := range []*ProviderCatalogEntry{&general, &hy} {
		entry.RequiresAcknowledgement = true
		entry.AcknowledgementTitle = "个人 Token Plan 使用确认"
		entry.AcknowledgementMessage = "该套餐适用性、使用方式和相关风险由录入此 Key 的管理员自行确认。确认后，TokenHub 将把该 Key 作为上游渠道参与路由。"
		entry.AcknowledgementVersion = tencentTokenPlanTermsVersion
	}

	return []ProviderCatalogEntry{professional, lightweight, general, hy}
}

func tencentTokenPlanCatalogEntry(id string, name string, baseURL string, docURL string, modelIDs []string) ProviderCatalogEntry {
	entry := builtinCatalogEntry(id, name, ProviderOpenAICompatible, baseURL, docURL, modelIDs)
	entry.Source = "builtin"
	return entry
}

func mergeProviderCatalogEntries(entries []ProviderCatalogEntry, additions []ProviderCatalogEntry) []ProviderCatalogEntry {
	indexes := make(map[string]int, len(entries))
	for index, entry := range entries {
		indexes[entry.ID] = index
	}
	for _, entry := range additions {
		if index, ok := indexes[entry.ID]; ok {
			entries[index] = entry
			continue
		}
		indexes[entry.ID] = len(entries)
		entries = append(entries, entry)
	}
	return entries
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

func customProviderCatalogEntry() ProviderCatalogEntry {
	return ProviderCatalogEntry{
		ID:             "custom",
		Name:           "自定义 Provider",
		DisplayName:    "自定义 Provider",
		Type:           ProviderOpenAICompatible,
		Categories:     []string{"custom"},
		CategoryCounts: map[string]int{"custom": 1},
		Source:         "builtin",
		Models: []ProviderCatalogModel{
			{
				ID:                  "custom-model",
				Name:                "custom-model",
				DisplayName:         "custom-model",
				CanonicalName:       "custom-model",
				Category:            "custom",
				Family:              "custom",
				Type:                "chat",
				InputModalities:     []string{"text"},
				OutputModalities:    []string{"text"},
				Capabilities:        []string{"chat", "temperature"},
				SupportedParameters: []string{"temperature"},
				Metadata:            map[string]string{"source": "custom"},
			},
		},
		ModelsCount: 1,
	}
}

func cloneCatalogEntries(entries []ProviderCatalogEntry, includeModels bool) []ProviderCatalogEntry {
	cloned := make([]ProviderCatalogEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = entry
		if entry.Categories != nil {
			cloned[i].Categories = append([]string(nil), entry.Categories...)
		}
		if entry.CategoryCounts != nil {
			cloned[i].CategoryCounts = map[string]int{}
			for key, value := range entry.CategoryCounts {
				cloned[i].CategoryCounts[key] = value
			}
		}
		if !includeModels {
			cloned[i].Models = nil
			continue
		}
		if entry.Models != nil {
			cloned[i].Models = append([]ProviderCatalogModel(nil), entry.Models...)
		}
	}
	return cloned
}

func sortCatalogEntries(entries []ProviderCatalogEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ID == "custom" {
			return false
		}
		if entries[j].ID == "custom" {
			return true
		}
		return strings.ToLower(entries[i].DisplayName) < strings.ToLower(entries[j].DisplayName)
	})
}

func inferProviderType(id string, baseURL string) string {
	normalized := strings.ToLower(id)
	switch {
	case normalized == "openai":
		return ProviderOpenAI
	case strings.Contains(normalized, "azure"):
		return ProviderAzureOpenAI
	case strings.Contains(normalized, "anthropic"):
		return ProviderAnthropic
	case normalized == "google" || strings.Contains(normalized, "gemini"):
		return ProviderGemini
	case strings.Contains(normalized, "deepseek"):
		return "deepseek"
	case strings.Contains(normalized, "qwen") || strings.Contains(normalized, "alibaba"):
		return "qwen"
	case strings.Contains(normalized, "ollama") || strings.Contains(normalized, "lmstudio") || strings.Contains(normalized, "local"):
		return "local"
	default:
		return ProviderOpenAICompatible
	}
}

func normalizeProviderBaseURL(id string, raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	switch strings.ToLower(id) {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return firstNonEmpty(raw, "https://api.anthropic.com")
	case "google":
		return firstNonEmpty(raw, "https://generativelanguage.googleapis.com/v1beta")
	case "ollama":
		return firstNonEmpty(raw, "http://127.0.0.1:11434/v1")
	case "lmstudio":
		return firstNonEmpty(raw, "http://127.0.0.1:1234/v1")
	default:
		return normalizeOpenAICompatibleBaseURL(id, raw)
	}
}

func normalizeOpenAICompatibleBaseURL(id string, raw string) string {
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

func normalizeModelModality(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "embed"):
		return "embedding"
	case strings.Contains(value, "image"):
		return "image"
	case strings.Contains(value, "audio"):
		return "audio"
	default:
		return "chat"
	}
}

func inferModelFamily(id string) string {
	normalized := strings.ToLower(id)
	for _, family := range []string{"gpt", "claude", "gemini", "deepseek", "qwen", "llama", "mistral", "kimi", "doubao", "glm"} {
		if strings.Contains(normalized, family) {
			return family
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
	normalized := strings.ToLower(strings.Join([]string{id, displayName}, " "))
	switch {
	case strings.Contains(normalized, "codex"):
		return "codex"
	case strings.Contains(normalized, "gpt") || strings.Contains(normalized, "openai") || strings.Contains(normalized, "o1") || strings.Contains(normalized, "o3") || strings.Contains(normalized, "o4"):
		return "openai"
	case strings.Contains(normalized, "claude") || strings.Contains(normalized, "anthropic"):
		return "claude"
	case strings.Contains(normalized, "deepseek"):
		return "deepseek"
	case strings.Contains(normalized, "gemini") || strings.Contains(normalized, "google/"):
		return "gemini"
	case strings.Contains(normalized, "qwen") || strings.Contains(normalized, "dashscope") || strings.Contains(normalized, "alibaba"):
		return "qwen"
	case strings.Contains(normalized, "glm") || strings.Contains(normalized, "zhipu"):
		return "glm"
	case strings.Contains(normalized, "kimi") || strings.Contains(normalized, "moonshot"):
		return "kimi"
	case strings.Contains(normalized, "doubao") || strings.Contains(normalized, "volcengine"):
		return "doubao"
	case strings.Contains(normalized, "ernie"):
		return "ernie"
	case strings.Contains(normalized, "baichuan"):
		return "baichuan"
	case strings.Contains(normalized, "minimax") || strings.Contains(normalized, "hailuo"):
		return "minimax"
	case strings.Contains(normalized, "step-"):
		return "stepfun"
	case strings.Contains(normalized, "wanx"):
		return "wanx"
	case strings.Contains(normalized, "paddleocr"):
		return "paddlepaddle"
	case strings.Contains(normalized, "phi-"):
		return "microsoft"
	case strings.Contains(normalized, "llama") || strings.Contains(normalized, "meta/"):
		return "llama"
	case strings.Contains(normalized, "mistral"):
		return "mistral"
	case strings.Contains(normalized, "grok") || strings.Contains(normalized, "xai/"):
		return "grok"
	default:
		return "custom"
	}
}

func standardModelCategory(category string) string {
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		return "custom"
	}
	if standardModelCategories[category] {
		return category
	}
	return inferModelCategory(category, "")
}

func canonicalModelName(id string, displayName string) string {
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
	value = strings.ReplaceAll(value, "--", "-")
	value = strings.Trim(value, "-")
	value = normalizeCompactModelVersion(value, "deepseek")
	value = normalizeCompactModelVersion(value, "claude")
	value = normalizeCompactModelVersion(value, "gemini")
	value = normalizeCompactModelVersion(value, "qwen")
	value = normalizeCompactModelVersion(value, "gpt")
	value = normalizeCompactModelVersion(value, "glm")
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

func catalogCategorySummary(models []ProviderCatalogModel) ([]string, map[string]int) {
	counts := map[string]int{}
	for _, model := range models {
		category := standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.ID, model.DisplayName)))
		if category == "" {
			category = "custom"
		}
		counts[category]++
	}
	categories := make([]string, 0, len(counts))
	for category := range counts {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return categories, counts
}

func stableCatalogRouteID(providerID string, modelID string) string {
	sum := sha256.Sum256([]byte(providerID + ":" + modelID))
	return "route_catalog_" + hex.EncodeToString(sum[:])[:16]
}

func sanitizeIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteRune(ch)
			continue
		}
		if ch == '-' || ch == '_' || ch == '.' {
			builder.WriteRune('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "custom"
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func catalogObjectField(raw map[string]any, key string) map[string]any {
	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := raw
		for _, part := range parts {
			next, ok := current[part].(map[string]any)
			if !ok {
				return nil
			}
			current = next
		}
		return current
	}
	if value, ok := raw[key].(map[string]any); ok {
		return value
	}
	return nil
}

func catalogStringField(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := raw
		for index, part := range parts {
			value, ok := current[part]
			if !ok {
				return ""
			}
			if index == len(parts)-1 {
				if text, ok := value.(string); ok {
					return strings.TrimSpace(text)
				}
				return ""
			}
			next, ok := value.(map[string]any)
			if !ok {
				return ""
			}
			current = next
		}
	}
	if value, ok := raw[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func catalogNumberField(raw map[string]any, key string) float64 {
	if raw == nil {
		return 0
	}
	switch value := raw[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	default:
		return 0
	}
}

func catalogBoolField(raw map[string]any, key string) bool {
	if raw == nil {
		return false
	}
	value, ok := raw[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed == "true" || typed == "yes" || typed == "1"
	default:
		return false
	}
}

func catalogStringSliceField(raw map[string]any, key string) []string {
	if raw == nil {
		return nil
	}
	value, ok := raw[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return catalogUniqueStrings(typed)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				items = append(items, text)
			}
		}
		return catalogUniqueStrings(items)
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

func catalogUniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result
}
