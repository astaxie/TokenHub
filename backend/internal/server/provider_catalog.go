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
	store           Store
	catalogFile     string
	upstreamURL     string
	upstreamClient  providerCatalogHTTPClient
	catalogTypes    map[string]string
	modelCategories []providerModelCategoryDefinition
	builtinEntries  []ProviderCatalogEntry
	defaultType     string
}

func newProviderCatalogService(store Store, catalogFile string, clients ...providerCatalogHTTPClient) *providerCatalogService {
	catalogFile = strings.TrimSpace(catalogFile)
	if catalogFile == "" {
		catalogFile = defaultProviderCatalogFile()
	}
	upstreamClient := providerCatalogHTTPClient(&http.Client{Timeout: providerCatalogUpstreamTimeout})
	if len(clients) > 0 && clients[0] != nil {
		upstreamClient = clients[0]
	}
	return &providerCatalogService{
		store:          store,
		catalogFile:    catalogFile,
		upstreamURL:    providerCatalogUpstreamURL,
		upstreamClient: upstreamClient,
		defaultType:    defaultProviderCatalogProviderType(),
	}
}

func (s *providerCatalogService) UsePluginCatalogTypes(registry *AdapterRegistry) {
	s.catalogTypes = providerCatalogTypesFromRegistry(registry)
	s.modelCategories = providerModelCategoryDefinitionsFromRegistry(registry)
	s.builtinEntries = providerCatalogSeedEntriesFromRegistry(registry)
	if defaultType := providerCatalogDefaultTypeFromRegistry(registry); defaultType != "" {
		s.defaultType = defaultType
	}
}

func (s *providerCatalogService) defaultProviderType() string {
	if s == nil {
		return defaultProviderCatalogProviderType()
	}
	if providerType := strings.TrimSpace(s.defaultType); providerType != "" {
		return providerType
	}
	return defaultProviderCatalogProviderType()
}

func defaultProviderCatalogProviderType() string {
	return strings.TrimSpace(builtinProviderPluginCatalogDefaultType())
}

// InitializeProviderCatalog refreshes the database snapshot from the tracked
// local catalog before the backend starts accepting requests.
func (s *Server) InitializeProviderCatalog(ctx context.Context) (bool, error) {
	return s.providerCatalog.Initialize(ctx)
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
		return s.customProviderCatalogEntry(), "builtin", true, nil
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
	entries = s.builtinProviderCatalog(true)
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
	entries := builtinProviderPluginCatalogSeedEntries()
	if len(entries) == 0 {
		entries = builtinProviderCatalog(true)
	}
	if !providerCatalogHasEntry(entries, "custom") {
		entries = append(entries, customProviderCatalogEntryWithType(defaultProviderCatalogProviderType()))
	}
	sortCatalogEntries(entries)
	return store.SaveProviderCatalogSnapshot(entries, "builtin", time.Now().UTC())
}

func (s *providerCatalogService) builtinProviderCatalog(includeModels bool) []ProviderCatalogEntry {
	if len(s.builtinEntries) == 0 {
		return builtinProviderCatalog(includeModels)
	}
	entries := cloneCatalogEntries(s.builtinEntries, includeModels)
	if !providerCatalogHasEntry(entries, "custom") {
		entries = append(entries, s.customProviderCatalogEntry())
	}
	sortCatalogEntries(entries)
	return entries
}

func providerCatalogHasEntry(entries []ProviderCatalogEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
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
	entries, err := s.loadLocalProviderCatalog()
	if err != nil {
		return nil, err
	}
	entries, err = prepareProviderCatalogRefreshWithDefault(entries, previous, s.defaultType)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveProviderCatalogSnapshot(entries, providerCatalogLocalSource, time.Now().UTC()); err != nil {
		return nil, err
	}
	return cloneCatalogEntries(entries, false), nil
}

