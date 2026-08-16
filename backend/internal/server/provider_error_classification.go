package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
)

// providerErrorBodyPrefix is how much of a failed upstream response is read for
// the error message. Provider error bodies are small; the bound is what keeps a
// misbehaving upstream from making the gateway buffer an arbitrary amount.
const providerErrorBodyPrefix = 4096

// An upstream failure has to answer three separate questions, and collapsing them
// into one status code answers none of them well:
//
//   - what the caller is told, which decides whether the caller retries;
//   - whether the router may try another candidate, which decides whether a
//     request that cannot possibly succeed elsewhere burns quota trying;
//   - what the attempt proved about the provider resource, which decides whether
//     it counts toward a breaker.
//
// A malformed request is malformed at every provider, so it must not fail over
// and says nothing about the resource. A broken credential is specific to the
// resource, so it must fail over and does count against it. Both used to arrive
// as 502 provider_error, which reads as a transient gateway fault and got both
// decisions wrong.
type providerErrorClass struct {
	// status is what the caller sees. It is not always the upstream status: an
	// upstream 401 means TokenHub's credential for that provider is broken, and
	// forwarding 401 would tell the caller their own API key was rejected.
	status      int
	code        string
	disposition ProviderErrorDisposition
}

func classifyProviderStatus(upstreamStatus int) providerErrorClass {
	switch upstreamStatus {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// The request itself is wrong, so the caller has to change it. Forwarding
		// the status is what lets them see that rather than retrying a gateway.
		return providerErrorClass{upstreamStatus, "provider_invalid_request", ProviderErrorClient}
	case http.StatusUnauthorized, http.StatusForbidden:
		return providerErrorClass{http.StatusBadGateway, "provider_auth_error", ProviderErrorAuthBroken}
	case http.StatusPaymentRequired:
		return providerErrorClass{http.StatusBadGateway, "provider_payment_required", ProviderErrorQuotaExhausted}
	case http.StatusNotFound:
		// Usually a model, deployment or path this account does not have. Another
		// candidate may well have it, but the account is not thereby broken.
		return providerErrorClass{http.StatusBadGateway, "provider_model_not_found", ProviderErrorModelUnsupported}
	case http.StatusRequestTimeout:
		return providerErrorClass{http.StatusGatewayTimeout, "provider_upstream_timeout", ProviderErrorTransientSame}
	case http.StatusTooManyRequests:
		return providerErrorClass{http.StatusTooManyRequests, "provider_rate_limited", ProviderErrorTransientSame}
	case http.StatusRequestEntityTooLarge:
		return providerErrorClass{http.StatusRequestEntityTooLarge, "provider_invalid_request", ProviderErrorClient}
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		// Forwarded rather than flattened: they already say which kind of upstream
		// fault this was, and every one of them is worth another candidate.
		return providerErrorClass{upstreamStatus, "provider_upstream_unavailable", ProviderErrorTransientSame}
	}
	if upstreamStatus >= http.StatusInternalServerError {
		return providerErrorClass{http.StatusBadGateway, "provider_upstream_error", ProviderErrorTransientSame}
	}
	// An unrecognised 4xx keeps the gateway status, because forwarding a status
	// whose meaning here is unknown could mislead the caller worse than 502 does.
	// It still gets the no-failover, resource-neutral treatment: whatever it
	// means, a 4xx is about the request rather than about the account.
	return providerErrorClass{http.StatusBadGateway, "provider_error", ProviderErrorClient}
}

// newProviderPolicyRefusal reports content the provider refused to process. It
// is the caller's prompt that was rejected, so the account is neither at fault
// nor worth failing over from: another provider would refuse it too, and counting
// it would cool down a working account over content it correctly declined.
func newProviderPolicyRefusal(message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadRequest, "content_filter", message),
		Disposition: ProviderErrorPolicy,
	}
}

// providerErrorMessage decides how much of the upstream body the caller sees. A
// rejected credential or an unpaid account is TokenHub's problem with its own
// provider account, and the bodies quote it: OpenAI echoes a masked API key,
// Azure names the subscription key. Those go no further than the attempt log,
// where the recorded upstream status already says what happened.
func providerErrorMessage(class providerErrorClass, data []byte) string {
	switch class.code {
	case "provider_auth_error":
		return "The gateway's credential for this provider was rejected"
	case "provider_payment_required":
		return "The gateway's account with this provider cannot be billed"
	}
	return strings.TrimSpace(string(data))
}

