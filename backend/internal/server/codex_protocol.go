package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var codexRequestHeaderAllowlist = []string{
	"session-id",
	"session_id",
	"thread-id",
	"x-client-request-id",
	"x-codex-turn-state",
	"x-codex-turn-metadata",
	"x-codex-parent-thread-id",
	"x-codex-window-id",
	"x-codex-installation-id",
	"x-codex-beta-features",
	"x-openai-internal-codex-responses-lite",
	"accept-language",
}

var codexResponseHeaderAllowlist = []string{
	"x-codex-turn-state",
	"x-codex-rate-limit-reached-type",
	"x-codex-promo-message",
	"x-codex-active-limit",
	"x-codex-safety-buffering-enabled",
	"x-codex-safety-buffering-faster-model",
	"x-models-etag",
	"openai-model",
	"x-reasoning-included",
	"x-request-id",
	"x-oai-request-id",
	"cf-ray",
}

var codexResponseHeaderPrefixes = []string{
	"x-codex-primary-",
	"x-codex-secondary-",
	"x-codex-credits-",
}

func applyCodexRequestHeaders(target http.Header, incoming http.Header) {
	for _, key := range codexRequestHeaderAllowlist {
		if value := boundedHeaderValue(incoming.Get(key)); value != "" {
			target.Set(key, value)
		}
	}
	setCodexClientIdentityHeader(target, incoming, "User-Agent", openAICodexUserAgent)
	setCodexClientIdentityHeader(target, incoming, "Version", openAICodexVersion)
	setCodexClientIdentityHeader(target, incoming, "Originator", "codex_cli_rs")
	setCodexClientIdentityHeader(target, incoming, "OpenAI-Beta", "responses=experimental")
}

func applyCodexRequestEnvelope(request *ResponsesRequest, incoming http.Header) {
	if request == nil || !codexResponsesLiteEnabled(incoming) {
		return
	}
	if request.Reasoning == nil {
		request.Reasoning = &ResponsesReasoning{}
	}
	request.Reasoning.Context = "all_turns"
}

func applyCodexCompactEnvelope(body map[string]json.RawMessage, incoming http.Header) {
	if body == nil || !codexResponsesLiteEnabled(incoming) {
		return
	}
	reasoning := ResponsesReasoning{Context: "all_turns"}
	if encoded, ok := body["reasoning"]; ok {
		_ = json.Unmarshal(encoded, &reasoning)
		reasoning.Context = "all_turns"
	}
	setResponsesReasoningField(body, reasoning)
}

