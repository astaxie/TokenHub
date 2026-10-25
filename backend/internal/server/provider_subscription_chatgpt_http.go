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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if guarded := providerEgressFailure(err); guarded != nil {
			return nil, guarded
		}
		return nil, NewHTTPError(http.StatusBadGateway, "subscription_request_failed", "Subscription upstream request failed")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 || resp.StatusCode == http.StatusNotModified {
		return resp, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	message := fmt.Sprintf("Subscription upstream returned HTTP %d", resp.StatusCode)
	if resp.StatusCode < http.StatusBadRequest {
		failure := NewHTTPError(http.StatusBadGateway, "subscription_upstream_error", message)
		failure.UpstreamStatus = resp.StatusCode
		return nil, &ProviderInvocationError{Err: failure, Disposition: ProviderErrorTransientSame}
	}
	// Keep standard route classification and parsed retry metadata without
	// exposing provider bodies or copying private headers to the caller.
	return nil, newProviderHTTPError(resp.StatusCode, resp.Header, []byte(message))
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
	return refresh(ctx, id, force)
}

func subscriptionCatalog(providerType, name, base, source, etag string, models []ProviderCatalogModel) ProviderCatalogEntry {
	categories, counts := catalogCategorySummary(models)
	return ProviderCatalogEntry{ID: providerType, Type: providerType, Name: name, DisplayName: name, BaseURL: base, Source: source,
		Models: models, ModelsCount: len(models), Categories: categories, CategoryCounts: counts, ETag: etag}
}
