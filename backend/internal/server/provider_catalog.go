package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// Keep the historical snapshot ID so existing databases are upgraded in
	// place instead of retaining a stale second catalog snapshot.
	providerCatalogSnapshotID   = "public-provider-conf"
	providerCatalogMinProviders = 20
	providerCatalogMinModels    = 100
	providerCatalogMinRetention = 0.8
)

type providerCatalogService struct {
	store          Store
	catalogFile    string
	upstreamURL    string
	upstreamClient providerCatalogHTTPClient
}

func newProviderCatalogService(store Store, catalogFile string) *providerCatalogService {
	catalogFile = strings.TrimSpace(catalogFile)
	if catalogFile == "" {
		catalogFile = defaultProviderCatalogFile()
	}
	return &providerCatalogService{
		store:          store,
		catalogFile:    catalogFile,
		upstreamURL:    providerCatalogUpstreamURL,
		upstreamClient: &http.Client{Timeout: providerCatalogUpstreamTimeout},
	}
}

// InitializeProviderCatalog refreshes the database snapshot from the tracked
// local catalog before the backend starts accepting requests.
func (s *Server) InitializeProviderCatalog(ctx context.Context) (bool, error) {
	return s.providerCatalog.Initialize(ctx)
}

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

func (s *providerCatalogService) List(ctx context.Context, refresh bool) ([]ProviderCatalogEntry, string, error) {
	if refresh {
		return s.reload(ctx)
	}
	entries, source, _, err := s.loadStored(false)
	return entries, source, err
}

func (s *providerCatalogService) Get(ctx context.Context, id string, refresh bool) (ProviderCatalogEntry, string, bool, error) {
	id = strings.TrimSpace(id)
	if id == "custom" {
		return customProviderCatalogEntry(), "builtin", true, nil
	}
	if refresh {
		if _, source, err := s.reload(ctx); err != nil {
			return ProviderCatalogEntry{}, source, false, err
		}
	}
	entries, source, _, err := s.loadStored(true)
	if err != nil {
		return ProviderCatalogEntry{}, source, false, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, source, true, nil
		}
	}
	return ProviderCatalogEntry{}, source, false, nil
}

func (s *providerCatalogService) Initialize(ctx context.Context) (bool, error) {
	initialized := false
	err := s.store.RunClusterOperation(ctx, "provider-catalog-reload", func(context.Context) error {
		entries, _, _, err := s.loadStored(false)
		if err != nil {
			return err
		}
		if _, err := s.reloadLocked(entries); err != nil {
			return err
		}
		initialized = true
		return nil
	})
	return initialized, err
}

func (s *providerCatalogService) loadStored(includeModels bool) ([]ProviderCatalogEntry, string, time.Time, error) {
	entries, source, fetchedAt, found, err := s.store.LoadProviderCatalogSnapshot(includeModels)
	if err != nil {
		return nil, source, fetchedAt, err
	}
	if found && len(entries) > 0 {
		return entries, source, fetchedAt, nil
	}
	entries = builtinProviderCatalog(true)
	sortCatalogEntries(entries)
	fetchedAt = time.Now().UTC()
	if err := s.store.SaveProviderCatalogSnapshot(entries, "builtin", fetchedAt); err != nil {
		return nil, "builtin", fetchedAt, err
	}
	if !includeModels {
		entries = cloneCatalogEntries(entries, false)
	}
	return entries, "builtin", fetchedAt, nil
}

func seedBuiltinProviderCatalog(store Store) error {
	_, _, _, found, err := store.LoadProviderCatalogSnapshot(false)
	if err != nil || found {
		return err
	}
	entries := builtinProviderCatalog(true)
	sortCatalogEntries(entries)
	return store.SaveProviderCatalogSnapshot(entries, "builtin", time.Now().UTC())
}

func (s *providerCatalogService) reload(ctx context.Context) ([]ProviderCatalogEntry, string, error) {
	var (
		refreshed []ProviderCatalogEntry
		source    = providerCatalogLocalSource
	)
	err := s.store.RunClusterOperation(ctx, "provider-catalog-reload", func(operationCtx context.Context) error {
		previous, _, _, err := s.loadStored(false)
		if err != nil {
			return err
		}
		refreshed, source, err = s.refreshLocked(operationCtx, previous)
		return err
	})
	if err != nil {
		return nil, source, err
	}
	return refreshed, source, nil
}

