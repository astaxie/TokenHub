package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func validateBaseURL(t *testing.T, raw string) error {
	t.Helper()
	endpoint, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse test URL %q: %v", raw, err)
	}
	// Tests exercise the strict form by default (redirect re-validation and
	// hostname resolution never get the private-range allowlist).
	return validateProviderUpstreamBaseURL(endpoint, nil)
}

func TestValidateProviderUpstreamBaseURLAllowsPublicHTTPS(t *testing.T) {
	for _, raw := range []string{
		"https://api.example.com/v1",
		"https://api.openai.com/v1",
		"http://api.example.com:8080/v1",
		"https://203.0.113.10/v1",
	} {
		if err := validateBaseURL(t, raw); err != nil {
			t.Fatalf("expected %q to be allowed, got %v", raw, err)
		}
	}
}

func TestValidateProviderUpstreamBaseURLRejectsMetadataAndPrivate(t *testing.T) {
	cases := map[string]string{
		"http://169.254.169.254/latest/meta-data": "provider_base_url_not_allowed",
		"http://169.254.0.1/":                     "provider_base_url_not_allowed",
		"http://10.0.0.5/v1":                      "provider_base_url_not_allowed",
		"http://172.16.0.9/v1":                    "provider_base_url_not_allowed",
		"http://192.168.1.10/v1":                  "provider_base_url_not_allowed",
		"http://[fd00::1]/v1":                     "provider_base_url_not_allowed",
		"http://[fe80::1]/v1":                     "provider_base_url_not_allowed",
		"http://0.0.0.0/v1":                       "provider_base_url_not_allowed",
		"http://[::]/v1":                          "provider_base_url_not_allowed",
		// Shared/CGNAT space hosts Alibaba Cloud's metadata endpoint.
		"http://100.100.100.200/latest/meta-data": "provider_base_url_not_allowed",
		"http://100.64.0.1/v1":                    "provider_base_url_not_allowed",
		// This-host and reserved ranges are never valid upstreams.
		"http://0.0.0.8/v1":   "provider_base_url_not_allowed",
		"http://240.0.0.1/v1": "provider_base_url_not_allowed",
	}
	for raw, wantCode := range cases {
		err := validateBaseURL(t, raw)
		if err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
		if code := AsHTTPError(err).Code; code != wantCode {
			t.Fatalf("expected %q to fail with code %q, got %q", raw, wantCode, code)
		}
	}
}

func TestValidateProviderUpstreamBaseURLRejectsNonHTTPSchemes(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"ftp://example.com/v1",
		"gopher://example.com/",
		"ws://example.com/v1",
	} {
		err := validateBaseURL(t, raw)
		if err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
		if code := AsHTTPError(err).Code; code != "provider_base_url_invalid_scheme" {
			t.Fatalf("expected %q to fail with provider_base_url_invalid_scheme, got %q", raw, code)
		}
	}
}

func TestValidateProviderUpstreamBaseURLRejectsEmbeddedCredentials(t *testing.T) {
	err := validateBaseURL(t, "https://user:secret@api.example.com/v1")
	if err == nil {
		t.Fatal("expected URL with embedded credentials to be rejected")
	}
	if code := AsHTTPError(err).Code; code != "provider_base_url_invalid" {
		t.Fatalf("expected provider_base_url_invalid, got %q", code)
	}
}

func TestValidateProviderUpstreamBaseURLAllowsLocalhostException(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:11434/v1",
		"http://[::1]:1234/v1",
	} {
		if err := validateBaseURL(t, raw); err != nil {
			t.Fatalf("expected local provider URL %q to be allowed, got %v", raw, err)
		}
	}
}

// writeModelsPayload writes a minimal OpenAI-compatible /models body.
func writeModelsPayload(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"data": []any{map[string]any{"id": "test-model", "object": "model"}},
	}); err != nil {
		t.Fatalf("failed to encode models payload: %v", err)
	}
}

// TestCustomProviderCatalogFromUpstreamAllowsRedirectToPublic verifies that a
// redirect to another allowed (loopback, in this in-process test) URL is
// followed and the models payload is loaded. The redirect target is a second
// httptest server on 127.0.0.1, which the localhost exception permits.
func TestCustomProviderCatalogFromUpstreamAllowsRedirectToPublic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			writeModelsPayload(t, w)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, upstream.URL+"/v1/models", http.StatusFound)
	}))
	defer redirector.Close()

	entry, err := CustomProviderCatalogFromUpstream(context.Background(), nil, ProviderCreateRequest{
		BaseURL: redirector.URL + "/v1",
		Type:    ProviderOpenAICompatible,
	})
	if err != nil {
		t.Fatalf("expected redirect to allowed target to succeed, got %v", err)
	}
	if entry.ModelsCount != 1 {
		t.Fatalf("expected 1 model after redirect, got %d", entry.ModelsCount)
	}
}