// newProviderMisconfigured reports a provider that cannot be called at all. That
// is the resource's own defect, so it counts against it and the router moves on.
func newProviderMisconfigured(message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusServiceUnavailable, "provider_not_configured", message),
		Disposition: ProviderErrorResourceBroken,
	}
}

// checkProviderResponse reports whether an upstream response carries a failure,
// and if so consumes it: a bounded prefix of the body becomes the error message
// and the body is closed. It deliberately only inspects a response someone else
// sent, because how a request is sent is not shared — a streaming call runs on a
// client with no total deadline and an idle budget, and folding sending into
// this check would put every caller on one policy.
//
// A nil error leaves the body open and owned by the caller.
func checkProviderResponse(resp *http.Response) error {
	return checkProviderResponseForProvider(resp, Provider{})
}

func checkProviderResponseForProvider(resp *http.Response, provider Provider) error {
	if resp.StatusCode < http.StatusBadRequest {
		return nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, providerErrorBodyPrefix))
	data = redactProviderErrorSecrets(data, provider)
	if provider.Type == ProviderKronk {
		return newKronkProviderHTTPError(resp.StatusCode, resp.Header, data)
	}
	return newProviderHTTPError(resp.StatusCode, resp.Header, data)
}

func newKronkProviderHTTPError(upstreamStatus int, headers http.Header, data []byte) error {
	kronkCode := kronkErrorCode(data)
	normalized := normalizeKronkErrorBody(data)
	class := classifyProviderStatus(upstreamStatus)
	switch kronkCode {
	case "resource_exhausted", "insufficient_resources", "out_of_memory":
		class = providerErrorClass{http.StatusServiceUnavailable, "provider_resource_exhausted", ProviderErrorTransientSame}
	case "model_not_found":
		class = providerErrorClass{http.StatusBadGateway, "provider_model_not_found", ProviderErrorModelUnsupported}
	case "deadline_exceeded", "timeout":
		class = providerErrorClass{http.StatusGatewayTimeout, "provider_upstream_timeout", ProviderErrorTransientSame}
	case "unauthenticated", "permission_denied":
		class = providerErrorClass{http.StatusBadGateway, "provider_auth_error", ProviderErrorAuthBroken}
	}
	httpErr := NewHTTPError(class.status, class.code, providerErrorMessage(class, normalized))
	httpErr.UpstreamStatus = upstreamStatus
	return &ProviderInvocationError{
		Err:         httpErr,
		Disposition: class.disposition,
		RetryAfter:  parseRetryAfter(headers.Get("retry-after")),
	}
}

func kronkErrorCode(data []byte) string {
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	code, _ := payload["code"].(string)
	return strings.ToLower(strings.TrimSpace(code))
}

func normalizeKronkErrorBody(data []byte) []byte {
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		return data
	}
	message, _ := payload["message"].(string)
	code, _ := payload["code"].(string)
	if strings.TrimSpace(message) == "" && strings.TrimSpace(code) == "" {
		return data
	}
	wrapped, err := json.Marshal(map[string]any{"error": map[string]any{
		"code": strings.TrimSpace(code), "message": strings.TrimSpace(message),
	}})
	if err != nil {
		return data
	}
	return wrapped
}

