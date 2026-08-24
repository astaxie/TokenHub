package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

const (
	kronkDefaultBaseURL = "http://127.0.0.1:11435/v1"
	kronkDocURL         = "https://github.com/ardanlabs/kronk"
)

// KronkAdapter intentionally embeds the OpenAI-compatible adapter. Kronk's
// inference wire format is OpenAI-shaped, while discovery and health are the
// only provider-specific operations.
type KronkAdapter struct {
	OpenAICompatibleAdapter
}

func (a KronkAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	provider.Type = ProviderKronk
	response, usage, err := a.OpenAICompatibleAdapter.Chat(ctx, provider, providerModel, req)
	return response, usage, normalizeKronkTransportError(err)
}

func (a KronkAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, writer io.Writer) (Usage, error) {
	provider.Type = ProviderKronk
	usage, err := a.OpenAICompatibleAdapter.ChatStream(ctx, provider, providerModel, req, writer)
	return usage, normalizeKronkTransportError(err)
}

func (a KronkAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	provider.Type = ProviderKronk
	response, usage, err := a.OpenAICompatibleAdapter.Responses(ctx, provider, providerModel, req)
	return response, usage, normalizeKronkTransportError(err)
}

func (a KronkAdapter) OpenResponses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest, incoming http.Header) (*http.Response, error) {
	provider.Type = ProviderKronk
	response, err := a.OpenAICompatibleAdapter.OpenResponses(ctx, provider, providerModel, req, incoming)
	return response, normalizeKronkTransportError(err)
}

func (a KronkAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	provider.Type = ProviderKronk
	response, usage, err := a.OpenAICompatibleAdapter.Embeddings(ctx, provider, providerModel, req)
	return response, usage, normalizeKronkTransportError(err)
}

func normalizeKronkTransportError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return kronkTransportInvocationError(http.StatusGatewayTimeout, "provider_upstream_timeout", "Kronk inference timed out")
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return kronkTransportInvocationError(http.StatusGatewayTimeout, "provider_upstream_timeout", "Kronk inference timed out")
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return kronkTransportInvocationError(http.StatusBadGateway, "provider_stream_interrupted", "Kronk closed the response stream unexpectedly")
	}
	if errors.As(err, &networkErr) {
		return kronkTransportInvocationError(http.StatusBadGateway, "provider_upstream_unreachable", "Kronk service is unreachable")
	}
	return err
}

func kronkTransportInvocationError(status int, code string, message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(status, code, message),
		Disposition: ProviderErrorTransientSame,
	}
}

type KronkHealthResult struct {
	Live        bool `json:"live"`
	Ready       bool `json:"ready"`
	ModelReady  bool `json:"model_ready"`
	ModelsCount int  `json:"models_count"`
}

func (a KronkAdapter) Health(ctx context.Context, provider Provider) (KronkHealthResult, error) {
	provider.BaseURL = firstNonEmpty(strings.TrimSpace(provider.BaseURL), kronkDefaultBaseURL)
	result := KronkHealthResult{}
	if err := a.getHealthEndpoint(ctx, provider, "/liveness"); err != nil {
		return result, kronkHealthError("kronk_unreachable", "Kronk service is unreachable", err)
	}
	result.Live = true
	if err := a.getHealthEndpoint(ctx, provider, "/readiness"); err != nil {
		return result, kronkHealthError("kronk_not_ready", "Kronk service is not ready", err)
	}
	result.Ready = true
	catalog, err := KronkProviderCatalogFromUpstream(ctx, a.Client, ProviderCreateRequest{
		Name: provider.Name, Type: ProviderKronk, BaseURL: provider.BaseURL, APIKey: provider.APIKey,
		Headers: provider.Headers, SensitiveHeaders: provider.SensitiveHeaders, Options: provider.Options,
	})
	if err != nil {
		return result, err
	}
	result.ModelsCount = catalog.ModelsCount
	result.ModelReady = catalog.ModelsCount > 0
	if !result.ModelReady {
		err := NewHTTPError(http.StatusServiceUnavailable, "kronk_models_unavailable", "Kronk is ready but has no available local models")
		err.Details = result
		return result, err
	}
	return result, nil
}

func (a KronkAdapter) getHealthEndpoint(ctx context.Context, provider Provider, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(provider.BaseURL, endpoint), nil)
	if err != nil {
		return err
	}
	if provider.APIKey != "" {
		request.Header.Set("authorization", "Bearer "+provider.APIKey)
	}
	applyProviderHeaders(request.Header, provider.Headers)
	response, err := sendUpstream(a.Client, nil, 0, request, false)
	if err != nil {
		return err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return checkProviderResponseForProvider(response, provider)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return nil
}

