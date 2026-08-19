package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Most server integration tests use httptest loopback upstreams. Opt in for
	// the package, while the default-deny regression tests explicitly unset it.
	_ = os.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "true")
	os.Exit(m.Run())
}

func validateBaseURL(t *testing.T, raw string) error {
	t.Helper()
	endpoint, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse test URL %q: %v", raw, err)
	}
	// Tests exercise the save-time form by default: operator allowlist empty,
	// localhost exception enabled. Redirect re-validation passes (nil, false)
	// and is covered by dedicated cases below.
	return validateProviderUpstreamBaseURL(endpoint, nil, true)
}

func TestValidateProviderUpstreamBaseURLAllowsPublicHTTPS(t *testing.T) {
	for _, raw := range []string{
		"https://api.example.com/v1",
		"https://api.openai.com/v1",
		// A globally routable literal (Google Public DNS): special-purpose
		// literals such as 203.0.113.x are rejected instead, see below.
		"https://8.8.8.8/v1",
		// DNS64-synthesized form of the same public address (RFC 6052
		// well-known prefix): reachable on IPv6-only networks.
		"https://[64:ff9b::808:808]/v1",
	} {
		if err := validateBaseURL(t, raw); err != nil {
			t.Fatalf("expected %q to be allowed, got %v", raw, err)
		}
	}
}

