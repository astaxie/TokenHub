package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	providerTypeSafe = "typesafe"
	typeSafeBaseURL  = "https://api.typesafe.ai/v1"
	typeSafeDocURL   = "https://docs.typesafe.ai/models"
)

type TypeSafeAdapter struct {
	Client *http.Client
}

func (a TypeSafeAdapter) SystemOne(ctx context.Context, provider Provider, providerModel string, req SystemOneRequest) (SystemOneResponse, Usage, error) {
	req.Model = providerModel
	if err := req.validate(); err != nil {
		return SystemOneResponse{}, Usage{}, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return SystemOneResponse{}, Usage{}, err
	}
	response, err := a.request(ctx, provider, http.MethodPost, "/systemone", body)
	if err != nil {
		return SystemOneResponse{}, Usage{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		return SystemOneResponse{}, Usage{MeteringInvalid: true}, invalidSystemOneResponse()
	}
	result, decodeErr := decodeSystemOneResponse(data)
	usage := result.meteredUsage()
	usage.UpstreamRequestID = firstNonEmpty(response.Header.Get("x-typesafe-request-id"), response.Header.Get("x-request-id"))
	usage.ResponseHeaders = response.Header.Clone()
	if decodeErr != nil {
		return result, usage, decodeErr
	}
	return result, usage, result.validate(req)
}

func (a TypeSafeAdapter) request(ctx context.Context, provider Provider, method, path string, body []byte) (*http.Response, error) {
	if strings.TrimSpace(provider.APIKey) == "" {
		return nil, newProviderMisconfigured("TypeSafe API key is required")
	}
	baseURL := firstNonEmpty(strings.TrimSpace(provider.BaseURL), typeSafeBaseURL)
	if err := ValidateProviderUpstreamBaseURL(baseURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, joinURL(baseURL, path), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	applyProviderHeaders(req.Header, provider.Headers)
	req.Header.Set("authorization", "Bearer "+provider.APIKey)
	req.Header.Set("content-type", "application/json")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := sendUpstream(ssrfGuardedProviderClient(client), nil, 0, req, false)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(response, provider); err != nil {
		return nil, err
	}
	return response, nil
}

func (a TypeSafeAdapter) DiscoverModels(ctx context.Context, req ProviderCreateRequest) (ProviderCatalogEntry, error) {
	provider := Provider{Type: providerTypeSafe, BaseURL: req.BaseURL, APIKey: req.APIKey, Headers: req.Headers, SensitiveHeaders: req.SensitiveHeaders}
	if err := validateProviderHeaderConfig(&provider); err != nil {
		return ProviderCatalogEntry{}, err
	}
	response, err := a.request(ctx, provider, http.MethodGet, "/models", nil)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	defer response.Body.Close()
	var payload struct {
		Models []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ReleaseDate string `json:"release_date"`
		} `json:"models"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &payload) != nil || len(payload.Models) == 0 {
		return ProviderCatalogEntry{}, NewHTTPError(502, "provider_models_invalid_response", "TypeSafe returned an invalid model list")
	}
	entry := ProviderCatalogEntry{ID: providerTypeSafe, Name: "TypeSafe", DisplayName: "TypeSafe", Type: providerTypeSafe, BaseURL: firstNonEmpty(req.BaseURL, typeSafeBaseURL), DocURL: typeSafeDocURL, Source: "typesafe-upstream", Categories: []string{providerTypeSafe}, CategoryCounts: map[string]int{}}
	seen := map[string]bool{}
	for _, model := range payload.Models {
		if strings.TrimSpace(model.Name) == "" || seen[model.Name] {
			continue
		}
		seen[model.Name] = true
		entry.Models = append(entry.Models, ProviderCatalogModel{
			ID: model.Name, Name: model.Name, DisplayName: model.Name, CanonicalName: model.Name,
			Category: providerTypeSafe, Family: "jev", Type: "decision",
			InputModalities: []string{"text"}, OutputModalities: []string{"text"},
			Capabilities: []string{"systemone"}, SupportedParameters: []string{"state", "questions"},
			Metadata: map[string]string{"source": "typesafe-upstream", "description": model.Description, "release_date": model.ReleaseDate, "endpoints": "systemone"},
		})
	}
	entry.ModelsCount = len(entry.Models)
	entry.CategoryCounts[providerTypeSafe] = entry.ModelsCount
	if entry.ModelsCount == 0 {
		return ProviderCatalogEntry{}, NewHTTPError(502, "provider_models_empty", "TypeSafe did not return any models")
	}
	return entry, nil
}

func (a TypeSafeAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, _ string) (ProviderCatalogEntry, int, error) {
	// Normalize sensitive names before merging, which otherwise drops unmatched flags.
	if err := validateProviderHeaderConfig(&provider); err != nil {
		return ProviderCatalogEntry{}, http.StatusBadRequest, err
	}
	resourceHeaders := Provider{Type: provider.Type, Headers: resource.Headers, SensitiveHeaders: resource.SensitiveHeaders}
	if err := validateProviderHeaderConfig(&resourceHeaders); err != nil {
		return ProviderCatalogEntry{}, http.StatusBadRequest, err
	}
	if err := validateMergedProviderHeaderLimits(provider.Headers, resourceHeaders.Headers); err != nil {
		return ProviderCatalogEntry{}, http.StatusBadRequest, err
	}
	resource.Headers, resource.SensitiveHeaders = resourceHeaders.Headers, resourceHeaders.SensitiveHeaders
	provider = effectiveProviderResourceConfig(provider, &resource)
	entry, err := a.DiscoverModels(ctx, ProviderCreateRequest{Type: providerTypeSafe, BaseURL: provider.BaseURL, APIKey: provider.APIKey, Headers: provider.Headers, SensitiveHeaders: provider.SensitiveHeaders})
	return entry, http.StatusOK, err
}

func (a TypeSafeAdapter) ProbeProvider(ctx context.Context, provider Provider) (any, error) {
	entry, _, err := a.ResourceModels(ctx, provider, ProviderResource{}, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"models_count": entry.ModelsCount}, nil
}

func (a TypeSafeAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{Model: "jev-latest", Prompt: "models"}
}

func (a TypeSafeAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, req ProviderProbeRequest) (ProviderProbeResult, error) {
	start := time.Now()
	entry, _, err := a.ResourceModels(ctx, provider, resource, "")
	if err != nil {
		return ProviderProbeResult{}, err
	}
	return ProviderProbeResult{ResourceID: resource.ID, Model: req.Model, LatencyMS: time.Since(start).Milliseconds(), OutputText: "TypeSafe credentials are valid and models are available.", Response: map[string]any{"models_count": entry.ModelsCount}}, nil
}
