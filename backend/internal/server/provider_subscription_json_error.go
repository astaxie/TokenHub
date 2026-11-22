package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// Keep usage with a body-read failure so protocol decoders cannot discard an
// upstream's reported work when they reject an embedded JSON error.
type subscriptionJSONFailure struct {
	err   error
	usage Usage
}

func (e *subscriptionJSONFailure) Error() string { return e.err.Error() }
func (e *subscriptionJSONFailure) Unwrap() error { return e.err }

func subscriptionJSONFailureUsage(err error) Usage {
	var failure *subscriptionJSONFailure
	if errors.As(err, &failure) {
		return failure.usage
	}
	return Usage{}
}

func subscriptionJSONContentType(headers http.Header) bool {
	mediaType, _, _ := mime.ParseMediaType(headers.Get("Content-Type"))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// Preserve the first content byte for the selected decoder. Leading stream
// separators have no payload and may be discarded without buffering them.
func subscriptionBodyStartsJSON(response *http.Response) (bool, error) {
	if !subscriptionJSONContentType(response.Header) {
		return false, nil
	}
	return peekSubscriptionJSON(response)
}

func peekSubscriptionJSON(response *http.Response) (bool, error) {
	reader := bufio.NewReader(response.Body)
	response.Body = struct {
		io.Reader
		io.Closer
	}{reader, response.Body}
	for {
		b, err := reader.Peek(1)
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			_, _ = reader.Discard(1)
		default:
			return b[0] == '{' || b[0] == '[', nil
		}
	}
}

// A successful HTTP status does not make an error envelope a completion.
// Recognize only protocol error fields; ordinary model output is unchanged.
func subscriptionJSONError(data []byte, provider Provider) (Usage, error) {
	return subscriptionResultError(data, provider, "")
}

func subscriptionResultError(data []byte, provider Provider, field string) (Usage, error) {
	probe := probeProviderStreamEvent(string(data))
	var body map[string]any
	if json.Unmarshal(data, &body) != nil || body == nil {
		return Usage{}, nil
	}
	if _, exists := body[field]; field != "" && exists {
		return Usage{}, nil
	}
	response, _ := body["response"].(map[string]any)
	if !probe.isError() && body["status"] != "failed" && response["status"] != "failed" {
		if field != "" {
			return Usage{}, checkProviderResult(body, field, provider, http.StatusOK)
		}
		return Usage{}, nil
	}
	envelope := body
	if response != nil {
		envelope = response
	}
	usageBody := envelope
	if envelope["usage"] == nil && envelope["usageMetadata"] == nil {
		usageBody = body
	}
	usage := usageFromMap(usageBody)
	if raw, ok := usageBody["usage"].(map[string]any); ok {
		if raw["cache_read_input_tokens"] != nil || raw["cache_creation_input_tokens"] != nil {
			usage = anthropicUsage(usageBody)
		}
	} else if usageBody["usageMetadata"] != nil {
		usage = geminiUsage(usageBody)
	}
	detail, _ := envelope["error"].(map[string]any)
	status := subscriptionJSONErrorStatus(detail)
	diagnostic := []byte("Subscription provider returned an error response")
	if len(data) <= providerErrorBodyPrefix {
		if redacted := redactProviderErrorSecrets(data, provider); len(redacted) <= providerErrorBodyPrefix {
			diagnostic = redacted
		}
	}
	err := newProviderHTTPError(status, nil, diagnostic)
	// The payload's inferred status determines disposition; the transport did
	// return 200, which must remain visible in attempt diagnostics.
	failure := AsHTTPError(err)
	failure.UpstreamStatus = http.StatusOK
	failure.diagnostic = string(diagnostic)
	failure.Message = unexpectedProviderMessage
	return usage, &subscriptionJSONFailure{err: err, usage: usage}
}

func subscriptionJSONErrorStatus(detail map[string]any) int {
	for _, key := range []string{"status_code", "status", "code"} {
		if status := int64FromAny(detail[key]); status >= 400 && status <= 599 {
			return int(status)
		}
	}
	for _, key := range []string{"type", "code", "status"} {
		value, _ := detail[key].(string)
		switch strings.ToLower(value) {
		case "authentication_error", "invalid_api_key", "unauthenticated":
			return http.StatusUnauthorized
		case "permission_error", "permission_denied":
			return http.StatusForbidden
		case "insufficient_quota", "billing_hard_limit_reached":
			return http.StatusPaymentRequired
		case "rate_limit_error", "rate_limit_exceeded", "resource_exhausted":
			return http.StatusTooManyRequests
		case "invalid_request_error", "context_length_exceeded", "invalid_argument":
			return http.StatusBadRequest
		case "request_too_large":
			return http.StatusRequestEntityTooLarge
		case "not_found_error", "model_not_found", "not_found":
			return http.StatusNotFound
		case "overloaded_error", "server_overloaded", "unavailable":
			return http.StatusServiceUnavailable
		case "deadline_exceeded":
			return http.StatusGatewayTimeout
		case "server_error", "internal":
			return http.StatusInternalServerError
		}
	}
	return http.StatusBadGateway
}
