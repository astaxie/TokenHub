package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	codexProviderCatalogID = "openai-codex"

	providerResourceSupportedModelsOption = "provider_resource_supported_models"
	providerResourceModelsFetchedAtOption = "provider_resource_models_fetched_at"
	providerResourceModelsETagOption      = "provider_resource_models_etag"
	providerResourceModelCatalogOption    = "provider_resource_model_catalog"

	codexResourceSupportedModelsOption = "codex_supported_models"
	codexResourceModelsFetchedAtOption = "codex_models_fetched_at"
	codexResourceModelsETagOption      = "codex_models_etag"
	codexResourceModelCatalogOption    = "codex_model_catalog"
)

type codexRemoteModelsResponse struct {
	Models []codexRemoteModel `json:"models"`
}

type codexRemoteModel struct {
	Slug                     string                      `json:"slug"`
	DisplayName              string                      `json:"display_name"`
	Description              string                      `json:"description"`
	DefaultReasoningLevel    string                      `json:"default_reasoning_level"`
	SupportedReasoningLevels []codexRemoteReasoningLevel `json:"supported_reasoning_levels"`
	Visibility               string                      `json:"visibility"`
	SupportedInAPI           *bool                       `json:"supported_in_api"`
	Priority                 int                         `json:"priority"`
	AdditionalSpeedTiers     []string                    `json:"additional_speed_tiers"`
	ServiceTiers             []codexRemoteServiceTier    `json:"service_tiers"`
	DefaultServiceTier       string                      `json:"default_service_tier"`
	MinimalClientVersion     json.RawMessage             `json:"minimal_client_version"`
	ShellType                string                      `json:"shell_type"`
	SupportVerbosity         bool                        `json:"support_verbosity"`
	DefaultVerbosity         string                      `json:"default_verbosity"`
	ApplyPatchToolType       string                      `json:"apply_patch_tool_type"`
	SupportsParallelTools    bool                        `json:"supports_parallel_tool_calls"`
	SupportsReasoningSummary bool                        `json:"supports_reasoning_summary_parameter"`
	SupportsSearchTool       bool                        `json:"supports_search_tool"`
	UseResponsesLite         bool                        `json:"use_responses_lite"`
	MaxContextWindow         int64                       `json:"max_context_window"`
	AutoCompactTokenLimit    int64                       `json:"auto_compact_token_limit"`
	EffectiveContextPercent  int64                       `json:"effective_context_window_percent"`
	TruncationPolicy         json.RawMessage             `json:"truncation_policy"`
	Upgrade                  *codexRemoteModelUpgrade    `json:"upgrade"`
	ContextWindow            int64                       `json:"context_window"`
	InputModalities          []string                    `json:"input_modalities"`
}

type codexRemoteReasoningLevel struct {
	Effort string `json:"effort"`
}

type codexRemoteServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type codexRemoteModelUpgrade struct {
	Model             string `json:"model"`
	MigrationMarkdown string `json:"migration_markdown"`
}

func (a CodexSubscriptionAdapter) Models(ctx context.Context, resourceID string) (ProviderCatalogEntry, error) {
	if a.RefreshCredentials == nil {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusServiceUnavailable, "provider_credentials_unavailable", "Codex Subscription credentials are unavailable")
	}
	creds, err := a.RefreshCredentials(ctx, resourceID, false)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	catalog, status, err := a.modelsWithCredentials(ctx, creds, "")
	if status != http.StatusUnauthorized {
		return catalog, err
	}
	creds, refreshErr := a.RefreshCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return ProviderCatalogEntry{}, refreshErr
	}
	catalog, _, err = a.modelsWithCredentials(ctx, creds, "")
	return catalog, err
}

func (a CodexSubscriptionAdapter) ModelsWithCredentials(ctx context.Context, creds ProviderResourceCredentials) (ProviderCatalogEntry, error) {
	catalog, _, err := a.modelsWithCredentials(ctx, creds, "")
	return catalog, err
}

