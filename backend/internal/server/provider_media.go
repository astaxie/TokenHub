package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
)

const maxMediaResponseBytes = 128 << 20

type mediaResponse struct {
	Body         []byte
	ContentType  string
	Status       int
	BindingRoute *RouteSelection
}

type providerMedia interface {
	Media(context.Context, Provider, string, string, mediaRequest) (mediaResponse, Usage, error)
}

func (a OpenAICompatibleAdapter) Media(ctx context.Context, provider Provider, model, endpoint string, request mediaRequest) (mediaResponse, Usage, error) {
	method := http.MethodPost
	switch endpoint {
	case "/audio/speech", "/audio/transcriptions", "/audio/translations", "/images/generations", "/images/edits", "/images/variations", "/videos/generations":
	case "/tasks":
		if !validMediaTaskID(request.UpstreamTaskID) {
			return mediaResponse{}, Usage{}, mediaTaskNotFound()
		}
		method = http.MethodGet
		endpoint += "/" + url.PathEscape(request.UpstreamTaskID)
		if request.TaskLanguage != "" {
			endpoint += "?" + url.Values{"language": {request.TaskLanguage}}.Encode()
		}
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
	if method == http.MethodGet {
		data = nil
	}
	req, err := openAICompatibleRequest(ctx, provider, method, model, endpoint, data)
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
		if mediaCapabilityForProtocol(endpoint) == AdapterCapabilityTranscription {
			err = sanitizeTranscriptionProviderError(err)
		}
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
		usage, responseErr = inspectMediaStream(body, provider, endpoint)
	} else if mediaResponseIsJSON(contentType) {
		usage, responseErr = inspectMediaJSON(body, endpoint)
	}
	usage.ServedModel, usage.UpstreamRequestID, usage.Transport = model, response.Header.Get("x-request-id"), "http_media"
	if err != nil || len(body) > maxMediaResponseBytes {
		usage.MeteringInvalid = true
		if ctx.Err() != nil {
			return mediaResponse{}, usage, uncertainMediaSubmission(ctx.Err())
		}
		if mediaResponseReadTimedOut(err) {
			return mediaResponse{}, usage, uncertainMediaSubmission(err)
		}
		return mediaResponse{}, usage, uncertainMediaSubmission(NewHTTPError(502, "invalid_media_response", "Unable to read the complete media response within the size limit"))
	}
	if responseErr != nil {
		if mediaCapabilityForProtocol(endpoint) == AdapterCapabilityTranscription {
			responseErr = sanitizeTranscriptionProviderError(responseErr)
		}
		return mediaResponse{}, usage, uncertainMediaSubmission(responseErr)
	}
	return mediaResponse{Body: body, ContentType: contentType, Status: response.StatusCode}, usage, nil
}

func mediaResponseReadTimedOut(err error) bool {
	var httpErr *HTTPError
	var networkErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &httpErr) && httpErr.Status == http.StatusGatewayTimeout || errors.As(err, &networkErr) && networkErr.Timeout()
}

// A transcription rejection may quote the uploaded audio or transcript. Keep
// routing metadata while excluding provider-controlled messages from all sinks.
func sanitizeTranscriptionProviderError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorClient}
	}
	safe := NewHTTPError(http.StatusBadGateway, "provider_error", "Audio transcription provider request failed")
	var original *HTTPError
	if errors.As(err, &original) {
		copySanitizedHTTPErrorMetadata(safe, original)
	} else if mediaResponseReadTimedOut(err) {
		safe = NewHTTPError(http.StatusGatewayTimeout, "provider_upstream_timeout", "Audio transcription provider request timed out")
	}
	var invocation *ProviderInvocationError
	if errors.As(err, &invocation) {
		return &ProviderInvocationError{Err: safe, Disposition: invocation.Disposition, RetryAfter: invocation.RetryAfter}
	}
	if original == nil && mediaResponseReadTimedOut(err) {
		return uncertainMediaSubmission(safe)
	}
	return safe
}

// A timeout or broken success response can follow a billable generation. Do not
// submit it again to another provider when the first outcome is unknown.
func uncertainMediaSubmission(err error) error {
	if errors.Is(err, context.Canceled) {
		return &ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorClient}
	}
	if egressErr := providerEgressFailure(err); egressErr != nil {
		return egressErr
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		var networkErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkErr) && networkErr.Timeout() {
			err = NewHTTPError(http.StatusGatewayTimeout, "provider_upstream_timeout", "Media provider request timed out")
		} else {
			err = NewHTTPError(http.StatusBadGateway, "provider_request_failed", "Unable to complete the media provider request")
		}
	}
	return &ProviderInvocationError{Err: err, Disposition: ProviderErrorOutcomeUnknown}
}