func prepareProviderCatalogRefresh(entries []ProviderCatalogEntry, previous []ProviderCatalogEntry) ([]ProviderCatalogEntry, error) {
	return prepareProviderCatalogRefreshWithDefault(entries, previous, defaultProviderCatalogProviderType())
}

func prepareProviderCatalogRefreshWithDefault(entries []ProviderCatalogEntry, previous []ProviderCatalogEntry, defaultType string) ([]ProviderCatalogEntry, error) {
	if err := validateProviderCatalogRefresh(entries, previous); err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.ID != "custom" {
			filtered = append(filtered, entry)
		}
	}
	entries = append(filtered, customProviderCatalogEntryWithType(defaultType))
	sortCatalogEntries(entries)
	return entries, nil
}

func validateProviderCatalogRefresh(entries []ProviderCatalogEntry, previous []ProviderCatalogEntry) error {
	providerCount, modelCount, ids := providerCatalogStats(entries)
	if providerCount < providerCatalogMinProviders || modelCount < providerCatalogMinModels {
		return NewHTTPError(http.StatusBadGateway, "provider_catalog_incomplete", "Provider catalog file is incomplete")
	}
	for _, requiredID := range builtinProviderCatalogRequiredProviderIDs() {
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
	return loadLocalProviderCatalogWithTypes(catalogFile, nil)
}

func (s *providerCatalogService) loadLocalProviderCatalog() ([]ProviderCatalogEntry, error) {
	return loadLocalProviderCatalogWithPolicy(s.catalogFile, s.catalogTypes, s.defaultType, s.modelCategories)
}

func loadLocalProviderCatalogWithTypes(catalogFile string, catalogTypes map[string]string) ([]ProviderCatalogEntry, error) {
	return loadLocalProviderCatalogWithDefault(catalogFile, catalogTypes, defaultProviderCatalogProviderType())
}

func loadLocalProviderCatalogWithDefault(catalogFile string, catalogTypes map[string]string, defaultType string) ([]ProviderCatalogEntry, error) {
	return loadLocalProviderCatalogWithPolicy(catalogFile, catalogTypes, defaultType, nil)
}

func loadLocalProviderCatalogWithPolicy(catalogFile string, catalogTypes map[string]string, defaultType string, modelCategories []providerModelCategoryDefinition) ([]ProviderCatalogEntry, error) {
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		return nil, fmt.Errorf("read provider catalog %s: %w", catalogFile, err)
	}
	entries, err := parseProviderCatalogWithPolicy(content, providerCatalogLocalSource, catalogTypes, defaultType, modelCategories)
	if err != nil {
		return nil, fmt.Errorf("parse provider catalog %s: %w", catalogFile, err)
	}
	return entries, nil
}

func parseProviderCatalog(content []byte, source string) ([]ProviderCatalogEntry, error) {
	return parseProviderCatalogWithTypes(content, source, nil)
}

func parseProviderCatalogWithTypes(content []byte, source string, catalogTypes map[string]string) ([]ProviderCatalogEntry, error) {
	return parseProviderCatalogWithDefault(content, source, catalogTypes, defaultProviderCatalogProviderType())
}

func parseProviderCatalogWithDefault(content []byte, source string, catalogTypes map[string]string, defaultType string) ([]ProviderCatalogEntry, error) {
	return parseProviderCatalogWithPolicy(content, source, catalogTypes, defaultType, nil)
}

func parseProviderCatalogWithPolicy(content []byte, source string, catalogTypes map[string]string, defaultType string, modelCategories []providerModelCategoryDefinition) ([]ProviderCatalogEntry, error) {
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
		entry := normalizeProviderCatalogEntryWithPolicy(id, raw, catalogTypes, defaultType, modelCategories)
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
	return normalizeProviderCatalogEntryWithTypes(id, raw, nil)
}

func normalizeProviderCatalogEntryWithTypes(id string, raw map[string]any, catalogTypes map[string]string) ProviderCatalogEntry {
	return normalizeProviderCatalogEntryWithDefault(id, raw, catalogTypes, defaultProviderCatalogProviderType())
}