func (a CodexSubscriptionAdapter) ModelsWithETag(ctx context.Context, resourceID string, etag string) (ProviderCatalogEntry, int, error) {
	if a.RefreshCredentials == nil {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusServiceUnavailable, "provider_credentials_unavailable", "Codex Subscription credentials are unavailable")
	}
	credentials, err := a.RefreshCredentials(ctx, resourceID, false)
	if err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	catalog, status, err := a.modelsWithCredentials(ctx, credentials, etag)
	if status != http.StatusUnauthorized {
		return catalog, status, err
	}
	credentials, refreshErr := a.RefreshCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return ProviderCatalogEntry{}, status, refreshErr
	}
	return a.modelsWithCredentials(ctx, credentials, etag)
}

func (a CodexSubscriptionAdapter) ResourceModels(ctx context.Context, _ Provider, resource ProviderResource, etag string) (ProviderCatalogEntry, int, error) {
	if !isOpenAIAccountResource(resource.ResourceType) {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusBadRequest, "provider_resource_models_unsupported", "Codex models are only available for OpenAI subscription resources")
	}
	return a.ModelsWithETag(ctx, resource.ID, etag)
}

func (a CodexSubscriptionAdapter) modelsWithCredentials(ctx context.Context, creds ProviderResourceCredentials, etag string) (ProviderCatalogEntry, int, error) {
	accessToken := strings.TrimSpace(creds.AccessToken)
	accountID := strings.TrimSpace(creds.AccountID)
	if accessToken == "" {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusBadRequest, "openai_account_token_missing", "OpenAI account access token is missing")
	}
	if accountID == "" {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusBadRequest, "openai_account_id_missing", "OpenAI ChatGPT account ID is missing")
	}
	modelsURL := strings.TrimSpace(a.ModelsURL)
	if modelsURL == "" {
		modelsURL = openAICodexModelsURL
	}
	endpoint, err := url.Parse(modelsURL)
	if err != nil {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusInternalServerError, "codex_models_url_invalid", "Codex models URL is invalid")
	}
	query := endpoint.Query()
	query.Set("client_version", openAICodexVersion)
	endpoint.RawQuery = query.Encode()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusBadGateway, "codex_models_request_failed", "Failed to create Codex models request")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("User-Agent", openAICodexUserAgent)
	req.Header.Set("Version", openAICodexVersion)
	if etag = strings.TrimSpace(etag); etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if egressErr := providerEgressFailure(err); egressErr != nil {
			return ProviderCatalogEntry{}, 0, egressErr
		}
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusBadGateway, "codex_models_request_failed", fmt.Sprintf("Codex models request failed: %v", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return ProviderCatalogEntry{ETag: etag, Source: "openai-codex-cache"}, resp.StatusCode, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return ProviderCatalogEntry{}, resp.StatusCode, NewHTTPError(resp.StatusCode, "codex_models_upstream_error", message)
	}
	modelsETag := strings.TrimSpace(resp.Header.Get("ETag"))
	var payload codexRemoteModelsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return ProviderCatalogEntry{}, resp.StatusCode, NewHTTPError(http.StatusBadGateway, "codex_models_invalid_response", "Codex models response is invalid")
	}
	models := make([]ProviderCatalogModel, 0, len(payload.Models))
	for _, remote := range payload.Models {
		if strings.TrimSpace(remote.Slug) == "" || (remote.Visibility != "" && !strings.EqualFold(remote.Visibility, "list")) {
			continue
		}
		if remote.SupportedInAPI != nil && !*remote.SupportedInAPI {
			continue
		}
		reasoningLevels := make([]string, 0, len(remote.SupportedReasoningLevels))
		for _, level := range remote.SupportedReasoningLevels {
			if effort := strings.TrimSpace(level.Effort); effort != "" {
				reasoningLevels = append(reasoningLevels, effort)
			}
		}
		supportedParameters := []string{"reasoning_effort"}
		if stringInList("fast", remote.AdditionalSpeedTiers) || len(remote.ServiceTiers) > 0 {
			supportedParameters = append(supportedParameters, "service_tier")
		}
		serviceTierIDs := make([]string, 0, len(remote.ServiceTiers))
		for _, tier := range remote.ServiceTiers {
			if tier.ID != "" {
				serviceTierIDs = append(serviceTierIDs, tier.ID)
			}
		}
		truncationPolicy := strings.TrimSpace(string(remote.TruncationPolicy))
		upgradeModel := ""
		if remote.Upgrade != nil {
			upgradeModel = remote.Upgrade.Model
		}
		supportedInAPI := true
		if remote.SupportedInAPI != nil {
			supportedInAPI = *remote.SupportedInAPI
		}
		models = append(models, ProviderCatalogModel{
			ID:                  remote.Slug,
			Name:                remote.Slug,
			DisplayName:         firstNonEmpty(remote.DisplayName, remote.Slug),
			CanonicalName:       remote.Slug,
			Category:            "codex",
			Family:              "codex",
			Type:                "chat",
			ContextWindow:       remote.ContextWindow,
			InputModalities:     append([]string(nil), remote.InputModalities...),
			OutputModalities:    []string{"text"},
			Capabilities:        []string{"chat", "reasoning", "streaming", "tools"},
			SupportedParameters: supportedParameters,
			LastUpdated:         time.Now().UTC().Format(time.RFC3339),
			Metadata: map[string]string{
				"source":                     "openai-codex-live",
				"description":                remote.Description,
				"visibility":                 remote.Visibility,
				"supported_in_api":           strconv.FormatBool(supportedInAPI),
				"priority":                   strconv.Itoa(remote.Priority),
				"display_name":               firstNonEmpty(remote.DisplayName, remote.Slug),
				"default_reasoning_level":    remote.DefaultReasoningLevel,
				"supported_reasoning_levels": strings.Join(reasoningLevels, ","),
				"additional_speed_tiers":     strings.Join(remote.AdditionalSpeedTiers, ","),
				"service_tiers":              strings.Join(serviceTierIDs, ","),
				"default_service_tier":       remote.DefaultServiceTier,
				"minimal_client_version":     codexMinimalClientVersionString(remote.MinimalClientVersion),
				"shell_type":                 remote.ShellType,
				"support_verbosity":          strconv.FormatBool(remote.SupportVerbosity),
				"default_verbosity":          remote.DefaultVerbosity,
				"apply_patch_tool_type":      remote.ApplyPatchToolType,
				"supports_parallel_tools":    strconv.FormatBool(remote.SupportsParallelTools),
				"supports_reasoning_summary": strconv.FormatBool(remote.SupportsReasoningSummary),
				"supports_search_tool":       strconv.FormatBool(remote.SupportsSearchTool),
				"use_responses_lite":         strconv.FormatBool(remote.UseResponsesLite),
				"max_context_window":         strconv.FormatInt(remote.MaxContextWindow, 10),
				"auto_compact_token_limit":   strconv.FormatInt(remote.AutoCompactTokenLimit, 10),
				"effective_context_percent":  strconv.FormatInt(remote.EffectiveContextPercent, 10),
				"truncation_policy":          truncationPolicy,
				"upgrade_model":              upgradeModel,
				"models_etag":                modelsETag,
			},
		})
	}
	sort.SliceStable(models, func(i, j int) bool {
		left, _ := strconv.Atoi(models[i].Metadata["priority"])
		right, _ := strconv.Atoi(models[j].Metadata["priority"])
		return left < right
	})
	categories, counts := catalogCategorySummary(models)
	return ProviderCatalogEntry{
		ID:             codexProviderCatalogID,
		Name:           "OpenAI Codex",
		DisplayName:    "OpenAI Codex",
		Type:           ProviderOpenAICodex,
		BaseURL:        openAICodexBaseURL,
		DocURL:         "https://developers.openai.com/codex",
		Categories:     categories,
		CategoryCounts: counts,
		ModelsCount:    len(models),
		Source:         "openai-codex-live",
		ETag:           modelsETag,
		Models:         models,
	}, resp.StatusCode, nil
}