func redactProviderErrorSecrets(data []byte, provider Provider) []byte {
	values := make([]string, 0, len(provider.SensitiveHeaders)+1)
	if value := strings.TrimSpace(provider.APIKey); value != "" {
		values = append(values, value)
	}
	for _, name := range provider.SensitiveHeaders {
		if value, ok := providerHeaderValue(provider.Headers, name); ok && value != "" && value != providerHeaderMask {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return data
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	if providerErrorBodyLooksLikeJSON(data) {
		// Validate without materialising strings or maps first. A truncated or
		// malformed JSON body has no unambiguous string boundaries, so failing
		// closed avoids both escape bypasses and large temporary allocations.
		if !json.Valid(data) {
			return []byte(providerHeaderMask)
		}
		if redacted, valid := redactProviderJSONSecrets(data, values); valid {
			return redacted
		}
		return []byte(providerHeaderMask)
	}
	message := string(data)
	representations := make(map[string]bool, len(values)*3)
	for _, value := range values {
		for _, representation := range providerSecretRepresentations(value) {
			representations[representation] = true
		}
	}
	patterns := make([]string, 0, len(representations))
	for representation := range representations {
		patterns = append(patterns, representation)
	}
	sort.Slice(patterns, func(i, j int) bool { return len(patterns[i]) > len(patterns[j]) })
	for _, pattern := range patterns {
		message = strings.ReplaceAll(message, pattern, providerHeaderMask)
	}
	return []byte(message)
}

func providerErrorBodyLooksLikeJSON(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{', '[', '"':
		return true
	default:
		return false
	}
}

// redactProviderJSONSecrets reports valid when data is one complete JSON value.
// Invalid or truncated JSON is handled by the caller's fail-closed path.
func redactProviderJSONSecrets(data []byte, secrets []string) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	redacted, changed := redactProviderJSONValue(payload, secrets)
	if !changed {
		return data, true
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return nil, false
	}
	return encoded, true
}

func redactProviderJSONValue(value any, secrets []string) (any, bool) {
	switch typed := value.(type) {
	case string:
		redacted := typed
		for _, secret := range secrets {
			redacted = strings.ReplaceAll(redacted, secret, providerHeaderMask)
		}
		return redacted, redacted != typed
	case []any:
		changed := false
		for index, item := range typed {
			redacted, itemChanged := redactProviderJSONValue(item, secrets)
			if itemChanged {
				typed[index] = redacted
				changed = true
			}
		}
		return typed, changed
	case map[string]any:
		changed := false
		redactedMap := make(map[string]any, len(typed))
		for key, item := range typed {
			redactedKey, keyChanged := redactProviderJSONValue(key, secrets)
			redactedItem, itemChanged := redactProviderJSONValue(item, secrets)
			redactedMap[redactedKey.(string)] = redactedItem
			changed = changed || keyChanged || itemChanged
		}
		return redactedMap, changed
	default:
		return value, false
	}
}

func redactProviderStreamEventSecrets(event serverSentEvent, provider Provider) []byte {
	redacted := redactProviderErrorSecrets([]byte(event.Data), provider)
	if bytes.Equal(redacted, []byte(event.Data)) {
		return event.Raw
	}
	return rewriteSSEEventData(event.Raw, string(redacted))
}

func providerSecretRepresentations(value string) []string {
	representations := []string{value}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) < 2 {
		return representations
	}
	escaped := string(encoded[1 : len(encoded)-1])
	if escaped != value {
		representations = append(representations, escaped)
	}
	slashEscaped := strings.ReplaceAll(escaped, "/", `\/`)
	if slashEscaped != escaped {
		representations = append(representations, slashEscaped)
	}
	uppercaseUnicodeEscapes := strings.NewReplacer(
		`\u003c`, `\u003C`,
		`\u003e`, `\u003E`,
		`\u0026`, `\u0026`,
	).Replace(escaped)
	if uppercaseUnicodeEscapes != escaped {
		representations = append(representations, uppercaseUnicodeEscapes)
	}
	return representations
}

// newProviderHTTPError turns one upstream failure into the error the rest of the
// gateway routes, reports and counts on. Sanitized valid bodies retain their
// provider-specific fields for callers; malformed JSON may already have been
// replaced by a fail-closed mask because its string boundaries are ambiguous.
func newProviderHTTPError(upstreamStatus int, headers http.Header, data []byte) error {
	class := classifyProviderStatus(upstreamStatus)
	httpErr := NewHTTPError(class.status, class.code, providerErrorMessage(class, data))
	httpErr.UpstreamStatus = upstreamStatus
	return &ProviderInvocationError{
		Err:         httpErr,
		Disposition: class.disposition,
		RetryAfter:  parseRetryAfter(headers.Get("retry-after")),
	}
}