func normalizeProviderCatalogEntryWithDefault(id string, raw map[string]any, catalogTypes map[string]string, defaultType string) ProviderCatalogEntry {
	return normalizeProviderCatalogEntryWithPolicy(id, raw, catalogTypes, defaultType, nil)
}

func normalizeProviderCatalogEntryWithPolicy(id string, raw map[string]any, catalogTypes map[string]string, defaultType string, modelCategories []providerModelCategoryDefinition) ProviderCatalogEntry {
	baseURL := firstNonEmpty(catalogStringField(raw, "base_url"), catalogStringField(raw, "api"))
	fallbackType := strings.TrimSpace(defaultType)
	if fallbackType == "" {
		fallbackType = defaultProviderCatalogProviderType()
	}
	entry := ProviderCatalogEntry{
		ID:          firstNonEmpty(catalogStringField(raw, "id"), id),
		Name:        firstNonEmpty(catalogStringField(raw, "name"), catalogStringField(raw, "display_name"), id),
		DisplayName: firstNonEmpty(catalogStringField(raw, "display_name"), catalogStringField(raw, "name"), id),
		BaseURL:     normalizeProviderBaseURL(id, baseURL),
		DocURL:      firstNonEmpty(catalogStringField(raw, "doc_url"), catalogStringField(raw, "doc")),
		Source:      "local-provider-catalog",
	}
	entry.Type = firstNonEmpty(catalogStringField(raw, "type"), catalogTypes[strings.TrimSpace(entry.ID)], fallbackType)
	if rawModels, ok := raw["models"].([]any); ok {
		entry.Models = make([]ProviderCatalogModel, 0, len(rawModels))
		for _, rawModel := range rawModels {
			modelMap, ok := rawModel.(map[string]any)
			if !ok {
				continue
			}
			model := normalizeProviderCatalogModelWithCategories(modelMap, modelCategories)
			if model.ID == "" {
				continue
			}
			entry.Models = append(entry.Models, model)
		}
	}
	entry.ModelsCount = len(entry.Models)
	entry.Categories, entry.CategoryCounts = catalogCategorySummaryWithDefinitions(entry.Models, modelCategories)
	return entry
}

func normalizeProviderCatalogModel(raw map[string]any) ProviderCatalogModel {
	return normalizeProviderCatalogModelWithCategories(raw, nil)
}

func normalizeProviderCatalogModelWithCategories(raw map[string]any, modelCategories []providerModelCategoryDefinition) ProviderCatalogModel {
	id := firstNonEmpty(catalogStringField(raw, "id"), catalogStringField(raw, "name"))
	name := firstNonEmpty(catalogStringField(raw, "name"), id)
	displayName := firstNonEmpty(catalogStringField(raw, "display_name"), name)
	modelType := firstNonEmpty(catalogStringField(raw, "type"), "chat")
	cost := catalogObjectField(raw, "cost")
	limit := catalogObjectField(raw, "limit")
	modalities := catalogObjectField(raw, "modalities")
	canonicalName := strings.TrimSpace(catalogStringField(raw, "canonical_name"))
	if canonicalName == "" {
		canonicalName = canonicalModelNameWithDefinitions(id, displayName, modelCategories)
	} else {
		canonicalName = canonicalModelNameWithDefinitions(canonicalName, canonicalName, modelCategories)
	}
	metadata := map[string]string{
		"source": "local-provider-catalog",
	}
	for _, key := range []string{"knowledge", "release_date", "last_updated", "endpoints", "billing_mode", "pricing_unit"} {
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
		ID:                        id,
		Name:                      name,
		DisplayName:               displayName,
		CanonicalName:             canonicalName,
		Category:                  catalogModelCategoryWithDefinitions(raw, id, displayName, modelCategories),
		Family:                    firstNonEmpty(catalogStringField(raw, "family"), inferModelFamilyWithDefinitions(id, modelCategories)),
		Type:                      modelType,
		ContextWindow:             int64(catalogNumberField(limit, "context")),
		MaxOutputTokens:           int64(catalogNumberField(limit, "output")),
		InputPriceUSDPer1M:        catalogNumberField(cost, "input"),
		CacheReadPriceUSDPer1M:    catalogNumberField(cost, "cache_read"),
		CacheWritePriceUSDPer1M:   catalogNumberField(cost, "cache_write"),
		CacheWrite5mPriceUSDPer1M: catalogNumberField(cost, "cache_write_5m"),
		CacheWrite1hPriceUSDPer1M: catalogNumberField(cost, "cache_write_1h"),
		OutputPriceUSDPer1M:       catalogNumberField(cost, "output"),
		CacheWritePriceConfiguration: CacheWritePriceConfiguration{
			CacheWritePriceConfigured:   catalogNumberFieldConfigured(cost, "cache_write"),
			CacheWrite5mPriceConfigured: catalogNumberFieldConfigured(cost, "cache_write_5m"),
			CacheWrite1hPriceConfigured: catalogNumberFieldConfigured(cost, "cache_write_1h"),
		},
		InputModalities:  catalogStringSliceField(modalities, "input"),
		OutputModalities: catalogStringSliceField(modalities, "output"),
		LastUpdated:      catalogStringField(raw, "last_updated"),
		Metadata:         metadata,
	}
	model.Capabilities = catalogModelCapabilities(raw, model)
	model.SupportedParameters = catalogModelParameters(raw, model)
	return model
}