func (s *Server) queryOpenAICodexModels(ctx context.Context, resourceID string) (ProviderCatalogEntry, error) {
	return s.queryProviderResourceModels(ctx, resourceID)
}

func (s *Server) queryProviderResourceModels(ctx context.Context, resourceID string) (ProviderCatalogEntry, error) {
	entry, supported, err := s.queryProviderResourceModelsForCatalog(ctx, "", resourceID)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	if !supported {
		return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_resource_models_unsupported", "Provider resource models are not available for this provider")
	}
	return entry, nil
}

func (s *Server) queryProviderResourceModelsForCatalog(ctx context.Context, catalogProviderType string, resourceID string) (ProviderCatalogEntry, bool, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if catalogProviderType = strings.TrimSpace(catalogProviderType); catalogProviderType != "" && provider.Type != catalogProviderType {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusBadRequest, "provider_resource_catalog_mismatch", "Provider resource does not belong to this provider catalog")
	}
	descriptor, ok := s.adapterRegistry.Describe(provider.Type)
	if !ok || !adapterSupports(descriptor, AdapterCapabilityModels) {
		return ProviderCatalogEntry{}, false, nil
	}
	adapter, err := s.adapterRegistry.Resolve(provider.Type)
	if err != nil {
		return ProviderCatalogEntry{}, true, err
	}
	modeler, ok := adapter.(ProviderResourceModelCataloger)
	if !ok {
		return ProviderCatalogEntry{}, false, nil
	}
	etag := ""
	if resource.Options != nil {
		etag = providerResourceModelsETag(&resource)
	}
	catalog, status, err := modeler.ResourceModels(ctx, provider, resource, etag)
	if err == nil && status == http.StatusNotModified {
		pluginCatalog, _ := s.pluginProviderCatalogCapabilityEntryForType(provider.Type)
		if cached, ok := providerResourceCachedCatalog(provider, &resource, pluginCatalog); ok {
			return cached, true, nil
		}
		catalog, _, err = modeler.ResourceModels(ctx, provider, resource, "")
	}
	if err == nil {
		if persistErr := s.persistProviderResourceModels(resourceID, catalog.Models, time.Now().UTC()); persistErr != nil {
			return ProviderCatalogEntry{}, true, persistErr
		}
	}
	return catalog, true, err
}