func TestValidateProviderUpstreamBaseURLRejectsPublicHTTP(t *testing.T) {
	for _, raw := range []string{
		"http://api.example.com/v1",
		"http://8.8.8.8/v1",
	} {
		err := validateBaseURL(t, raw)
		if code := AsHTTPError(err).Code; code != "provider_base_url_insecure_scheme" {
			t.Fatalf("expected %q to require HTTPS, got %q", raw, code)
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
		// Documentation and benchmarking special-purpose ranges are not
		// routable upstreams either.
		"http://192.0.2.10/v1":    "provider_base_url_not_allowed",
		"http://198.51.100.20/v1": "provider_base_url_not_allowed",
		"http://203.0.113.10/v1":  "provider_base_url_not_allowed",
		"http://198.18.0.1/v1":    "provider_base_url_not_allowed",
		"http://[2001:db8::1]/v1": "provider_base_url_not_allowed",
		// Multicast destinations can never serve a unicast upstream API.
		"http://224.0.0.1/v1":     "provider_base_url_not_allowed",
		"http://[ff02::1]/v1":     "provider_base_url_not_allowed",
		"http://255.255.255.255/": "provider_base_url_not_allowed",
		// Deprecated IPv6 site-local space is still routable inside sites, and
		// NAT64 prefixes synthesizing private/metadata IPv4 targets stay
		// rejected (the embedded address drives the classification).
		"http://[fec0::1]/v1":            "provider_base_url_not_allowed",
		"http://[64:ff9b::a00:1]/v1":     "provider_base_url_not_allowed",
		"http://[64:ff9b::a9fe:a9fe]/v1": "provider_base_url_not_allowed",
		"http://[64:ff9b:1::a00:1]/v1":   "provider_base_url_not_allowed",
		// IPv4-mapped IPv6 spellings of private addresses are classified as
		// private through To4, no special range needed.
		"http://[::ffff:10.0.0.5]/v1":      "provider_base_url_not_allowed",
		"http://[::ffff:169.254.169.254]/": "provider_base_url_not_allowed",
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

// TestCustomProviderCatalogFromUpstreamRejectsRedirectToLoopback verifies the
// redirect guard runs in strict mode: even though the initial base URL and the
// redirect target both sit on loopback (legitimate when typed directly), a
// redirect hop must never lead onto a loopback service, because a public URL
// could use the same bounce to reach services bound to the host.
func TestCustomProviderCatalogFromUpstreamRejectsRedirectToLoopback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeModelsPayload(t, w)
	}))
	defer upstream.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, upstream.URL+"/v1/models", http.StatusFound)
	}))
	defer redirector.Close()

	_, err := CustomProviderCatalogFromUpstream(context.Background(), nil, ProviderCreateRequest{
		BaseURL: redirector.URL + "/v1",
		Type:    ProviderOpenAICompatible,
	})
	if err == nil {
		t.Fatal("expected redirect onto a loopback target to be rejected")
	}
	if code := AsHTTPError(err).Code; code != "provider_models_request_failed" {
		t.Fatalf("expected provider_models_request_failed, got %q (%v)", code, err)
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

// TestSSRFGuardedProviderTransportPoolsIdleConnections covers the gateway
// connection-pool sizing. A burst of concurrent requests to one upstream must
// leave every connection in the idle pool, so the requests behind it reuse
// them instead of paying a fresh TCP and TLS handshake; the standard library
// default of two idle connections per host would discard the rest.
func TestSSRFGuardedProviderTransportPoolsIdleConnections(t *testing.T) {
	const parallel = 8

	transport := ssrfGuardedProviderTransport(nil)
	if transport.DialContext == nil {
		t.Fatal("expected guarded transport to install a DialContext")
	}
	if transport.MaxIdleConnsPerHost != 64 {
		t.Fatalf("expected 64 idle connections per host, got %d", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxIdleConns != 256 {
		t.Fatalf("expected a 256 connection idle pool, got %d", transport.MaxIdleConns)
	}

	// The burst handler holds every request until all of them have arrived, so
	// the transport has to open one connection per request.
	arrived := make(chan struct{}, parallel)
	release := make(chan struct{})
	var connections atomic.Int32
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/burst" {
			arrived <- struct{}{}
			<-release
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	}))
	upstream.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	upstream.Start()
	defer upstream.Close()
	// Registered after Close so it runs first: a failed burst must not leave
	// handlers blocked, which would hang the server shutdown.
	var releaseOnce sync.Once
	releaseBurst := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseBurst()

	// PutIdleConn reports per connection whether the transport kept it for
	// reuse; under the default limit the surplus connections would report
	// "too many idle connections for host" instead.
	pooled := make(chan error, parallel)
	trace := &httptrace.ClientTrace{PutIdleConn: func(err error) { pooled <- err }}
	// The timeout bounds Do and the body read together, so a stalled request
	// fails the test instead of hanging the package on the burst collection.
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	burst := make(chan error, parallel)
	for i := 0; i < parallel; i++ {
		go func() {
			request, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodGet, upstream.URL+"/burst", nil)
			if err != nil {
				burst <- err
				return
			}
			response, err := client.Do(request)
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				err = response.Body.Close()
			}
			burst <- err
		}()
	}
	for i := 0; i < parallel; i++ {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for the concurrent burst, %d of %d requests arrived", i, parallel)
		}
	}
	releaseBurst()
	for i := 0; i < parallel; i++ {
		if err := <-burst; err != nil {
			t.Fatalf("burst request failed: %v", err)
		}
	}
	if got := connections.Load(); got != parallel {
		t.Fatalf("expected the burst to open %d connections, got %d", parallel, got)
	}

	// Draining a response returns its connection to the idle pool, so
	// collecting every callback both asserts the pooling and synchronizes the
	// reuse round below.
	for i := 0; i < parallel; i++ {
		select {
		case err := <-pooled:
			if err != nil {
				t.Fatalf("expected every burst connection to stay pooled, got %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for idle connections, only %d of %d were pooled", i, parallel)
		}
	}

	for i := 0; i < parallel; i++ {
		response, err := client.Get(upstream.URL + "/reuse")
		if err != nil {
			t.Fatalf("reuse request %d failed: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	if got := connections.Load(); got != parallel {
		t.Fatalf("expected the reuse round to open no new connections, got %d in total", got)
	}
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
		if err := validateProviderUpstreamBaseURL(endpoint, allowed, true); err != nil {
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
		if err := validateProviderUpstreamBaseURL(endpoint, allowed, true); err == nil {
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
	if err := validateProviderUpstreamBaseURL(endpoint, linkLocal, true); err == nil {
		t.Fatal("expected metadata address to stay rejected even when its range is configured")
	}
}

// TestCheckProviderUpstreamLiteralDialAllowlist verifies the dial-time guard's
// literal-IP decision without any network access: allowlisted private literals
// pass, literals outside the allowlist and special-use addresses fail, and
// public literals always pass.
func TestCheckProviderUpstreamLiteralDialAllowlist(t *testing.T) {
	allowed := mustParseCIDRs(t, "192.168.0.0/16")
	if err := checkProviderUpstreamLiteralDial(net.ParseIP("192.168.1.10"), allowed); err != nil {
		t.Fatalf("expected allowlisted literal to pass, got %v", err)
	}
	// The same address without the allowlist is refused.
	if err := checkProviderUpstreamLiteralDial(net.ParseIP("192.168.1.10"), nil); !errors.Is(err, errProviderUpstreamDialDisallowed) {
		t.Fatalf("expected non-allowlisted literal to be rejected, got %v", err)
	}
	for _, ip := range []string{
		"10.0.0.5", // private but outside the configured ranges
		"169.254.169.254",
		"203.0.113.10",
		"224.0.0.1",
		"fec0::1",        // deprecated site-local, still routable on-site
		"64:ff9b::a00:1", // NAT64 well-known prefix encoding 10.0.0.1
	} {
		if err := checkProviderUpstreamLiteralDial(net.ParseIP(ip), allowed); !errors.Is(err, errProviderUpstreamDialDisallowed) {
			t.Fatalf("expected %s to be rejected as disallowed, got %v", ip, err)
		}
	}
	// Public unicast literals pass regardless of the allowlist, including the
	// DNS64-synthesized form of a public address.
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "64:ff9b::808:808"} {
		if err := checkProviderUpstreamLiteralDial(net.ParseIP(ip), nil); err != nil {
			t.Fatalf("expected public literal %s to pass, got %v", ip, err)
		}
	}
	// Configuring a link-local range must not allowlist the metadata address:
	// it is not RFC1918/ULA private, so the allowlist ignores it.
	if err := checkProviderUpstreamLiteralDial(net.ParseIP("169.254.169.254"), mustParseCIDRs(t, "169.254.0.0/16")); !errors.Is(err, errProviderUpstreamDialDisallowed) {
		t.Fatalf("expected metadata address to stay rejected even when configured, got %v", err)
	}
}

// TestValidateProviderUpstreamBaseURLStrictModeRejectsLoopback pins the
// redirect re-validation form: with the localhost exception disabled, every
// loopback spelling must be rejected, so a validated public URL can never
// redirect onto a service bound to the gateway host.
func TestValidateProviderUpstreamBaseURLStrictModeRejectsLoopback(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:11434/v1",
		"http://[::1]:1234/v1",
		// Allowlisted private literals are also refused in strict mode.
		"http://192.168.1.10/v1",
	} {
		endpoint, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("failed to parse test URL %q: %v", raw, err)
		}
		if err := validateProviderUpstreamBaseURL(endpoint, nil, false); err == nil {
			t.Fatalf("expected %q to be rejected in strict (redirect) mode", raw)
		}
	}
}

// TestAdminCreateProviderRejectsSSRFBaseURL covers the persistence guard the
// review called out: saving a provider whose base URL points at link-local
// metadata or private space must fail at create time, not only when the
// models endpoint is probed.
func TestAdminCreateProviderRejectsSSRFBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/latest/meta-data",
		"http://10.0.0.5/v1",
		"http://192.168.1.10/v1",
		"http://203.0.113.10/v1",
	} {
		store := NewMemoryStore()
		if err := SeedDemoData(store); err != nil {
			t.Fatal(err)
		}
		app := New(store).Handler()
		resp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
			"name":     "SSRF attempt",
			"type":     "local",
			"base_url": baseURL,
		}, "")
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("expected base URL %q to be rejected with 400, got %d: %s", baseURL, resp.Code, resp.Body)
		}
		if !strings.Contains(resp.Body, "provider_base_url_not_allowed") {
			t.Fatalf("expected provider_base_url_not_allowed for %q, got %s", baseURL, resp.Body)
		}
	}
}

