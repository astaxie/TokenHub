package server

import (
	"context"
	"errors"
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
		if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout {
			return mediaResponse{}, Usage{MeteringInvalid: true}, uncertainMediaSubmission(err)
		}
		return mediaResponse{}, Usage{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMediaResponseBytes+1))
	contentType = response.Header.Get("Content-Type")
	usage := Usage{}
	var responseErr error
	if mediaResponseIsSSE(contentType) {
		usage, responseErr = inspectMediaStream(body, provider)
	} else if strings.Contains(strings.ToLower(contentType), "application/json") {
		usage, responseErr = inspectMediaJSON(body)
	}
	usage.ServedModel, usage.UpstreamRequestID, usage.Transport = model, response.Header.Get("x-request-id"), "http_media"
	if err != nil || len(body) > maxMediaResponseBytes {
		usage.MeteringInvalid = true
		if ctx.Err() != nil {
			return mediaResponse{}, usage, uncertainMediaSubmission(ctx.Err())
		}
		return mediaResponse{}, usage, uncertainMediaSubmission(NewHTTPError(502, "invalid_media_response", "Unable to read the complete media response within the size limit"))
	}
	if responseErr != nil {
		return mediaResponse{}, usage, uncertainMediaSubmission(responseErr)
	}
	return mediaResponse{Body: body, ContentType: contentType, Status: response.StatusCode}, usage, nil
}

// A timeout or broken success response can follow a billable generation. Do not
// submit it again to another provider when the first outcome is unknown.
func uncertainMediaSubmission(err error) error {
	if errors.Is(err, context.Canceled) {
		return &ProviderInvocationError{Err: err, Disposition: ProviderErrorClient}
	}
	if egressErr := providerEgressFailure(err); egressErr != nil {
		return egressErr
	}
	return &ProviderInvocationError{Err: err, Disposition: ProviderErrorOutcomeUnknown}
}