func providerResourceCachedCatalog(provider Provider, resource *ProviderResource, catalogEntries ...ProviderCatalogEntry) (ProviderCatalogEntry, bool) {
	if resource == nil || resource.Options == nil {
		return ProviderCatalogEntry{}, false
	}
	var models []ProviderCatalogModel
	if json.Unmarshal([]byte(providerResourceModelCatalogJSON(resource)), &models) != nil || len(models) == 0 {
		return ProviderCatalogEntry{}, false
	}
	categories, counts := catalogCategorySummary(models)
	metadata := providerResourceCachedCatalogIdentityFor(provider, resource, catalogEntries...)
	return ProviderCatalogEntry{
		ID:             metadata.id,
		Name:           metadata.name,
		DisplayName:    metadata.displayName,
		Type:           metadata.providerType,
		BaseURL:        metadata.baseURL,
		DocURL:         metadata.docURL,
		Categories:     categories,
		CategoryCounts: counts,
		ModelsCount:    len(models),
		Source:         metadata.source,
		ETag:           providerResourceModelsETag(resource),
		Models:         models,
	}, true
}

type providerResourceCachedCatalogIdentity struct {
	id           string
	name         string
	displayName  string
	providerType string
	baseURL      string
	docURL       string
	source       string
}

func providerResourceCachedCatalogIdentityFor(provider Provider, resource *ProviderResource, catalogEntries ...ProviderCatalogEntry) providerResourceCachedCatalogIdentity {
	for _, catalog := range catalogEntries {
		if strings.TrimSpace(catalog.Type) == "" {
			continue
		}
		name := firstNonEmpty(strings.TrimSpace(catalog.Name), strings.TrimSpace(catalog.DisplayName), strings.TrimSpace(provider.Name), strings.TrimSpace(provider.Type))
		return providerResourceCachedCatalogIdentity{
			id:           firstNonEmpty(strings.TrimSpace(catalog.ID), provider.Type),
			name:         name,
			displayName:  firstNonEmpty(strings.TrimSpace(catalog.DisplayName), name),
			providerType: firstNonEmpty(strings.TrimSpace(catalog.Type), provider.Type),
			baseURL:      firstNonEmpty(strings.TrimSpace(catalog.BaseURL), provider.BaseURL),
			docURL:       strings.TrimSpace(catalog.DocURL),
			source:       cachedProviderCatalogSource(provider, resource),
		}
	}
	if provider.Type == ProviderOpenAICodex && resource != nil && isOpenAIAccountResource(resource.ResourceType) {
		return providerResourceCachedCatalogIdentity{
			id:           codexProviderCatalogID,
			name:         "OpenAI Codex",
			displayName:  "OpenAI Codex",
			providerType: ProviderOpenAICodex,
			baseURL:      openAICodexBaseURL,
			docURL:       "https://developers.openai.com/codex",
			source:       "openai-codex-cache",
		}
	}
	name := firstNonEmpty(strings.TrimSpace(provider.Name), strings.TrimSpace(provider.Type))
	return providerResourceCachedCatalogIdentity{
		id:           provider.Type,
		name:         name,
		displayName:  name,
		providerType: provider.Type,
		baseURL:      provider.BaseURL,
		source:       "provider-resource-cache",
	}
}

