package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	codexImageCapabilityOption          = "image_generation_capability"
	codexImageCapabilityCheckedAtOption = "image_generation_capability_checked_at"
	codexImageCapabilitySupported       = "supported"
	codexImageCapabilityUnsupported     = "unsupported"
	codexImageUpstreamModel             = "gpt-image-2"
	codexImageResponseLimit             = (maxGeneratedImageBytes * 4 / 3) + (2 << 20)
)

type codexSubscriptionImageRequest struct {
	Model      string                   `json:"model"`
	Prompt     string                   `json:"prompt"`
	Images     []codexSubscriptionImage `json:"images,omitempty"`
	Background string                   `json:"background,omitempty"`
	Quality    string                   `json:"quality,omitempty"`
	Size       string                   `json:"size,omitempty"`
}

type codexSubscriptionImage struct {
	ImageURL string `json:"image_url"`
}

type codexSubscriptionImageResponse struct {
	Data  []codexSubscriptionImageData `json:"data"`
	Usage map[string]any               `json:"usage,omitempty"`
}

type codexSubscriptionImageData struct {
	B64JSON string `json:"b64_json"`
}

func (s *Server) executeCodexSubscriptionImage(ctx context.Context, route RouteSelection, job ImageJob) ([]byte, string, Usage, error) {
	resourceID := routeResourceID(route)
	request, err := s.codexSubscriptionImageRequest(job)
	if err != nil {
		return nil, "", Usage{}, err
	}
	response, headers, err := s.codexSubscription.Image(ctx, route.Provider, resourceID, request)
	if err != nil {
		if AsHTTPError(err).Code == "codex_image_forbidden" {
			return nil, "", Usage{}, s.codexImageForbiddenError(resourceID)
		}
		return nil, "", Usage{}, err
	}
	if len(response.Data) == 0 || strings.TrimSpace(response.Data[0].B64JSON) == "" {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadGateway, "image_result_missing", "Codex image generation completed without an image result")
	}
	imageBytes, err := decodeGeneratedImage(response.Data[0].B64JSON)
	if err != nil {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadGateway, "image_result_invalid", err.Error())
	}
	usage := usageFromMap(map[string]any{"usage": response.Usage})
	applyCodexResponseMetadata(&usage, headers)
	usage.Transport = "http_json"
	usage.ServedModel = firstNonEmpty(usage.ServedModel, codexImageUpstreamModel)
	s.recordCodexImageCapability(resourceID, codexImageCapabilitySupported)
	return imageBytes, "", usage, nil
}

func (s *Server) codexSubscriptionImageRequest(job ImageJob) (codexSubscriptionImageRequest, error) {
	request := codexSubscriptionImageRequest{
		Model:      codexImageUpstreamModel,
		Prompt:     job.Prompt,
		Background: "auto",
		Quality:    normalizedImageOption(job.Quality, "auto"),
		Size:       normalizedImageOption(job.Size, "auto"),
	}
	if job.Action != "edit" {
		return request, nil
	}
	for _, asset := range s.store.ListImageAssets(job.ID) {
		if asset.Role != "input" {
			continue
		}
		path, err := s.imageAssetPath(asset.RelativePath)
		if err != nil {
			return codexSubscriptionImageRequest{}, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return codexSubscriptionImageRequest{}, err
		}
		request.Images = append(request.Images, codexSubscriptionImage{
			ImageURL: "data:" + asset.ContentType + ";base64," + base64.StdEncoding.EncodeToString(raw),
		})
	}
	if len(request.Images) == 0 {
		return codexSubscriptionImageRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_input_image", "Image edit job has no input images")
	}
	return request, nil
}

func mergedStringMap(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	result := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range override {
		result[key] = value
	}
	return result
}

func (a CodexSubscriptionAdapter) Image(
	ctx context.Context,
	provider Provider,
	resourceID string,
	request codexSubscriptionImageRequest,
) (codexSubscriptionImageResponse, http.Header, error) {
	if a.RefreshCredentials == nil {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusServiceUnavailable, "provider_credentials_unavailable", "Codex Subscription credentials are unavailable")
	}
	credentials, err := a.RefreshCredentials(ctx, resourceID, false)
	if err != nil {
		return codexSubscriptionImageResponse{}, nil, err
	}
	response, headers, err := a.imageWithRetry(ctx, provider, credentials, request)
	if err == nil || providerErrorDisposition(err) != ProviderErrorAuthBroken {
		return response, headers, err
	}
	credentials, refreshErr := a.RefreshCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return codexSubscriptionImageResponse{}, nil, refreshErr
	}
	return a.imageWithRetry(ctx, provider, credentials, request)
}