func catalogModelCategory(raw map[string]any, id string, displayName string) string {
	return catalogModelCategoryWithDefinitions(raw, id, displayName, nil)
}

func catalogModelCategoryWithDefinitions(raw map[string]any, id string, displayName string, modelCategories []providerModelCategoryDefinition) string {
	if category := strings.TrimSpace(catalogStringField(raw, "category")); category != "" {
		return standardModelCategoryWithDefinitions(category, modelCategories)
	}
	return inferModelCategoryWithDefinitions(id, displayName, modelCategories)
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

func customProviderCatalogEntry() ProviderCatalogEntry {
	return customProviderCatalogEntryWithType(defaultProviderCatalogProviderType())
}

func (s *providerCatalogService) customProviderCatalogEntry() ProviderCatalogEntry {
	return customProviderCatalogEntryWithType(s.defaultProviderType())
}

func customProviderCatalogEntryWithType(providerType string) ProviderCatalogEntry {
	return ProviderCatalogEntry{
		ID:             "custom",
		Name:           "自定义 Provider",
		DisplayName:    "自定义 Provider",
		Type:           firstNonEmpty(strings.TrimSpace(providerType), defaultProviderCatalogProviderType()),
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
	return CustomProviderCatalogFromUpstreamWithDescriptor(ctx, client, req, AdapterDescriptor{})
}

func CustomProviderCatalogFromUpstreamWithDescriptor(ctx context.Context, client *http.Client, req ProviderCreateRequest, descriptor AdapterDescriptor) (ProviderCatalogEntry, error) {
	if err := validateProviderHeaderSupport(req.Type, req.Headers); err != nil {
		return ProviderCatalogEntry{}, err
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_base_url_required", "Base URL is required to load upstream models")
	}
	if err := ValidateProviderUpstreamBaseURL(baseURL); err != nil {
		return ProviderCatalogEntry{}, err
	}
	providerType := strings.ToLower(strings.TrimSpace(req.Type))
	discovery := providerModelDiscoveryPolicy(descriptor)
	modelsURL := providerModelDiscoveryURL(baseURL, discovery.Path)
	endpoint, err := url.Parse(modelsURL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	if err := validateProviderUpstreamURLSyntax(endpoint); err != nil {
		return ProviderCatalogEntry{}, err
	}
	client = ssrfGuardedProviderClient(client)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_request_failed", "Failed to create upstream models request")
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if err := applyProviderModelDiscoveryAuth(httpReq, endpoint, req, descriptor, discovery); err != nil {
		return ProviderCatalogEntry{}, err
	}
	for name, value := range providerModelDiscoveryHeaders(req.Options, discovery.Headers) {
		httpReq.Header.Set(name, value)
	}
	headers, err := normalizeProviderHeaders(req.Headers)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	applyProviderHeaders(httpReq.Header, headers)
	resp, err := client.Do(httpReq)
	if err != nil {
		if egressErr := providerEgressFailure(err); egressErr != nil {
			return ProviderCatalogEntry{}, egressErr
		}
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_request_failed", "Failed to request upstream models")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		if descriptor.ProviderPolicy.ErrorProfile != "" {
			return ProviderCatalogEntry{}, checkProviderResponseForProviderPolicy(resp, Provider{
				Type: providerType, APIKey: apiKey, Headers: headers, SensitiveHeaders: req.SensitiveHeaders,
			}, descriptor.ProviderPolicy)
		}
		return ProviderCatalogEntry{}, providerModelsUpstreamError(resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&payload); err != nil {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_invalid_response", "Upstream models response is invalid")
	}
	models := customProviderModelsFromPayloadWithDefinitions(payload, providerModelCategoryDefinitionsFromAdapter(descriptor.ProviderPolicy.ModelCategories))
	if len(models) == 0 {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadGateway, "provider_models_empty", "Upstream did not return any models")
	}
	categories, categoryCounts := catalogCategorySummary(models)
	name := firstNonEmpty(strings.TrimSpace(req.Name), "自定义渠道商")
	return ProviderCatalogEntry{
		ID:             "custom",
		Name:           name,
		DisplayName:    name,
		Type:           firstNonEmpty(strings.TrimSpace(req.Type), strings.TrimSpace(descriptor.Type), defaultProviderCatalogProviderType()),
		BaseURL:        baseURL,
		Categories:     categories,
		CategoryCounts: categoryCounts,
		ModelsCount:    len(models),
		Source:         "custom-upstream",
		Models:         models,
	}, nil
}

func providerModelDiscoveryPolicy(descriptor AdapterDescriptor) AdapterModelDiscoveryPolicy {
	policy := descriptor.ProviderPolicy.ModelDiscovery
	policy.Path = strings.TrimSpace(policy.Path)
	if policy.Path == "" {
		policy.Path = "/models"
	}
	policy.Auth = strings.ToLower(strings.TrimSpace(policy.Auth))
	if policy.Auth == "" {
		policy.Auth = "bearer_header"
	}
	policy.APIKeyQueryParam = strings.TrimSpace(policy.APIKeyQueryParam)
	policy.Headers = normalizedStringMap(policy.Headers)
	return policy
}

func providerModelDiscoveryURL(baseURL string, path string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	if strings.HasSuffix(strings.ToLower(baseURL), "/v1") && strings.HasPrefix(strings.ToLower(path), "/v1/") {
		path = path[len("/v1"):]
	}
	return baseURL + path
}

func applyProviderModelDiscoveryAuth(httpReq *http.Request, endpoint *url.URL, req ProviderCreateRequest, descriptor AdapterDescriptor, discovery AdapterModelDiscoveryPolicy) error {
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return nil
	}
	switch discovery.Auth {
	case "bearer_header":
		httpReq.Header.Set("authorization", "Bearer "+apiKey)
	case "query_param":
		queryParam := firstNonEmpty(discovery.APIKeyQueryParam, "key")
		query := endpoint.Query()
		query.Set(queryParam, apiKey)
		endpoint.RawQuery = query.Encode()
		httpReq.URL = endpoint
	case "provider_auth_mode":
		mode, err := providerModelDiscoveryAuthMode(req, descriptor)
		if err != nil {
			return err
		}
		switch mode {
		case anthropicAuthTypeBearer:
			httpReq.Header.Set("authorization", "Bearer "+apiKey)
		case anthropicAuthTypeAPIKey:
			httpReq.Header.Set("x-api-key", apiKey)
		case "":
		default:
			return providerAuthModeInvalidError(descriptor.ProviderPolicy)
		}
	default:
		return NewHTTPError(http.StatusBadRequest, "provider_model_discovery_auth_invalid", "Provider model discovery authentication mode is not supported")
	}
	return nil
}

func providerModelDiscoveryAuthMode(req ProviderCreateRequest, descriptor AdapterDescriptor) (string, error) {
	provider := Provider{Type: strings.TrimSpace(req.Type), APIKey: strings.TrimSpace(req.APIKey), Options: req.Options}
	if err := configureProviderAuthMode(&provider, requestedProviderAuthMode(req), descriptor.ProviderPolicy); err != nil {
		return "", err
	}
	if mode := providerConfiguredAuthMode(provider, descriptor.ProviderPolicy); mode != "" {
		return mode, nil
	}
	return preferredProviderAuthMode(descriptor.ProviderPolicy.AuthModes), nil
}

func preferredProviderAuthMode(modes []string) string {
	for _, mode := range modes {
		if strings.EqualFold(strings.TrimSpace(mode), anthropicAuthTypeAPIKey) {
			return anthropicAuthTypeAPIKey
		}
	}
	for _, mode := range modes {
		if mode = strings.ToLower(strings.TrimSpace(mode)); mode != "" {
			return mode
		}
	}
	return ""
}

func providerModelDiscoveryHeaders(options map[string]string, defaults map[string]string) map[string]string {
	headers := normalizedStringMap(defaults)
	if len(headers) == 0 {
		return nil
	}
	for name := range headers {
		if value := strings.TrimSpace(options[providerModelDiscoveryHeaderOption(name)]); value != "" {
			headers[name] = value
		}
	}
	return headers
}

func providerModelDiscoveryHeaderOption(header string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(header)), "-", "_")
}