func kronkHealthError(code string, message string, err error) error {
	if httpErr := AsHTTPError(err); httpErr != nil {
		switch httpErr.Code {
		case "provider_auth_error", "provider_rate_limited", "provider_upstream_timeout":
			return err
		}
	}
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadGateway, code, message),
		Disposition: ProviderErrorTransientSame,
	}
}

func KronkProviderCatalogFromUpstream(ctx context.Context, client *http.Client, req ProviderCreateRequest) (ProviderCatalogEntry, error) {
	req.Type = ProviderKronk
	if strings.TrimSpace(req.BaseURL) == "" {
		req.BaseURL = kronkDefaultBaseURL
	}
	entry, err := CustomProviderCatalogFromUpstream(ctx, client, req)
	if err != nil {
		if httpErr := AsHTTPError(err); httpErr.Code != "provider_models_empty" {
			return ProviderCatalogEntry{}, err
		}
		entry = kronkCatalogEntry()
		entry.BaseURL = strings.TrimRight(req.BaseURL, "/")
		entry.Source = "kronk-upstream"
	}
	entry.ID = ProviderKronk
	entry.Name = "Kronk"
	entry.DisplayName = "Kronk"
	entry.Type = ProviderKronk
	entry.BaseURL = strings.TrimRight(req.BaseURL, "/")
	entry.DocURL = kronkDocURL
	entry.Source = "kronk-upstream"
	for index := range entry.Models {
		model := &entry.Models[index]
		if model.Metadata == nil {
			model.Metadata = map[string]string{}
		}
		model.Metadata["source"] = "kronk-discovery"
		model.Metadata["kronk_available"] = "true"
	}
	return entry, nil
}

func (s *Server) discoverKronkCatalog(ctx context.Context, req ProviderCreateRequest) (ProviderCatalogEntry, error) {
	providerID := firstNonEmpty(strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ID))
	if providerID != "" {
		provider, ok := s.store.GetProvider(providerID)
		if !ok {
			return ProviderCatalogEntry{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
		}
		if provider.Type != ProviderKronk {
			return ProviderCatalogEntry{}, NewHTTPError(http.StatusBadRequest, "provider_type_invalid", "Provider is not a Kronk provider")
		}
		storedBaseURL := normalizeProviderBaseURL(ProviderKronk, provider.BaseURL)
		requestedBaseURL := normalizeProviderBaseURL(ProviderKronk, req.BaseURL)
		if strings.TrimSpace(req.BaseURL) != "" && requestedBaseURL != storedBaseURL {
			return ProviderCatalogEntry{}, NewHTTPError(
				http.StatusBadRequest,
				"provider_base_url_override_forbidden",
				"Save the Kronk provider Base URL before discovering models from a different destination",
			)
		}
		if req.Name == "" {
			req.Name = provider.Name
		}
		req.BaseURL = storedBaseURL
		if req.APIKey == "" {
			req.APIKey = provider.APIKey
		}
		if req.Headers == nil {
			req.Headers = provider.Headers
			req.SensitiveHeaders = provider.SensitiveHeaders
		}
		req.Options = mergedStringMap(provider.Options, req.Options)
	}
	entry, err := KronkProviderCatalogFromUpstream(ctx, s.upstreamClient, req)
	if err != nil {
		return ProviderCatalogEntry{}, err
	}
	if providerID != "" {
		s.reconcileKronkProviderModels(providerID, entry.Models)
	}
	return entry, nil
}

// reconcileKronkProviderModels runs only after a successful discovery. Missing
// models are retained and marked unavailable; models that later reappear are
// restored only when Kronk discovery was what disabled them.
func (s *Server) reconcileKronkProviderModels(providerID string, discovered []ProviderCatalogModel) {
	available := make(map[string]bool, len(discovered))
	for _, model := range discovered {
		available[model.ID] = true
	}
	for _, model := range s.store.ListProviderModels() {
		if model.ProviderID != providerID || model.Source != "kronk-discovery" {
			continue
		}
		metadata := cloneStringMap(model.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		if available[model.UpstreamModel] {
			if metadata["kronk_available"] != "false" {
				continue
			}
			metadata["kronk_available"] = "true"
			model.Metadata = metadata
			model.Status = StatusActive
			_, _ = s.store.UpdateProviderModel(model.ID, model)
			continue
		}
		metadata["kronk_available"] = "false"
		model.Metadata = metadata
		model.Status = StatusDisabled
		_, _ = s.store.UpdateProviderModel(model.ID, model)
	}
}

func kronkCatalogEntry() ProviderCatalogEntry {
	return ProviderCatalogEntry{
		ID: ProviderKronk, Name: "Kronk", DisplayName: "Kronk", Type: ProviderKronk,
		BaseURL: kronkDefaultBaseURL, DocURL: kronkDocURL,
		Categories: []string{"custom"}, CategoryCounts: map[string]int{"custom": 0},
		ModelsCount: 0, Source: "builtin",
	}
}