// TestCustomProviderCatalogFromUpstreamRejectsRedirectToPrivate verifies the
// CheckRedirect guard: a public URL that 302s to a literal private/link-local
// address (the cloud metadata endpoint) must be refused even though the
// initial base URL passed validation.
func TestCustomProviderCatalogFromUpstreamRejectsRedirectToPrivate(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer redirector.Close()

	_, err := CustomProviderCatalogFromUpstream(context.Background(), nil, ProviderCreateRequest{
		BaseURL: redirector.URL + "/v1",
		Type:    ProviderOpenAICompatible,
	})
	if err == nil {
		t.Fatal("expected redirect to a link-local metadata address to be rejected")
	}
	// The guarded client surfaces the CheckRedirect failure as a request error,
	// which the function wraps as provider_models_request_failed.
	if code := AsHTTPError(err).Code; code != "provider_models_request_failed" {
		t.Fatalf("expected provider_models_request_failed, got %q (%v)", code, err)
	}
}

// TestCustomProviderCatalogFromUpstreamRejectsRedirectToNonHTTP verifies that a
// redirect to a non-http(s) scheme is refused by the redirect guard.
func TestCustomProviderCatalogFromUpstreamRejectsRedirectToNonHTTP(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer redirector.Close()

	_, err := CustomProviderCatalogFromUpstream(context.Background(), nil, ProviderCreateRequest{
		BaseURL: redirector.URL + "/v1",
		Type:    ProviderOpenAICompatible,
	})
	if err == nil {
		t.Fatal("expected redirect to a file:// URL to be rejected")
	}
}

// TestCustomProviderCatalogFromUpstreamCustomClientKeepsRedirectGuard verifies
// that when the caller passes a custom client (as tests do), the redirect
// re-validation is still enforced on top of the custom Transport.
func TestCustomProviderCatalogFromUpstreamCustomClientKeepsRedirectGuard(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer redirector.Close()

	custom := &http.Client{Timeout: 5 * time.Second}
	_, err := CustomProviderCatalogFromUpstream(context.Background(), custom, ProviderCreateRequest{
		BaseURL: redirector.URL + "/v1",
		Type:    ProviderOpenAICompatible,
	})
	if err == nil {
		t.Fatal("expected redirect to a link-local metadata address to be rejected with a custom client")
	}
}

// TestCustomProviderCatalogFromUpstreamDropsCallerCheckRedirect verifies the
// guard replaces any caller-supplied CheckRedirect that would otherwise allow
// every redirect, so a permissive custom client cannot silently re-open the
// redirect bypass.
func TestCustomProviderCatalogFromUpstreamDropsCallerCheckRedirect(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer redirector.Close()

	permissive := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return nil },
	}
	_, err := CustomProviderCatalogFromUpstream(context.Background(), permissive, ProviderCreateRequest{
		BaseURL: redirector.URL + "/v1",
		Type:    ProviderOpenAICompatible,
	})
	if err == nil {
		t.Fatal("expected redirect guard to override the caller's permissive CheckRedirect")
	}
}

// TestSSRFGuardedDialContextRejectsPrivateLiteral exercises the dial-time
// guard directly: dialing a disallowed literal IP must fail, while the
// loopback exception and a hostname that resolves to loopback must succeed.
func TestSSRFGuardedDialContextRejectsPrivateLiteral(t *testing.T) {
	transport := ssrfGuardedProviderTransport(nil)
	if transport.DialContext == nil {
		t.Fatal("expected guarded transport to install a DialContext")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Literal link-local (metadata) address must be rejected before dialing.
	if _, err := transport.DialContext(ctx, "tcp", "169.254.169.254:80"); err == nil {
		t.Fatal("expected dial to a link-local literal address to be rejected")
	}
	// Literal private address must be rejected.
	if _, err := transport.DialContext(ctx, "tcp", "10.0.0.5:80"); err == nil {
		t.Fatal("expected dial to a private literal address to be rejected")
	}

	// Loopback is the intentional exception; start a real listener and dial it.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start loopback listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	conn, err := transport.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("expected loopback dial to succeed, got %v", err)
	}
	conn.Close()

	// "localhost" resolves to loopback and must be allowed through the resolver path.
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to split listener address: %v", err)
	}
	_ = host
	conn, err = transport.DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("expected localhost dial to succeed, got %v", err)
	}
	conn.Close()
}