func providerModelsUpstreamError(status int) *HTTPError {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return NewHTTPError(statusForProvider(status), "provider_models_authentication_failed", "Upstream rejected the Provider credentials")
	case http.StatusTooManyRequests:
		return NewHTTPError(statusForProvider(status), "provider_models_rate_limited", "Upstream model catalog request was rate limited")
	default:
		return NewHTTPError(statusForProvider(status), "provider_models_upstream_error", "Upstream model catalog request failed")
	}
}

func customProviderModelsFromPayload(payload map[string]any) []ProviderCatalogModel {
	return customProviderModelsFromPayloadWithDefinitions(payload, nil)
}

func customProviderModelsFromPayloadWithDefinitions(payload map[string]any, modelCategories []providerModelCategoryDefinition) []ProviderCatalogModel {
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
			CanonicalName:       canonicalModelNameWithDefinitions(id, displayName, modelCategories),
			Category:            inferModelCategoryWithDefinitions(id, displayName, modelCategories),
			Family:              inferModelFamilyWithDefinitions(id, modelCategories),
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

func normalizeProviderBaseURL(id string, raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	return normalizeOpenAICompatibleBaseURL(id, raw)
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

func catalogCategorySummary(models []ProviderCatalogModel) ([]string, map[string]int) {
	return catalogCategorySummaryWithDefinitions(models, nil)
}

func catalogCategorySummaryWithDefinitions(models []ProviderCatalogModel, modelCategories []providerModelCategoryDefinition) ([]string, map[string]int) {
	counts := map[string]int{}
	for _, model := range models {
		category := standardModelCategoryWithDefinitions(firstNonEmpty(model.Category, inferModelCategoryWithDefinitions(model.ID, model.DisplayName, modelCategories)), modelCategories)
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

func catalogNumberFieldConfigured(raw map[string]any, key string) bool {
	if raw == nil {
		return false
	}
	switch raw[key].(type) {
	case float64, int, json.Number:
		return true
	default:
		return false
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