func TestAdminCreateProviderRejectsLoopbackByDefault(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "")
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	resp := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Loopback attempt",
		"type":     ProviderOpenAICompatible,
		"base_url": "http://127.0.0.1:11434/v1",
	}, "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "provider_base_url_not_allowed") {
		t.Fatalf("expected loopback provider create to be rejected, got %d: %s", resp.Code, resp.Body)
	}
}

func TestAdminCreateProviderRejectsPublicHTTP(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	resp := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Plaintext public provider",
		"type":     ProviderOpenAICompatible,
		"base_url": "http://api.example.com/v1",
	}, "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "provider_base_url_insecure_scheme") {
		t.Fatalf("expected public HTTP provider create to be rejected, got %d: %s", resp.Code, resp.Body)
	}
}

// TestAdminPatchProviderRejectsSSRFBaseURL verifies the same guard on update:
// an existing healthy provider must not be repointed at a metadata endpoint.
func TestAdminPatchProviderRejectsSSRFBaseURL(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Local vLLM",
		"type":     "local",
		"base_url": "http://localhost:8000/v1",
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider created, got %d: %s", created.Code, created.Body)
	}
	var payload struct {
		Provider Provider `json:"provider"`
	}
	if err := json.Unmarshal([]byte(created.Body), &payload); err != nil {
		t.Fatal(err)
	}

	patched := doJSON(t, app, http.MethodPatch, "/api/admin/providers/"+payload.Provider.ID, map[string]any{
		"base_url": "http://169.254.169.254/latest/meta-data",
	}, "")
	if patched.Code != http.StatusBadRequest {
		t.Fatalf("expected SSRF patch to be rejected with 400, got %d: %s", patched.Code, patched.Body)
	}
	if !strings.Contains(patched.Body, "provider_base_url_not_allowed") {
		t.Fatalf("expected provider_base_url_not_allowed, got %s", patched.Body)
	}

	patched = doJSON(t, app, http.MethodPatch, "/api/admin/providers/"+payload.Provider.ID, map[string]any{
		"base_url": "http://api.example.com/v1",
	}, "")
	if patched.Code != http.StatusBadRequest || !strings.Contains(patched.Body, "provider_base_url_insecure_scheme") {
		t.Fatalf("expected public HTTP provider patch to be rejected, got %d: %s", patched.Code, patched.Body)
	}
}

