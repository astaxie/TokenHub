package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Subscription credentials must never follow an upstream redirect. The supplied
// client's transport is preserved, including TokenHub's egress and SSRF guards.
func subscriptionClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

func subscriptionHTTP(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := subscriptionClient(client).Do(req)
	if err != nil {
		return nil, subscriptionTransportError(ctx, err)
	}
	return finishSubscriptionResponse(resp)
}

func subscriptionTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if guarded := providerEgressFailure(err); guarded != nil {
		return guarded
	}
	return NewHTTPError(http.StatusBadGateway, "subscription_request_failed", "Subscription upstream request failed")
}

func finishSubscriptionResponse(resp *http.Response) (*http.Response, error) {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusNotModified {
		return resp, nil
	}
	defer resp.Body.Close()
	return nil, subscriptionStatusFailure(resp.StatusCode, resp.Header)
}

// subscriptionPost sends one Responses body. A zstd body that the upstream
// refuses is retried once uncompressed. The refusal body is not returned to
// the caller. HTTP 415 is a refusal without reading the body. Any other error
// body is peeked under the stream idle budget, because a streaming client has
// no total deadline.
func subscriptionPost(ctx context.Context, client *http.Client, req *http.Request, rawReq func() (*http.Request, error), encoding string, idle time.Duration) (*http.Response, error) {
	if encoding == "" {
		return subscriptionHTTP(ctx, client, req)
	}
	resp, err := subscriptionClient(client).Do(req)
	if err != nil {
		return nil, subscriptionTransportError(ctx, err)
	}
	if resp.StatusCode < http.StatusBadRequest {
		return finishSubscriptionResponse(resp)
	}
	header := resp.Header.Clone()
	status := resp.StatusCode
	if status == http.StatusUnsupportedMediaType {
		resp.Body.Close()
		return subscriptionCompressionFallback(ctx, client, rawReq)
	}
	body := newIdleTimeoutReadCloser(resp.Body, subscriptionIdleTimeout(idle), NewHTTPError(http.StatusGatewayTimeout, "subscription_stream_idle", "Subscription stream was idle for too long"))
	snippet, readErr := io.ReadAll(io.LimitReader(body, 4096))
	body.Close()
	if readErr == nil && responsesCompressionRejected(status, snippet) {
		return subscriptionCompressionFallback(ctx, client, rawReq)
	}
	return nil, subscriptionStatusFailure(status, header)
}

func subscriptionCompressionFallback(ctx context.Context, client *http.Client, rawReq func() (*http.Request, error)) (*http.Response, error) {
	fallback, buildErr := rawReq()
	if buildErr != nil {
		return nil, buildErr
	}
	return subscriptionHTTP(ctx, client, fallback)
}

func subscriptionStatusFailure(status int, header http.Header) error {
	// The status and headers fully determine this failure. Do not wait for a
	// discarded error body: streaming clients have no total request deadline,
	// and the response's idle guard is installed only after this function.
	message := fmt.Sprintf("Subscription upstream returned HTTP %d", status)
	if status < http.StatusBadRequest {
		failure := NewHTTPError(http.StatusBadGateway, "subscription_upstream_error", message)
		failure.UpstreamStatus = status
		return &ProviderInvocationError{Err: failure, Disposition: ProviderErrorTransientSame, RetryAfter: parseRetryAfter(header.Get("Retry-After"))}
	}
	// Keep standard route classification and parsed retry metadata without
	// exposing provider bodies or copying private headers to the caller.
	return newProviderHTTPError(status, header, []byte(message))
}

func subscriptionDecode(resp *http.Response, target any) error {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 || json.Unmarshal(data, target) != nil {
		return NewHTTPError(http.StatusBadGateway, "subscription_invalid_response", "Subscription upstream returned an invalid response")
	}
	return nil
}

func subscriptionEndpoint(base, fallback, suffix string, allowed func(string) bool) (string, error) {
	base = firstNonEmpty(strings.TrimSpace(base), fallback)
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" && u.Port() != "443" || !allowed(strings.ToLower(u.Hostname())) {
		return "", NewHTTPError(http.StatusBadRequest, "subscription_endpoint_not_allowed", "Subscription credentials cannot be sent to this endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/") + suffix
	return u.String(), nil
}

func subscriptionCredentials(ctx context.Context, provider Provider, refresh func(context.Context, string, bool) (ProviderResourceCredentials, error), force bool) (ProviderResourceCredentials, error) {
	id := strings.TrimSpace(provider.Options["resource_id"])
	if id == "" || refresh == nil {
		return ProviderResourceCredentials{}, NewHTTPError(http.StatusBadRequest, "provider_resource_missing", "Subscription account resource is required")
	}
	credentials, err := refresh(ctx, id, force)
	if err != nil {
		return ProviderResourceCredentials{}, subscriptionCredentialError(err)
	}
	return credentials, nil
}

// Reauthorization is an account failure for routing, while the conflict status
// remains visible to administrator actions. Keep both pieces of information by
// wrapping the HTTP error instead of changing its public status.
func subscriptionCredentialError(err error) error {
	if err == nil || providerErrorDisposition(err) != "" {
		return err
	}
	if httpErr := AsHTTPError(err); httpErr != nil && httpErr.Code == "provider_resource_reauthorization_required" {
		return &ProviderInvocationError{Err: err, Disposition: ProviderErrorAuthBroken}
	}
	return err
}

func subscriptionCatalog(providerType, name, base, source, etag string, models []ProviderCatalogModel) ProviderCatalogEntry {
	categories, counts := catalogCategorySummary(models)
	return ProviderCatalogEntry{ID: providerType, Type: providerType, Name: name, DisplayName: name, BaseURL: base, Source: source,
		Models: models, ModelsCount: len(models), Categories: categories, CategoryCounts: counts, ETag: etag}
}