func (a CodexSubscriptionAdapter) imageWithRetry(
	ctx context.Context,
	provider Provider,
	credentials ProviderResourceCredentials,
	request codexSubscriptionImageRequest,
) (codexSubscriptionImageResponse, http.Header, error) {
	maxRetries := a.MaxRequestRetries
	if maxRetries <= 0 {
		maxRetries = openAICodexMaxRequestRetries
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		response, headers, err := a.imageWithCredentials(ctx, provider, credentials, request)
		if err == nil {
			return response, headers, nil
		}
		lastErr = err
		if providerErrorDisposition(err) != ProviderErrorTransientSame ||
			AsHTTPError(err).Code == "codex_rate_limited" ||
			attempt == maxRetries {
			return codexSubscriptionImageResponse{}, nil, err
		}
		delay := providerErrorRetryAfter(err)
		if delay <= 0 {
			delay = time.Duration(100*(1<<attempt)) * time.Millisecond
		}
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return codexSubscriptionImageResponse{}, nil, ctx.Err()
		case <-timer.C:
		}
	}
	return codexSubscriptionImageResponse{}, nil, lastErr
}

func (a CodexSubscriptionAdapter) imageWithCredentials(
	ctx context.Context,
	provider Provider,
	credentials ProviderResourceCredentials,
	request codexSubscriptionImageRequest,
) (codexSubscriptionImageResponse, http.Header, error) {
	accessToken := strings.TrimSpace(credentials.AccessToken)
	accountID := strings.TrimSpace(credentials.AccountID)
	if accessToken == "" {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadRequest, "openai_account_token_missing", "OpenAI account access token is missing")
	}
	if accountID == "" {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadRequest, "openai_account_id_missing", "OpenAI ChatGPT account ID is missing")
	}
	if request.Model != codexImageUpstreamModel {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadRequest, "unsupported_image_model", "Codex subscription image requests must use gpt-image-2 upstream")
	}
	endpoint, err := codexImageEndpoint(provider, len(request.Images) > 0)
	if err != nil {
		return codexSubscriptionImageResponse{}, nil, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadGateway, "codex_image_request_failed", err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	applyCodexRequestHeaders(req.Header, nil)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return codexSubscriptionImageResponse{}, nil, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_image_request_failed", fmt.Sprintf("Codex image request failed: %v", err)),
			Disposition: ProviderErrorTransientSame,
		}
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, codexImageResponseLimit+1))
	if readErr != nil {
		return codexSubscriptionImageResponse{}, nil, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_image_response_failed", readErr.Error()),
			Disposition: ProviderErrorTransientSame,
		}
	}
	if len(body) > codexImageResponseLimit {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadGateway, "codex_image_response_too_large", "Codex image response exceeded the configured limit")
	}
	if resp.StatusCode == http.StatusForbidden {
		return codexSubscriptionImageResponse{}, nil, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusForbidden, "codex_image_forbidden", "This Codex subscription account is not allowed to use image generation"),
			Disposition: ProviderErrorModelUnsupported,
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return codexSubscriptionImageResponse{}, nil, classifyCodexHTTPError(resp.StatusCode, resp.Header, body)
	}
	var response codexSubscriptionImageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return codexSubscriptionImageResponse{}, nil, NewHTTPError(http.StatusBadGateway, "codex_image_response_invalid", "Codex image response was not valid JSON")
	}
	return response, resp.Header.Clone(), nil
}

func codexImageEndpoint(provider Provider, edit bool) (string, error) {
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		baseURL = openAICodexBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", NewHTTPError(http.StatusBadRequest, "codex_base_url_invalid", "Codex base URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && !strings.EqualFold(parsed.Hostname(), "localhost") &&
		parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
		return "", NewHTTPError(http.StatusBadRequest, "codex_base_url_invalid", "Codex base URL must use HTTPS")
	}
	if err := validateCodexEndpointHost(provider, parsed); err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/responses", "/images/generations", "/images/edits"} {
		path = strings.TrimSuffix(path, suffix)
	}
	if edit {
		parsed.Path = path + "/images/edits"
	} else {
		parsed.Path = path + "/images/generations"
	}
	return parsed.String(), nil
}

func (s *Server) codexImageForbiddenError(resourceID string) error {
	s.recordCodexImageCapability(resourceID, codexImageCapabilityUnsupported)
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusForbidden, "codex_image_forbidden", "This Codex subscription account is not allowed to use image generation"),
		Disposition: ProviderErrorModelUnsupported,
	}
}

func (s *Server) recordCodexImageCapability(resourceID string, capability string) {
	if strings.TrimSpace(resourceID) == "" {
		return
	}
	if _, err := s.store.UpdateProviderResourceOptions(resourceID, map[string]string{
		codexImageCapabilityOption:          capability,
		codexImageCapabilityCheckedAtOption: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		log.Printf("[tokenhub] failed to record Codex image capability resource=%s: %v", resourceID, err)
	}
}