// TestAdminCreateProviderAllowsAllowlistedPrivateLiteral covers the operator
// workflow for in-house model servers: with the range configured, a literal
// private IP saves successfully.
func TestAdminCreateProviderAllowsAllowlistedPrivateLiteral(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "192.168.0.0/16")
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	resp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "In-house vLLM",
		"type":     "local",
		"base_url": "http://192.168.1.10:8000/v1",
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected allowlisted private literal to be created, got %d: %s", resp.Code, resp.Body)
	}
}

// TestUpstreamClientsGuardInferenceDial verifies the streaming and
// non-streaming inference clients dial through the SSRF guard: disallowed
// literals fail before connecting, while the loopback exception still permits
// local providers.
func TestUpstreamClientsGuardInferenceDial(t *testing.T) {
	client, streamClient, _ := newUpstreamClients(Config{})
	for name, candidate := range map[string]*http.Client{"non-streaming": client, "streaming": streamClient} {
		policy, ok := candidate.Transport.(*providerUpstreamPolicyTransport)
		if !ok {
			t.Fatalf("expected %s client to validate each request before sending it", name)
		}
		transport, ok := policy.next.(*http.Transport)
		if !ok || transport.DialContext == nil {
			t.Fatalf("expected %s client to install a guarded DialContext", name)
		}
		if candidate.CheckRedirect == nil {
			t.Fatalf("expected %s client to enforce the strict redirect policy", name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for _, addr := range []string{"169.254.169.254:80", "10.0.0.5:80", "203.0.113.10:443"} {
			if _, err := transport.DialContext(ctx, "tcp", addr); err == nil || !strings.Contains(err.Error(), "disallowed") {
				t.Fatalf("expected %s client dial to %s to be rejected by the guard, got %v", name, addr, err)
			}
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to start loopback listener: %v", err)
		}
		conn, err := transport.DialContext(ctx, "tcp", listener.Addr().String())
		if err != nil {
			_ = listener.Close()
			t.Fatalf("expected %s client loopback dial to succeed, got %v", name, err)
		}
		conn.Close()
		_ = listener.Close()
	}
	server := New(NewMemoryStore())
	if _, ok := server.codexSubscription.Client.Transport.(*providerUpstreamPolicyTransport); !ok {
		t.Fatal("expected Codex subscription client to validate each request before sending it")
	}
}

// TestProviderResourceBaseURLSSRFGuard covers the resource-level persistence
// paths: routeSelection lets a resource BaseURL override the provider's
// validated one, so create, update and import must all reject disallowed
// URLs, while clearing the override (empty value) and allowlisted literals
// keep working.
func TestProviderResourceBaseURLSSRFGuard(t *testing.T) {
	newStore := func(t *testing.T) (*GormStore, Provider) {
		t.Helper()
		store := NewMemoryStore()
		provider := store.AddProvider(Provider{
			ID:      "prv_ssrf_test",
			Name:    "SSRF test provider",
			Type:    ProviderOpenAICompatible,
			BaseURL: "https://api.example.com/v1",
			Status:  StatusActive,
		})
		return store, provider
	}

	t.Run("create rejects metadata and private URLs", func(t *testing.T) {
		store, provider := newStore(t)
		for _, baseURL := range []string{
			"http://api.example.com/v1",
			"http://169.254.169.254/latest/meta-data",
			"http://192.168.1.10/v1",
			"http://203.0.113.10/v1",
		} {
			_, err := store.AddProviderResource(ProviderResource{
				ProviderID: provider.ID,
				Name:       "bad-" + baseURL,
				BaseURL:    baseURL,
			})
			if err == nil {
				t.Fatalf("expected resource with base URL %q to be rejected", baseURL)
			}
		}
	})

	t.Run("update rejects private URL and allows clearing", func(t *testing.T) {
		store, provider := newStore(t)
		resource, err := store.AddProviderResource(ProviderResource{
			ProviderID: provider.ID,
			Name:       "updatable",
			BaseURL:    "http://localhost:9000/v1",
		})
		if err != nil {
			t.Fatalf("expected localhost resource to be created, got %v", err)
		}
		if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{BaseURL: "http://10.0.0.5/v1"}); err == nil {
			t.Fatal("expected private base URL update to be rejected")
		}
		// Clearing the override (empty value) restores the provider URL and
		// must stay allowed.
		updated, err := store.UpdateProviderResource(resource.ID, ProviderResource{BaseURL: ""})
		if err != nil {
			t.Fatalf("expected clearing the base URL override to succeed, got %v", err)
		}
		if updated.BaseURL != "" {
			t.Fatalf("expected override cleared, got %q", updated.BaseURL)
		}
	})

	t.Run("import fails only the offending row", func(t *testing.T) {
		store, provider := newStore(t)
		result, err := store.ImportProviderResources([]ProviderResource{
			{ProviderID: provider.ID, Name: "good", BaseURL: "http://localhost:9001/v1"},
			{ProviderID: provider.ID, Name: "bad", BaseURL: "http://169.254.169.254/latest/meta-data"},
		})
		if err != nil {
			t.Fatalf("expected import to complete with per-row results, got %v", err)
		}
		if result.Success != 1 || result.Failed != 1 {
			t.Fatalf("expected 1 success and 1 row failure, got %d/%d", result.Success, result.Failed)
		}
	})

	t.Run("allowlisted private literal is accepted", func(t *testing.T) {
		t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "192.168.0.0/16")
		store, provider := newStore(t)
		if _, err := store.AddProviderResource(ProviderResource{
			ProviderID: provider.ID,
			Name:       "in-house",
			BaseURL:    "http://192.168.1.10:8000/v1",
		}); err != nil {
			t.Fatalf("expected allowlisted private literal to be accepted, got %v", err)
		}
	})
}