// TestSSRFGuardedDialContextDialsValidatedAddress confirms that when a
// hostname resolves to an allowed address, the guard dials that address
// directly rather than re-resolving, by pointing the request at the loopback
// listener via the "localhost" hostname.
func TestSSRFGuardedDialContextDialsValidatedAddress(t *testing.T) {
	transport := ssrfGuardedProviderTransport(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start loopback listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to split listener address: %v", err)
	}

	conn, err := transport.DialContext(ctx, "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("expected dial via localhost to reach the listener, got %v", err)
	}
	if !strings.HasPrefix(conn.RemoteAddr().String(), "127.0.0.1:") && !strings.HasPrefix(conn.RemoteAddr().String(), "[::1]:") {
		t.Fatalf("expected connection to a loopback address, got %s", conn.RemoteAddr())
	}
	conn.Close()
}

func mustParseCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatalf("failed to parse test CIDR %q: %v", cidr, err)
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func TestAllowedProviderUpstreamCIDRsParsesEnv(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "10.0.0.0/8, 192.168.0.0/16;not-a-cidr")
	blocks := allowedProviderUpstreamCIDRs()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 valid CIDR blocks (invalid entry skipped), got %d", len(blocks))
	}
	if !blocks[0].Contains(net.ParseIP("10.1.2.3")) || !blocks[1].Contains(net.ParseIP("192.168.1.10")) {
		t.Fatalf("parsed blocks do not cover the configured ranges: %v", blocks)
	}
}

// TestValidateProviderUpstreamBaseURLAllowlistedPrivateLiteral verifies the
// operator allowlist: literal private IPs inside the configured ranges pass,
// while private IPs outside the ranges and link-local/metadata addresses stay
// rejected (the allowlist can never widen to non-private special-use ranges).
func TestValidateProviderUpstreamBaseURLAllowlistedPrivateLiteral(t *testing.T) {
	allowed := mustParseCIDRs(t, "192.168.0.0/16", "10.0.0.0/8")
	for _, raw := range []string{
		"http://192.168.1.10:8000/v1",
		"http://10.0.0.5/v1",
	} {
		endpoint, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("failed to parse test URL %q: %v", raw, err)
		}
		if err := validateProviderUpstreamBaseURL(endpoint, allowed); err != nil {
			t.Fatalf("expected allowlisted literal %q to pass, got %v", raw, err)
		}
	}
	for _, raw := range []string{
		// Private but outside the configured ranges.
		"http://172.16.0.9/v1",
		"http://[fd00::1]/v1",
		// Link-local metadata can never be allowlisted (not RFC1918/ULA).
		"http://169.254.169.254/latest/meta-data",
		// CGNAT metadata range is likewise not RFC1918/ULA private.
		"http://100.100.100.200/latest/meta-data",
	} {
		endpoint, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("failed to parse test URL %q: %v", raw, err)
		}
		if err := validateProviderUpstreamBaseURL(endpoint, allowed); err == nil {
			t.Fatalf("expected %q to stay rejected despite the allowlist", raw)
		}
	}
	// Even an operator attempting to allowlist link-local must fail: the range
	// is not private, so isAllowlistedPrivateProviderUpstreamIP ignores it.
	linkLocal := mustParseCIDRs(t, "169.254.0.0/16")
	endpoint, err := url.Parse("http://169.254.169.254/latest/meta-data")
	if err != nil {
		t.Fatalf("failed to parse metadata URL: %v", err)
	}
	if err := validateProviderUpstreamBaseURL(endpoint, linkLocal); err == nil {
		t.Fatal("expected metadata address to stay rejected even when its range is configured")
	}
}

// TestSSRFGuardedDialContextAllowlistedPrivateLiteral verifies the dial-time
// guard applies the allowlist only to literal IPs: an allowlisted literal
// private address reaches the dialer (failing only because nothing listens),
// while the same address without the allowlist is refused up front.
func TestSSRFGuardedDialContextAllowlistedPrivateLiteral(t *testing.T) {
	// 192.168.222.222 on an unusual port: nothing should be listening there in
	// test environments, and the sandbox has no route, so a passing check
	// surfaces as a connection error rather than the guard's rejection.
	guarded := ssrfGuardedProviderTransport(mustParseCIDRs(t, "192.168.0.0/16"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := guarded.DialContext(ctx, "tcp", "192.168.222.222:19999")
	if err == nil {
		conn.Close()
		return // unexpectedly reachable: the allowlist clearly let the dial through
	}
	if strings.Contains(err.Error(), "disallowed") {
		t.Fatalf("expected allowlisted literal to reach the dialer, got guard rejection: %v", err)
	}

	strict := ssrfGuardedProviderTransport(nil)
	if _, err := strict.DialContext(ctx, "tcp", "192.168.222.222:19999"); err == nil || !strings.Contains(err.Error(), "disallowed") {
		t.Fatalf("expected non-allowlisted literal to be rejected by the guard, got %v", err)
	}
}
