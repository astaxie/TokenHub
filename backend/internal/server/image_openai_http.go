package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

type openAIImageGenerationRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	N            int    `json:"n"`
	Quality      string `json:"quality,omitempty"`
	Size         string `json:"size,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

type openAIImageResponse struct {
	Data  []openAIImageData `json:"data"`
	Usage map[string]any    `json:"usage,omitempty"`
}

type openAIImageData struct {
	B64JSON       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

func (s *Server) executeOpenAIImage(ctx context.Context, route RouteSelection, job ImageJob) ([]byte, string, Usage, error) {
	prepared, err := s.prepareRouteForUpstream(ctx, route)
	if err != nil {
		return nil, "", Usage{}, err
	}
	adapter, ok := resolveTypedAdapter[ProviderImageGenerator](s.adapterRegistry, prepared.Provider.Type)
	if !ok {
		return nil, "", Usage{}, NewHTTPError(http.StatusServiceUnavailable, "image_provider_unavailable", "OpenAI image adapter is unavailable")
	}
	request, err := s.providerImageGenerationRequest(prepared, job)
	if err != nil {
		return nil, "", Usage{}, err
	}
	return adapter.GenerateImage(ctx, prepared.Provider, prepared.ProviderModel, request)
}

func (a OpenAICompatibleAdapter) GenerateImage(ctx context.Context, provider Provider, providerModel string, req ProviderImageGenerationRequest) ([]byte, string, Usage, error) {
	if a.Client != nil {
		client := *a.Client
		client.Timeout = 0
		a.Client = &client
	}
	var response openAIImageResponse
	var headers http.Header
	var err error
	if req.Action == "edit" {
		response, headers, err = executeOpenAIImageEdit(ctx, a, provider, providerModel, req)
	} else {
		response, headers, err = executeOpenAIImageGeneration(ctx, a, provider, providerModel, req)
	}
	if err != nil {
		return nil, "", Usage{}, err
	}
	if len(response.Data) == 0 || strings.TrimSpace(response.Data[0].B64JSON) == "" {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadGateway, "image_result_missing", "OpenAI image generation completed without an image result")
	}
	imageBytes, err := decodeGeneratedImage(response.Data[0].B64JSON)
	if err != nil {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadGateway, "image_result_invalid", err.Error())
	}
	usage := usageFromMap(map[string]any{"usage": response.Usage})
	usage.Transport = "http_json"
	usage.ServedModel = firstNonEmpty(strings.TrimSpace(providerModel), strings.TrimSpace(req.Model), openAIImageModelName)
	usage.UpstreamRequestID = strings.TrimSpace(headers.Get("x-request-id"))
	return imageBytes, response.Data[0].RevisedPrompt, usage, nil
}

func executeOpenAIImageGeneration(
	ctx context.Context,
	adapter OpenAICompatibleAdapter,
	provider Provider,
	providerModel string,
	req ProviderImageGenerationRequest,
) (openAIImageResponse, http.Header, error) {
	payload := openAIImageGenerationRequest{
		Model:        firstNonEmpty(strings.TrimSpace(providerModel), strings.TrimSpace(req.Model), openAIImageModelName),
		Prompt:       req.Prompt,
		N:            1,
		Quality:      normalizedImageOption(req.Quality, "auto"),
		Size:         normalizedImageOption(req.Size, "auto"),
		OutputFormat: "png",
	}
	resp, err := adapter.doRaw(ctx, provider, http.MethodPost, "/images/generations", payload, false)
	if err != nil {
		return openAIImageResponse{}, nil, err
	}
	defer resp.Body.Close()
	response, err := decodeOpenAIImageResponse(resp.Body)
	return response, resp.Header.Clone(), err
}

func executeOpenAIImageEdit(
	ctx context.Context,
	adapter OpenAICompatibleAdapter,
	provider Provider,
	providerModel string,
	req ProviderImageGenerationRequest,
) (openAIImageResponse, http.Header, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"model":         firstNonEmpty(strings.TrimSpace(providerModel), strings.TrimSpace(req.Model), openAIImageModelName),
		"prompt":        req.Prompt,
		"n":             "1",
		"quality":       normalizedImageOption(req.Quality, "auto"),
		"size":          normalizedImageOption(req.Size, "auto"),
		"output_format": "png",
	} {
		if err := writer.WriteField(key, value); err != nil {
			return openAIImageResponse{}, nil, err
		}
	}
	inputCount := 0
	fileIndex := 0
	for _, image := range req.Images {
		role := strings.TrimSpace(image.Role)
		if role != "input" && role != "mask" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(image.DataBase64)
		if err != nil {
			return openAIImageResponse{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_input_image", "Image edit input image is not valid base64")
		}
		field := "image[]"
		if role == "mask" {
			field = "mask"
		} else {
			inputCount++
		}
		fileIndex++
		part, err := writer.CreateFormFile(field, fmt.Sprintf("%s-%d.png", role, fileIndex))
		if err != nil {
			return openAIImageResponse{}, nil, err
		}
		if _, err := part.Write(raw); err != nil {
			return openAIImageResponse{}, nil, err
		}
	}
	if inputCount == 0 {
		return openAIImageResponse{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_input_image", "Image edit job has no input images")
	}
	if err := writer.Close(); err != nil {
		return openAIImageResponse{}, nil, err
	}
	resp, err := adapter.doMultipartRaw(ctx, provider, "/images/edits", writer.FormDataContentType(), &body)
	if err != nil {
		return openAIImageResponse{}, nil, err
	}
	defer resp.Body.Close()
	response, err := decodeOpenAIImageResponse(resp.Body)
	return response, resp.Header.Clone(), err
}

func decodeOpenAIImageResponse(reader io.Reader) (openAIImageResponse, error) {
	body, err := io.ReadAll(io.LimitReader(reader, codexImageResponseLimit+1))
	if err != nil {
		return openAIImageResponse{}, NewHTTPError(http.StatusBadGateway, "image_response_failed", err.Error())
	}
	if len(body) > codexImageResponseLimit {
		return openAIImageResponse{}, NewHTTPError(http.StatusBadGateway, "image_response_too_large", "OpenAI image response exceeded the configured limit")
	}
	var response openAIImageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return openAIImageResponse{}, NewHTTPError(http.StatusBadGateway, "image_response_invalid", "OpenAI image response was not valid JSON")
	}
	return response, nil
}

func (a OpenAICompatibleAdapter) doMultipartRaw(
	ctx context.Context,
	provider Provider,
	endpoint string,
	contentType string,
	body io.Reader,
) (*http.Response, error) {
	if provider.BaseURL == "" {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "provider_not_configured", "Provider base_url is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(provider.BaseURL, endpoint), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", contentType)
	if provider.APIKey != "" {
		req.Header.Set("authorization", "Bearer "+provider.APIKey)
	}
	applyOpenAICompatibleAccountHeaders(req, provider)
	applyProviderHeaders(req.Header, provider.Headers)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(resp, provider); err != nil {
		return nil, err
	}
	return resp, nil
}