// TestDialGuardedUpstreamFallbackAfterSlowLookup is the regression test for
// the staggered Happy Eyeballs race: a slow lookup runs first and the first
// resolved address black-holes, yet the healthy second candidate must win in
// about one fallback delay — not after a sequential share of the budget
// (which would burn several seconds, breaking the 15s test-connection
// context), and not after the whole deadline (a fixed up-front slice).
func TestDialGuardedUpstreamFallbackAfterSlowLookup(t *testing.T) {
	budget := 10 * time.Second
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		select {
		case <-time.After(500 * time.Millisecond): // slow resolver
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	var mu sync.Mutex
	var dialed []string
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		if strings.HasPrefix(addr, "1.1.1.1:") {
			<-ctx.Done() // black hole: only the race winner cancels it
			return nil, ctx.Err()
		}
		server, client := net.Pipe()
		go func() {
			defer server.Close()
			_, _ = server.Write([]byte{})
		}()
		return client, nil
	}

	started := time.Now()
	conn, err := dialGuardedUpstream(context.Background(), "tcp", "provider.example:443", nil, nil, budget, lookup, dial)
	if err != nil {
		t.Fatalf("expected fallback to reach the healthy candidate, got %v (dialed: %v)", err, dialed)
	}
	conn.Close()
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Fatalf("expected staggered fallback in about one fallback delay, took %v", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 2 {
		t.Fatalf("expected both validated candidates to be dialed, got %v", dialed)
	}
	seen := map[string]bool{dialed[0]: true, dialed[1]: true}
	if !seen["1.1.1.1:443"] || !seen["8.8.8.8:443"] {
		t.Fatalf("expected the two validated addresses to be dialed, got %v", dialed)
	}
}

// TestRaceValidatedUpstreamCandidatesClosesLosingConnections verifies the
// leak guard: when a slower candidate also connects after the race was
// decided, its connection is closed by the losing goroutine instead of
// being stranded in the results channel.
func TestRaceValidatedUpstreamCandidatesClosesLosingConnections(t *testing.T) {
	var mu sync.Mutex
	var loser net.Conn
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		server, client := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 1)
			_, _ = server.Read(buf) // keep the pipe until the peer closes
		}()
		if strings.HasPrefix(addr, "1.1.1.1:") {
			// Connect just after the fallback delay, so the second candidate
			// has already started and wins the race first. The wait ignores
			// ctx on purpose: it simulates a dial that reports success even
			// though the race was already decided and raceCtx cancelled,
			// which is exactly the case the leak guard exists for.
			time.Sleep(upstreamDialFallbackDelay + 150*time.Millisecond)
			mu.Lock()
			loser = client
			mu.Unlock()
		}
		return client, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := raceValidatedUpstreamCandidates(ctx, "tcp", "443", []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("8.8.8.8")}}, dial)
	if err != nil {
		t.Fatalf("expected the race to produce a winner, got %v", err)
	}
	conn.Close()

	// Wait for the slower attempt to finish connecting, then check its fate.
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		late := loser
		mu.Unlock()
		if late != nil {
			if _, writeErr := late.Write([]byte{0}); writeErr == nil {
				t.Fatal("expected the losing connection to be closed, but it still accepts writes")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the slower candidate never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRaceValidatedUpstreamCandidatesDrainsAfterCancellation covers the
// cancellation boundary: the caller gives up while a dial is still in
// flight, and the dialer then reports success anyway. The drainer must
// collect that late outcome and close the connection instead of stranding
// it in the buffered results channel.
func TestRaceValidatedUpstreamCandidatesDrainsAfterCancellation(t *testing.T) {
	var mu sync.Mutex
	var late net.Conn
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		server, client := net.Pipe()
		go func() {
			defer server.Close()
			buf := make([]byte, 1)
			_, _ = server.Read(buf) // keep the pipe until the peer closes
		}()
		// Ignore ctx on purpose: this simulates a dialer that connects just
		// after the race was abandoned, the exact leak window.
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		late = client
		mu.Unlock()
		return client, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := raceValidatedUpstreamCandidates(ctx, "tcp", "443", []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, dial); err == nil {
		t.Fatal("expected the cancelled race to fail")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		conn := late
		mu.Unlock()
		if conn != nil {
			// Give the drainer a moment to collect the late outcome, then
			// probe exactly once: net.Pipe is synchronous, so a successful
			// write proves the connection is still open.
			time.Sleep(300 * time.Millisecond)
			if _, writeErr := conn.Write([]byte{0}); writeErr == nil {
				t.Fatal("expected the late connection to be closed by the drainer, but it still accepts writes")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the in-flight dial never completed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
