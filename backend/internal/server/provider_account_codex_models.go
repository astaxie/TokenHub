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

const codexProviderCatalogID = "openai-codex"

const (
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

func (a CodexSubscriptionAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, etag string) (ProviderCatalogEntry, int, error) {
	if a.SupportsResourceModels == nil || !a.SupportsResourceModels(provider.Type, resource.ResourceType) {
		return ProviderCatalogEntry{}, 0, NewHTTPError(http.StatusBadRequest, "provider_resource_models_unsupported", "Provider resource models are not available for this resource type")
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

func (s *Server) queryOpenAICodexModels(ctx context.Context, resourceID string) (ProviderCatalogEntry, error) {
	return s.queryProviderResourceModels(ctx, resourceID)
}

func (s *Server) persistCodexResourceModels(resourceID string, models []ProviderCatalogModel, fetchedAt time.Time) error {
	return s.persistProviderResourceModels(resourceID, models, fetchedAt)
}

func codexResourceCachedModels(resource *ProviderResource) ([]string, time.Time, bool) {
	return providerResourceCachedModels(resource)
}

func codexModelInList(modelName string, models []string) bool {
	return providerResourceModelInList(modelName, models)
}

func providerResourceModelOptionLegacyKeys(key string) []string {
	switch key {
	case providerResourceSupportedModelsOption:
		return []string{codexResourceSupportedModelsOption}
	case providerResourceModelsFetchedAtOption:
		return []string{codexResourceModelsFetchedAtOption}
	case providerResourceModelsETagOption:
		return []string{codexResourceModelsETagOption}
	case providerResourceModelCatalogOption:
		return []string{codexResourceModelCatalogOption}
	default:
		return nil
	}
}

func (s *Server) filterCodexRoutesByModel(_ context.Context, modelName string, routes []RouteSelection) ([]RouteSelection, error) {
	return s.filterProviderAccountRoutesByModel(modelName, routes)
}

func (s *Server) removeCodexResourceModel(resourceID string, modelName string) {
	s.removeProviderResourceModel(resourceID, modelName)
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