func cachedProviderCatalogSource(provider Provider, resource *ProviderResource) string {
	if provider.Type == ProviderOpenAICodex && resource != nil && isOpenAIAccountResource(resource.ResourceType) {
		return "openai-codex-cache"
	}
	return "provider-resource-cache"
}

func (s *Server) persistCodexResourceModels(resourceID string, models []ProviderCatalogModel, fetchedAt time.Time) error {
	return s.persistProviderResourceModels(resourceID, models, fetchedAt)
}

func (s *Server) persistProviderResourceModels(resourceID string, models []ProviderCatalogModel, fetchedAt time.Time) error {
	modelIDs := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		lookupName := normalizeModelLookupName(modelID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[lookupName]; ok {
			continue
		}
		seen[lookupName] = struct{}{}
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	encoded, err := json.Marshal(modelIDs)
	if err != nil {
		return err
	}
	catalogEncoded, err := json.Marshal(models)
	if err != nil {
		return err
	}
	_, err = s.store.UpdateProviderResourceOptions(resourceID, map[string]string{
		providerResourceSupportedModelsOption: string(encoded),
		providerResourceModelsFetchedAtOption: fetchedAt.UTC().Format(time.RFC3339Nano),
		providerResourceModelsETagOption:      firstNonEmptyModelETag(models),
		providerResourceModelCatalogOption:    string(catalogEncoded),
	})
	return err
}

func integerListString(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ".")
}

func codexMinimalClientVersionString(value json.RawMessage) string {
	var version string
	if json.Unmarshal(value, &version) == nil {
		return strings.TrimSpace(version)
	}
	var parts []int
	if json.Unmarshal(value, &parts) == nil {
		return integerListString(parts)
	}
	return ""
}

func firstNonEmptyModelETag(models []ProviderCatalogModel) string {
	for _, model := range models {
		if value := strings.TrimSpace(model.Metadata["models_etag"]); value != "" {
			return value
		}
	}
	return ""
}