// reloadLocked refreshes the snapshot while provider-catalog-reload is held.
func (s *providerCatalogService) reloadLocked(previous []ProviderCatalogEntry) ([]ProviderCatalogEntry, error) {
	entries, err := loadLocalProviderCatalog(s.catalogFile)
	if err != nil {
		return nil, err
	}
	entries, err = prepareProviderCatalogRefresh(entries, previous)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveProviderCatalogSnapshot(entries, providerCatalogLocalSource, time.Now().UTC()); err != nil {
		return nil, err
	}
	return cloneCatalogEntries(entries, false), nil
}

func prepareProviderCatalogRefresh(entries []ProviderCatalogEntry, previous []ProviderCatalogEntry) ([]ProviderCatalogEntry, error) {
	if err := validateProviderCatalogRefresh(entries, previous); err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.ID != "custom" {
			filtered = append(filtered, entry)
		}
	}
	entries = append(filtered, customProviderCatalogEntry())
	sortCatalogEntries(entries)
	return entries, nil
}

func validateProviderCatalogRefresh(entries []ProviderCatalogEntry, previous []ProviderCatalogEntry) error {
	providerCount, modelCount, ids := providerCatalogStats(entries)
	if providerCount < providerCatalogMinProviders || modelCount < providerCatalogMinModels {
		return NewHTTPError(http.StatusBadGateway, "provider_catalog_incomplete", "Provider catalog file is incomplete")
	}
	for _, requiredID := range []string{"openai", "anthropic", "google"} {
		if !ids[requiredID] {
			return NewHTTPError(http.StatusBadGateway, "provider_catalog_incomplete", "Provider catalog file is missing required providers")
		}
	}
	previousProviders, previousModels, _ := providerCatalogStats(previous)
	if previousProviders >= providerCatalogMinProviders && float64(providerCount) < float64(previousProviders)*providerCatalogMinRetention {
		return NewHTTPError(http.StatusBadGateway, "provider_catalog_incomplete", "Provider catalog file provider count dropped unexpectedly")
	}
	if previousModels >= providerCatalogMinModels && float64(modelCount) < float64(previousModels)*providerCatalogMinRetention {
		return NewHTTPError(http.StatusBadGateway, "provider_catalog_incomplete", "Provider catalog file model count dropped unexpectedly")
	}
	return nil
}

func providerCatalogStats(entries []ProviderCatalogEntry) (int, int, map[string]bool) {
	providerCount := 0
	modelCount := 0
	ids := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "custom" {
			continue
		}
		providerCount++
		modelCount += entry.ModelsCount
		ids[entry.ID] = true
	}
	return providerCount, modelCount, ids
}

func loadLocalProviderCatalog(catalogFile string) ([]ProviderCatalogEntry, error) {
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		return nil, fmt.Errorf("read provider catalog %s: %w", catalogFile, err)
	}
	entries, err := parseProviderCatalog(content, providerCatalogLocalSource)
	if err != nil {
		return nil, fmt.Errorf("parse provider catalog %s: %w", catalogFile, err)
	}
	return entries, nil
}

