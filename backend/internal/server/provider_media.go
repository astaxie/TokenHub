package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

const maxMediaResponseBytes = 128 << 20

type mediaResponse struct {
	Body        []byte
	ContentType string
	Status      int
}

type providerMedia interface {
	Media(context.Context, Provider, string, string, mediaRequest) (mediaResponse, Usage, error)
}

func (a OpenAICompatibleAdapter) Media(ctx context.Context, provider Provider, model, endpoint string, request mediaRequest) (mediaResponse, Usage, error) {
	switch endpoint {
	case "/audio/speech", "/audio/transcriptions", "/audio/translations", "/images/generations", "/images/edits", "/images/variations":
	default:
		return mediaResponse{}, Usage{}, NewHTTPError(400, "invalid_media_endpoint", "Unsupported media endpoint")
	}
	if err := ValidateProviderUpstreamBaseURL(provider.BaseURL); err != nil {
		return mediaResponse{}, Usage{}, err
	}
	data, contentType, err := request.encode(model)
	if err != nil {
		return mediaResponse{}, Usage{}, err
	}
	req, err := openAICompatibleRequest(ctx, provider, http.MethodPost, model, endpoint, data)
	if err != nil {
		return mediaResponse{}, Usage{}, err
	}
	req.Header.Set("Content-Type", contentType)
	client := a.Client
	if a.StreamClient != nil {
		client = a.StreamClient
	}
	response, err := sendUpstream(ssrfGuardedProviderClient(client), nil, a.StreamIdleTimeout, req, true)
	if err != nil {
		return mediaResponse{}, Usage{MeteringInvalid: true}, uncertainMediaSubmission(err)
	}
	if err := checkProviderResponseForProvider(response, provider); err != nil {
		if response.StatusCode >= 500 {
			err = uncertainMediaSubmission(err)
		}
		return mediaResponse{}, Usage{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMediaResponseBytes+1))
	if err != nil || len(body) > maxMediaResponseBytes {
		if ctx.Err() != nil {
			return mediaResponse{}, Usage{MeteringInvalid: true}, ctx.Err()
		}
		return mediaResponse{}, Usage{MeteringInvalid: true}, uncertainMediaSubmission(NewHTTPError(502, "invalid_media_response", "Unable to read the complete media response within the size limit"))
	}
	usage := Usage{ServedModel: model, UpstreamRequestID: response.Header.Get("x-request-id"), Transport: "http_media"}
	contentType = response.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		var payload map[string]any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			return mediaResponse{}, Usage{MeteringInvalid: true}, uncertainMediaSubmission(NewHTTPError(502, "invalid_media_response", "Provider returned invalid JSON"))
		}
		usage = usageFromMap(payload)
		usage.ServedModel, usage.UpstreamRequestID, usage.Transport = model, response.Header.Get("x-request-id"), "http_media"
	}
	return mediaResponse{Body: body, ContentType: contentType, Status: response.StatusCode}, usage, nil
}

// A timeout or broken success response can follow a billable generation. Do not
// submit it again to another provider when the first outcome is unknown.
func uncertainMediaSubmission(err error) error {
	return &ProviderInvocationError{Err: err, Disposition: ProviderErrorOutcomeUnknown}
}