func providerResourceCachedModels(resource *ProviderResource) ([]string, time.Time, bool) {
	if resource == nil || resource.Options == nil {
		return nil, time.Time{}, false
	}
	encoded := providerResourceSupportedModelsJSON(resource)
	if strings.TrimSpace(encoded) == "" {
		return nil, time.Time{}, false
	}
	var models []string
	if err := json.Unmarshal([]byte(encoded), &models); err != nil {
		return nil, time.Time{}, false
	}
	fetchedAt, err := time.Parse(time.RFC3339Nano, providerResourceModelsFetchedAt(resource))
	if err != nil {
		fetchedAt = time.Time{}
	}
	return models, fetchedAt, true
}

func providerResourceSupportedModelsJSON(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceSupportedModelsOption, codexResourceSupportedModelsOption)
}

func providerResourceModelsFetchedAt(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceModelsFetchedAtOption, codexResourceModelsFetchedAtOption)
}

func providerResourceModelsETag(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceModelsETagOption, codexResourceModelsETagOption)
}

func providerResourceModelCatalogJSON(resource *ProviderResource) string {
	return providerResourceModelOption(resource, providerResourceModelCatalogOption, codexResourceModelCatalogOption)
}

func providerResourceModelOption(resource *ProviderResource, key string, legacyKeys ...string) string {
	if resource == nil || resource.Options == nil {
		return ""
	}
	if value := strings.TrimSpace(resource.Options[key]); value != "" {
		return value
	}
	for _, legacyKey := range legacyKeys {
		if value := strings.TrimSpace(resource.Options[legacyKey]); value != "" {
			return value
		}
	}
	return ""
}

func codexResourceCachedModels(resource *ProviderResource) ([]string, time.Time, bool) {
	return providerResourceCachedModels(resource)
}

func providerResourceModelInList(modelName string, models []string) bool {
	lookupName := normalizeModelLookupName(modelName)
	for _, candidate := range models {
		if normalizeModelLookupName(candidate) == lookupName {
			return true
		}
	}
	return false
}

func codexModelInList(modelName string, models []string) bool {
	return providerResourceModelInList(modelName, models)
}

func (s *Server) filterCodexRoutesByModel(_ context.Context, modelName string, routes []RouteSelection) ([]RouteSelection, error) {
	return s.filterProviderAccountRoutesByModel(modelName, routes)
}

func (s *Server) filterProviderAccountRoutesByModel(modelName string, routes []RouteSelection) ([]RouteSelection, error) {
	filtered := make([]RouteSelection, 0, len(routes))
	filteredByAccountModels := false
	codexOnly := true

	for _, route := range routes {
		if route.Resource == nil || !s.store.IsProviderAccountResourceType(route.Provider.Type, route.Resource.ResourceType) {
			filtered = append(filtered, route)
			continue
		}
		cachedModels, _, cached := providerResourceCachedModels(route.Resource)
		if !cached {
			// Model discovery is a control-plane operation. If no snapshot exists
			// yet, keep the route and let the upstream return a precise result.
			filtered = append(filtered, route)
			continue
		}
		filteredByAccountModels = true
		if !isOpenAIAccountResource(route.Resource.ResourceType) {
			codexOnly = false
		}
		upstreamModel := firstNonEmpty(route.ProviderModel, modelName)
		if providerResourceModelInList(upstreamModel, cachedModels) {
			filtered = append(filtered, route)
		}
	}

	if len(filtered) > 0 {
		return filtered, nil
	}
	if filteredByAccountModels && !codexOnly {
		return nil, NewHTTPError(
			http.StatusServiceUnavailable,
			"provider_resource_model_unavailable",
			fmt.Sprintf("No connected provider account supports model %q", strings.TrimSpace(modelName)),
		)
	}
	return nil, NewHTTPError(
		http.StatusServiceUnavailable,
		"codex_model_unavailable",
		fmt.Sprintf("No connected Codex account supports model %q", strings.TrimSpace(modelName)),
	)
}