func codexResponsesLiteEnabled(headers http.Header) bool {
	value := strings.TrimSpace(headers.Get("x-openai-internal-codex-responses-lite"))
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

func setCodexClientIdentityHeader(target http.Header, incoming http.Header, key string, fallback string) {
	value := boundedHeaderValue(incoming.Get(key))
	if value == "" {
		value = fallback
	}
	if value != "" {
		target.Set(key, value)
	}
}

func boundedHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func codexResponseHeaders(source http.Header) http.Header {
	result := make(http.Header)
	for key, values := range source {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if !codexResponseHeaderAllowed(normalized) {
			continue
		}
		for _, value := range values {
			if value = boundedHeaderValue(value); value != "" {
				result.Add(key, value)
			}
		}
	}
	return result
}

func codexResponseHeaderAllowed(key string) bool {
	for _, allowed := range codexResponseHeaderAllowlist {
		if key == allowed {
			return true
		}
	}
	for _, prefix := range codexResponseHeaderPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return codexDynamicRateLimitHeader(key)
}

func codexDynamicRateLimitHeader(key string) bool {
	if !strings.HasPrefix(key, "x-") {
		return false
	}
	for _, suffix := range []string{
		"-primary-used-percent",
		"-primary-window-minutes",
		"-primary-reset-at",
		"-secondary-used-percent",
		"-secondary-window-minutes",
		"-secondary-reset-at",
		"-limit-name",
	} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func writeCodexResponseHeaders(target http.Header, source http.Header) {
	safeHeaders := codexResponseHeaders(source)
	upstreamRequestID := firstNonEmpty(
		boundedHeaderValue(safeHeaders.Get("x-request-id")),
		boundedHeaderValue(safeHeaders.Get("x-oai-request-id")),
		boundedHeaderValue(safeHeaders.Get("cf-ray")),
	)
	for key, values := range safeHeaders {
		if strings.EqualFold(key, "x-request-id") || strings.EqualFold(key, "x-oai-request-id") {
			continue
		}
		target.Del(key)
		for _, value := range values {
			target.Add(key, value)
		}
	}
	if upstreamRequestID != "" {
		target.Set("x-tokenhub-upstream-request-id", upstreamRequestID)
	}
}

func applyCodexResponseMetadata(usage *Usage, headers http.Header) {
	if usage == nil {
		return
	}
	usage.ResponseHeaders = codexResponseHeaders(headers)
	usage.UpstreamRequestID = firstNonEmpty(
		boundedHeaderValue(headers.Get("x-request-id")),
		boundedHeaderValue(headers.Get("x-oai-request-id")),
		boundedHeaderValue(headers.Get("cf-ray")),
	)
	usage.ServedModel = boundedHeaderValue(headers.Get("openai-model"))
	usage.ModelETag = boundedHeaderValue(headers.Get("x-models-etag"))
	usage.Transport = "http_sse"
}

func codexRateLimitHeaders(headers http.Header) map[string]string {
	result := map[string]string{}
	for key, values := range codexResponseHeaders(headers) {
		normalized := strings.ToLower(key)
		if !strings.HasPrefix(normalized, "x-codex-primary-") &&
			!strings.HasPrefix(normalized, "x-codex-secondary-") &&
			!strings.HasPrefix(normalized, "x-codex-credits-") &&
			normalized != "x-codex-rate-limit-reached-type" &&
			normalized != "x-codex-promo-message" &&
			!codexDynamicRateLimitHeader(normalized) {
			continue
		}
		if len(values) > 0 {
			result[normalized] = values[len(values)-1]
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func classifyCodexHTTPError(status int, headers http.Header, body []byte) error {
	code, message := codexErrorCodeAndMessage(body)
	lowerCode := strings.ToLower(code)
	lowerMessage := strings.ToLower(message)
	retryAfter := parseRetryAfter(headers.Get("retry-after"))

	disposition := ProviderErrorClient
	publicCode := "codex_upstream_error"
	publicStatus := status
	switch {
	case strings.Contains(lowerCode, "unsupported_model") ||
		(strings.Contains(lowerMessage, "model") && strings.Contains(lowerMessage, "not supported")):
		disposition = ProviderErrorModelUnsupported
		publicCode = "codex_model_unsupported"
	case codexErrorContains(lowerCode, lowerMessage, "context_window", "context length", "too many tokens"):
		disposition = ProviderErrorClient
		publicCode = "context_window_exceeded"
		publicStatus = http.StatusBadRequest
	case codexErrorContains(lowerCode, lowerMessage, "policy", "safety", "cyber", "invalid_prompt", "bio_policy"):
		disposition = ProviderErrorPolicy
		publicCode = "codex_policy_rejected"
		if publicStatus >= http.StatusInternalServerError {
			publicStatus = http.StatusBadRequest
		}
	case codexQuotaExhausted(headers, lowerCode, lowerMessage):
		disposition = ProviderErrorQuotaExhausted
		publicCode = "codex_quota_exhausted"
		publicStatus = http.StatusTooManyRequests
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		disposition = ProviderErrorAuthBroken
		publicCode = "codex_account_auth_failed"
	case status == http.StatusTooManyRequests:
		disposition = ProviderErrorTransientSame
		publicCode = "codex_rate_limited"
	case status >= http.StatusInternalServerError:
		disposition = ProviderErrorTransientSame
		publicCode = "codex_upstream_unavailable"
	case status == http.StatusRequestTimeout:
		disposition = ProviderErrorTransientSame
		publicCode = "codex_upstream_timeout"
	}
	if publicStatus <= 0 {
		publicStatus = http.StatusBadGateway
	}
	if message == "" {
		message = http.StatusText(status)
	}
	httpErr := NewHTTPError(publicStatus, publicCode, message)
	// The public status is deliberately not the upstream one, so record what the
	// account actually answered for the route attempt log to report.
	httpErr.UpstreamStatus = status
	return &ProviderInvocationError{
		Err:         httpErr,
		Disposition: disposition,
		RetryAfter:  retryAfter,
	}
}

func codexErrorCodeAndMessage(body []byte) (string, string) {
	message := strings.TrimSpace(string(body))
	code := ""
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return code, message
	}
	for _, candidate := range []map[string]any{payload, mapFromAny(payload["error"])} {
		if candidate == nil {
			continue
		}
		code = firstNonEmpty(code, stringFromAny(candidate["code"]), stringFromAny(candidate["type"]))
		message = firstNonEmpty(stringFromAny(candidate["message"]), stringFromAny(candidate["detail"]), message)
	}
	return strings.TrimSpace(code), strings.TrimSpace(message)
}

func mapFromAny(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func stringFromAny(value any) string {
	result, _ := value.(string)
	return strings.TrimSpace(result)
}

func codexErrorContains(code string, message string, needles ...string) bool {
	combined := code + " " + message
	for _, needle := range needles {
		if strings.Contains(combined, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func codexQuotaExhausted(headers http.Header, code string, message string) bool {
	if boundedHeaderValue(headers.Get("x-codex-rate-limit-reached-type")) != "" {
		return true
	}
	return codexErrorContains(code, message,
		"quota",
		"usage limit",
		"usage_limit",
		"subscription",
		"credits",
		"limit reached",
	)
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
	}
	return 0
}
