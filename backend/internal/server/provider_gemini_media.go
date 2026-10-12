package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type providerGeminiMedia interface {
	GeminiMedia(context.Context, Provider, string, map[string]any, bool) (mediaResponse, Usage, error)
}

// Native media retains Gemini's generationConfig, tools, parts and signatures.
// Header authentication avoids putting provider credentials into transport errors.
func (a GeminiAdapter) GeminiMedia(ctx context.Context, provider Provider, model string, payload map[string]any, stream bool) (mediaResponse, Usage, error) {
	base := firstNonEmpty(provider.BaseURL, "https://generativelanguage.googleapis.com/v1beta")
	if err := ValidateProviderUpstreamBaseURL(base); err != nil {
		return mediaResponse{}, Usage{}, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return mediaResponse{}, Usage{}, err
	}
	action := ":generateContent"
	if stream {
		action = ":streamGenerateContent?alt=sse"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/models/"+url.PathEscape(model)+action, bytes.NewReader(data))
	if err != nil {
		return mediaResponse{}, Usage{}, err
	}
	applyProviderHeaders(req.Header, provider.Headers)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", provider.APIKey)
	client := a.Client
	if a.StreamClient != nil {
		client = a.StreamClient
	}
	response, err := sendUpstream(ssrfGuardedProviderClient(client), nil, a.StreamIdleTimeout, req, true)
	if err != nil {
		return mediaResponse{}, Usage{MeteringInvalid: true}, uncertainMediaSubmission(err)
	}
	if err = checkProviderResponseForProvider(response, provider); err != nil {
		if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout {
			return mediaResponse{}, Usage{MeteringInvalid: true}, uncertainMediaSubmission(err)
		}
		return mediaResponse{}, Usage{}, err
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxMediaResponseBytes+1))
	result := mediaResponse{Body: body, ContentType: response.Header.Get("Content-Type"), Status: response.StatusCode}
	usage, inspectErr := inspectGeminiMediaResponse(result, stream)
	usage.ServedModel, usage.UpstreamRequestID, usage.Transport = model, response.Header.Get("x-request-id"), "http_gemini_media"
	if readErr != nil || len(body) > maxMediaResponseBytes {
		usage.MeteringInvalid = true
		if ctx.Err() != nil {
			return mediaResponse{}, usage, uncertainMediaSubmission(ctx.Err())
		}
		return mediaResponse{}, usage, uncertainMediaSubmission(NewHTTPError(502, "invalid_media_response", "Unable to read the complete Gemini media response within the size limit"))
	}
	if inspectErr != nil {
		return mediaResponse{}, usage, uncertainMediaSubmission(inspectErr)
	}
	return result, usage, nil
}