func (s *Server) removeCodexResourceModel(resourceID string, modelName string) {
	s.removeProviderResourceModel(resourceID, modelName)
}

func (s *Server) removeProviderResourceModel(resourceID string, modelName string) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return
	}
	models, _, cached := providerResourceCachedModels(&resource)
	if !cached {
		return
	}
	filtered := make([]string, 0, len(models))
	for _, candidate := range models {
		if normalizeModelLookupName(candidate) != normalizeModelLookupName(modelName) {
			filtered = append(filtered, candidate)
		}
	}
	catalogModels := make([]ProviderCatalogModel, 0, len(filtered))
	for _, modelID := range filtered {
		catalogModels = append(catalogModels, ProviderCatalogModel{ID: modelID})
	}
	_ = s.persistProviderResourceModels(resourceID, catalogModels, time.Now().UTC())
}

func (s *Server) codexProviderCatalogFromStandardModels(selected []string) ProviderCatalogEntry {
	entry := s.codexProviderCatalogMetadata()
	modelsByName := map[string]Model{}
	for _, model := range s.store.ListModels() {
		modelsByName[normalizeModelLookupName(model.Name)] = model
	}
	models := make([]ProviderCatalogModel, 0, len(selected))
	for _, modelID := range selected {
		model, ok := modelsByName[normalizeModelLookupName(modelID)]
		if !ok {
			continue
		}
		models = append(models, ProviderCatalogModel{
			ID:                  model.Name,
			Name:                model.Name,
			DisplayName:         firstNonEmpty(model.Metadata["display_name"], model.Name),
			CanonicalName:       model.Name,
			Category:            "codex",
			Family:              model.Family,
			Type:                model.Modality,
			ContextWindow:       model.ContextWindow,
			InputModalities:     append([]string(nil), model.InputModalities...),
			OutputModalities:    append([]string(nil), model.OutputModalities...),
			Capabilities:        append([]string(nil), model.Capabilities...),
			SupportedParameters: append([]string(nil), model.SupportedParameters...),
			Metadata:            cloneStringMap(model.Metadata),
		})
	}
	categories, counts := catalogCategorySummary(models)
	if len(models) > 0 {
		entry.Categories = categories
		entry.CategoryCounts = counts
	}
	entry.ModelsCount = len(models)
	entry.Models = models
	return entry
}

func (s *Server) codexProviderCatalogFromSubmittedModels(models []ProviderCatalogModel) ProviderCatalogEntry {
	candidates := append([]ProviderCatalogModel(nil), models...)
	for index := range candidates {
		candidates[index].Category = "codex"
	}
	return providerCatalogEntryWithSubmittedModels(s.codexProviderCatalogMetadata(), candidates, "codex")
}

func codexProviderCatalogFromModels(models []ProviderCatalogModel) ProviderCatalogEntry {
	candidates := append([]ProviderCatalogModel(nil), models...)
	for index := range candidates {
		candidates[index].Category = "codex"
	}
	entry := customProviderCatalogFromModels(candidates, "codex")
	entry.ID = codexProviderCatalogID
	entry.Name = "OpenAI Codex"
	entry.DisplayName = "OpenAI Codex"
	entry.Type = ProviderOpenAICodex
	entry.BaseURL = openAICodexBaseURL
	entry.DocURL = "https://developers.openai.com/codex"
	entry.Source = "openai-codex-live"
	return entry
}

func (s *Server) codexProviderCatalogMetadata() ProviderCatalogEntry {
	if s != nil {
		if entry, ok := s.pluginProviderCatalogCapabilityEntryForType(ProviderOpenAICodex); ok {
			return entry
		}
	}
	return codexProviderCatalogFromModels(nil)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