func parseProviderCatalog(content []byte, source string) ([]ProviderCatalogEntry, error) {
	var payload struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, err
	}
	if len(payload.Providers) == 0 {
		return nil, fmt.Errorf("provider catalog has no providers")
	}
	entries := make([]ProviderCatalogEntry, 0, len(payload.Providers))
	for id, raw := range payload.Providers {
		entry := normalizeProviderCatalogEntry(id, raw)
		if entry.ID == "" || entry.Name == "" {
			continue
		}
		entry.Source = source
		for index := range entry.Models {
			if entry.Models[index].Metadata == nil {
				entry.Models[index].Metadata = map[string]string{}
			}
			entry.Models[index].Metadata["source"] = source
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
		Source:      "local-provider-catalog",
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
	canonicalName := strings.TrimSpace(catalogStringField(raw, "canonical_name"))
	if canonicalName == "" {
		canonicalName = canonicalModelName(id, displayName)
	} else {
		canonicalName = canonicalModelName(canonicalName, canonicalName)
	}
	metadata := map[string]string{
		"source": "local-provider-catalog",
	}
	for _, key := range []string{"knowledge", "release_date", "last_updated"} {
		if value := catalogStringField(raw, key); value != "" {
			metadata[key] = value
		}
	}
	if rawOptions, ok := raw["reasoning_options"].([]any); ok {
		for _, rawOption := range rawOptions {
			option, ok := rawOption.(map[string]any)
			if !ok || !strings.EqualFold(catalogStringField(option, "type"), "effort") {
				continue
			}
			if values := catalogOrderedStringSliceField(option, "values"); len(values) > 0 {
				metadata["reasoning_effort_options"] = strings.Join(values, ",")
			}
			break
		}
	}
	model := ProviderCatalogModel{
		ID:                     id,
		Name:                   name,
		DisplayName:            displayName,
		CanonicalName:          canonicalName,
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

func builtinProviderCatalog(includeModels bool) []ProviderCatalogEntry {
	entries := []ProviderCatalogEntry{
		builtinCatalogEntry("openai", "OpenAI", ProviderOpenAI, "https://api.openai.com/v1", "https://platform.openai.com/docs/models", []string{"gpt-5", "gpt-5-mini", "gpt-4.1-mini", "text-embedding-3-small"}),
		builtinCatalogEntry("anthropic", "Anthropic", ProviderAnthropic, "https://api.anthropic.com", "https://docs.anthropic.com", []string{"claude-sonnet-4.5", "claude-haiku-4.5"}),
		builtinCatalogEntry("google", "Google Gemini", ProviderGemini, "https://generativelanguage.googleapis.com/v1beta", "https://ai.google.dev/gemini-api/docs", []string{"gemini-2.5-pro", "gemini-2.5-flash"}),
		deepSeekBuiltinCatalogEntry(),
		builtinCatalogEntry("qwen", "Qwen", "qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1", "https://help.aliyun.com/zh/model-studio", []string{"qwen-max", "qwen-plus"}),
		{ID: "siliconflow", Name: "SiliconFlow", DisplayName: "SiliconFlow", Type: ProviderOpenAICompatible, BaseURL: "https://api.siliconflow.cn/v1", DocURL: "https://cloud.siliconflow.com/models", Source: "builtin"},
		{ID: "ollama", Name: "Ollama", DisplayName: "Ollama", Type: "local", BaseURL: "http://127.0.0.1:11434/v1", DocURL: "https://ollama.com", Source: "builtin"},
		kronkCatalogEntry(),
		customProviderCatalogEntry(),
	}
	if includeModels {
		return entries
	}
	return cloneCatalogEntries(entries, false)
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

func CustomProviderCatalogFromUpstream(ctx context.Context, client *http.Client, req ProviderCreateRequest) (ProviderCatalogEntry, error) {
	if err := validateProviderHeaderSupport(req.Type, req.Headers); err != nil {
		return ProviderCatalogEntry{}, err
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_base_url_required", "Base URL is required to load upstream models")
	}
	providerType := strings.ToLower(strings.TrimSpace(req.Type))
	modelsURL := strings.TrimRight(baseURL, "/") + "/models"
	if providerType == ProviderAnthropic {
		modelsURL = anthropicEndpointURL(baseURL, "/v1/models")
	}
	endpoint, err := url.Parse(modelsURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	if err := validateProviderUpstreamBaseURL(endpoint, allowedProviderUpstreamCIDRs(), providerUpstreamLoopbackAllowed()); err != nil {
		return ProviderCatalogEntry{}, err
	}
	client = ssrfGuardedProviderClient(client)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_request_failed", "Failed to create upstream models request")
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if providerType == ProviderGemini {
		if apiKey != "" {
			query := endpoint.Query()
			query.Set("key", apiKey)
			endpoint.RawQuery = query.Encode()
			httpReq.URL = endpoint
		}
	} else if providerType == ProviderAnthropic {
		if apiKey != "" {
			provider := Provider{Type: providerType, APIKey: apiKey, Options: req.Options}
			if err := configureAnthropicProviderAuth(&provider, req.AnthropicAuthType); err != nil {
				return ProviderCatalogEntry{}, err
			}
			applyAnthropicProviderAuth(httpReq, provider)
		}
		version := strings.TrimSpace(req.Options["anthropic_version"])
		if version == "" {
			version = "2023-06-01"
		}
		httpReq.Header.Set("anthropic-version", version)
	} else if apiKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+apiKey)
	}
	headers, err := normalizeProviderHeaders(req.Headers)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	applyProviderHeaders(httpReq.Header, headers)
	resp, err := client.Do(httpReq)
	if err != nil {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_request_failed", "Failed to request upstream models")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		if providerType == ProviderKronk {
			return ProviderCatalogEntry{}, checkProviderResponseForProvider(resp, Provider{
				Type: ProviderKronk, APIKey: apiKey, Headers: headers, SensitiveHeaders: req.SensitiveHeaders,
			})
		}
		return ProviderCatalogEntry{}, NewHTTPError(statusForProvider(resp.StatusCode), "provider_models_upstream_error", resp.Status)
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&payload); err != nil {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_invalid_response", "Upstream models response is invalid")
	}
	models := customProviderModelsFromPayload(payload)
	if len(models) == 0 {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_empty", "Upstream did not return any models")
	}
	categories, categoryCounts := catalogCategorySummary(models)
	name := firstNonEmpty(strings.TrimSpace(req.Name), "自定义渠道商")
	return ProviderCatalogEntry{
		ID:             "custom",
		Name:           name,
		DisplayName:    name,
		Type:           firstNonEmpty(strings.TrimSpace(req.Type), ProviderOpenAICompatible),
		BaseURL:        baseURL,
		Categories:     categories,
		CategoryCounts: categoryCounts,
		ModelsCount:    len(models),
		Source:         "custom-upstream",
		Models:         models,
	}, nil
}

func customProviderModelsFromPayload(payload map[string]any) []ProviderCatalogModel {
	rawModels, _ := payload["data"].([]any)
	if len(rawModels) == 0 {
		rawModels, _ = payload["models"].([]any)
	}
	models := make([]ProviderCatalogModel, 0, len(rawModels))
	seen := map[string]bool{}
	for _, raw := range rawModels {
		id, displayName, object, ownedBy := customProviderModelFields(raw)
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		displayName = firstNonEmpty(displayName, id)
		modelType := normalizeModelModality(strings.Join([]string{id, displayName, object}, " "))
		metadata := map[string]string{"source": "custom-upstream"}
		if object != "" {
			metadata["object"] = object
		}
		if ownedBy != "" {
			metadata["owned_by"] = ownedBy
		}
		models = append(models, ProviderCatalogModel{
			ID:                  id,
			Name:                id,
			DisplayName:         displayName,
			CanonicalName:       canonicalModelName(id, displayName),
			Category:            inferModelCategory(id, displayName),
			Family:              inferModelFamily(id),
			Type:                modelType,
			InputModalities:     []string{"text"},
			OutputModalities:    []string{"text"},
			Capabilities:        []string{modelType},
			SupportedParameters: []string{},
			Metadata:            metadata,
		})
	}
	sort.SliceStable(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
	return models
}

func customProviderModelFields(raw any) (string, string, string, string) {
	switch item := raw.(type) {
	case string:
		return item, item, "", ""
	case map[string]any:
		id := firstNonEmpty(catalogStringField(item, "id"), catalogStringField(item, "name"), catalogStringField(item, "model"))
		displayName := firstNonEmpty(catalogStringField(item, "display_name"), catalogStringField(item, "name"), id)
		return id, displayName, catalogStringField(item, "object"), catalogStringField(item, "owned_by")
	default:
		return "", "", "", ""
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
	normalizedBaseURL := strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	switch {
	case normalized == "openai":
		return ProviderOpenAI
	case strings.Contains(normalized, "azure"):
		return ProviderAzureOpenAI
	case strings.Contains(normalized, "anthropic") || strings.Contains(normalizedBaseURL, "/anthropic/") || strings.HasSuffix(normalizedBaseURL, "/anthropic"):
		return ProviderAnthropic
	case normalized == "google" || strings.Contains(normalized, "gemini"):
		return ProviderGemini
	case strings.Contains(normalized, "deepseek"):
		return "deepseek"
	case strings.Contains(normalized, "qwen") || strings.Contains(normalized, "alibaba"):
		return "qwen"
	case strings.Contains(normalized, "ollama") || strings.Contains(normalized, "lmstudio") || strings.Contains(normalized, "local"):
		return "local"
	case strings.Contains(normalized, "kronk"):
		return ProviderKronk
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
	case ProviderKronk:
		return firstNonEmpty(raw, kronkDefaultBaseURL)
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

func catalogOrderedStringSliceField(raw map[string]any, key string) []string {
	if raw == nil {
		return nil
	}
	var values []string
	switch typed := raw[key].(type) {
	case []string:
		values = typed
	case []any:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
	case string:
		values = []string{typed}
	}
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
	return result
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
