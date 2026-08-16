package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func redirectRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	endpoint, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse redirect URL %q: %v", raw, err)
	}
	return &http.Request{URL: endpoint}
}

func TestStrictProviderUpstreamRedirectAllowsSameOrigin(t *testing.T) {
	original := redirectRequest(t, "https://api.example.com/v1/messages")
	next := redirectRequest(t, "https://api.example.com/v2/messages")
	if err := strictProviderUpstreamRedirect(next, []*http.Request{original}); err != nil {
		t.Fatalf("expected same-origin redirect to pass, got %v", err)
	}
}

func TestStrictProviderUpstreamRedirectRejectsOriginChanges(t *testing.T) {
	original := redirectRequest(t, "https://api.example.com/v1/messages")
	for _, raw := range []string{
		"https://attacker.example/v1/messages",
		"https://api.example.com:8443/v1/messages",
		"http://api.example.com/v1/messages",
	} {
		next := redirectRequest(t, raw)
		if err := strictProviderUpstreamRedirect(next, []*http.Request{original}); err == nil {
			t.Fatalf("expected redirect to %q to be rejected", raw)
		}
	}
}

func TestProviderUpstreamPolicyTransportRejectsPublicHTTPBeforeSendingCredentials(t *testing.T) {
	called := false
	next := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})
	transport := guardProviderUpstreamRequests(next, nil)
	req, err := http.NewRequest(http.MethodPost, "http://api.example.com/v1/chat?key=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("x-api-key", "secret")
	if _, err := transport.RoundTrip(req); AsHTTPError(err).Code != "provider_base_url_insecure_scheme" {
		t.Fatalf("expected public HTTP request to require HTTPS, got %v", err)
	}
	if called {
		t.Fatal("underlying transport was called after credentials were attached to public HTTP")
	}

	req.URL.Scheme = "https"
	if _, err := transport.RoundTrip(req); err != nil || !called {
		t.Fatalf("expected HTTPS request to reach the underlying transport, called=%v err=%v", called, err)
	}
}

func TestConfiguredProviderUpstreamNAT64PrefixClassifiesEmbeddedIPv4(t *testing.T) {
	t.Setenv(providerUpstreamNAT64PrefixEnv, "64:ff9b:1::/48")
	for _, ip := range []string{
		"64:ff9b:1:808:8:800::",    // 8.8.8.8
		"64:ff9b:1:c000:2:100::",   // 192.0.2.1
		"64:ff9b:1:a9fe:a9:fe00::", // 169.254.169.254
	} {
		err := checkProviderUpstreamLiteralDial(net.ParseIP(ip), nil)
		if ip == "64:ff9b:1:808:8:800::" {
			if err != nil {
				t.Fatalf("expected public NAT64 target %s to pass, got %v", ip, err)
			}
			continue
		}
		if !errors.Is(err, errProviderUpstreamDialDisallowed) {
			t.Fatalf("expected special-use NAT64 target %s to be rejected, got %v", ip, err)
		}
	}
}

func TestConfiguredProviderUpstreamNAT64PrefixDecodesRFC6052Formats(t *testing.T) {
	cases := []struct {
		prefix  string
		address string
	}{
		{"2001:db8::/32", "2001:db8:c000:221::"},
		{"2001:db8:100::/40", "2001:db8:1c0:2:21::"},
		{"2001:db8:122::/48", "2001:db8:122:c000:2:2100::"},
		{"2001:db8:122:300::/56", "2001:db8:122:3c0:0:221::"},
		{"2001:db8:122:344::/64", "2001:db8:122:344:c0:2:2100:0"},
		{"2001:db8:122:344::/96", "2001:db8:122:344::c000:221"},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			t.Setenv(providerUpstreamNAT64PrefixEnv, tc.prefix)
			embedded, translated := providerUpstreamEmbeddedNAT64IPv4(net.ParseIP(tc.address))
			if !translated || embedded == nil || embedded.String() != "192.0.2.33" {
				t.Fatalf("expected %s to decode to 192.0.2.33, got %v (translated=%v)", tc.address, embedded, translated)
			}
		})
	}
}

func TestProviderUpstreamAllowlistCannotBypassHardDenials(t *testing.T) {
	allowed := mustParseCIDRs(t, "fd00::/8")
	t.Setenv(providerUpstreamNAT64PrefixEnv, "fd00:64::/64")
	for _, ip := range []string{
		"fd00:ec2::254",      // AWS IPv6 instance metadata
		"fd00:64::a:0:100:0", // NAT64 encoding of 10.0.0.1
	} {
		if err := checkProviderUpstreamLiteralDial(net.ParseIP(ip), allowed); !errors.Is(err, errProviderUpstreamDialDisallowed) {
			t.Fatalf("expected %s to stay rejected despite the allowlist, got %v", ip, err)
		}
	}
}

func TestInvalidProviderUpstreamNAT64PrefixFailsClosed(t *testing.T) {
	for _, prefix := range []string{
		"64:ff9b:1::/72",            // unsupported RFC 6052 prefix length
		"2001:db8:122:344:100::/96", // non-zero bits 64-71 violate RFC 6052
	} {
		t.Run(prefix, func(t *testing.T) {
			t.Setenv(providerUpstreamNAT64PrefixEnv, prefix)
			if block, _ := configuredProviderUpstreamNAT64Prefix(); block != nil {
				t.Fatalf("expected invalid NAT64 prefix %s to be ignored", prefix)
			}
		})
	}
	t.Setenv(providerUpstreamNAT64PrefixEnv, "64:ff9b:1::/72")
	if err := checkProviderUpstreamLiteralDial(net.ParseIP("64:ff9b:1:808:8:800::"), nil); !errors.Is(err, errProviderUpstreamDialDisallowed) {
		t.Fatalf("expected local-use NAT64 address to stay rejected with an invalid prefix, got %v", err)
	}
}

func TestProviderUpstreamLoopbackRequiresExplicitOptIn(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "")
	if err := ValidateProviderUpstreamBaseURL("http://127.0.0.1:11434/v1"); err == nil {
		t.Fatal("expected loopback base URL to be rejected by default")
	}

	dialed := false
	_, err := dialGuardedUpstream(
		context.Background(), "tcp", "127.0.0.1:11434", nil, nil, time.Second,
		nil,
		func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, nil
		},
	)
	if err == nil || dialed {
		t.Fatalf("expected loopback dial to fail before connecting, got err=%v dialed=%v", err, dialed)
	}
}

func TestProviderUpstreamLoopbackExplicitOptIn(t *testing.T) {
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "true")
	if err := ValidateProviderUpstreamBaseURL("http://localhost:11434/v1"); err != nil {
		t.Fatalf("expected opted-in loopback base URL to pass, got %v", err)
	}

	dialed := false
	_, err := dialGuardedUpstream(
		context.Background(), "tcp", "127.0.0.1:11434", nil, nil, time.Second,
		nil,
		func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, nil
		},
	)
	if err != nil || !dialed {
		t.Fatalf("expected opted-in loopback dial to connect, got err=%v dialed=%v", err, dialed)
	}
}

func TestProviderUpstreamRejectsCuratedNonProviderRanges(t *testing.T) {
	for _, raw := range []string{
		"192.0.0.1", "192.0.0.6", "192.0.0.8", "192.0.0.170", "192.0.0.171",
		"192.88.99.1", "192.88.99.2",
		"100::1", "100:0:0:1::1", "2001:2::1", "3fff::1", "5f00::1",
	} {
		t.Run(raw, func(t *testing.T) {
			ip := net.ParseIP(raw)
			if err := checkProviderUpstreamLiteralDial(ip, nil); !errors.Is(err, errProviderUpstreamDialDisallowed) {
				t.Fatalf("expected dial target %s to be rejected, got %v", raw, err)
			}
			host := raw
			if ip.To4() == nil {
				host = "[" + raw + "]"
			}
			if err := ValidateProviderUpstreamBaseURL("http://" + host + "/v1"); err == nil {
				t.Fatalf("expected save-time target %s to be rejected", raw)
			}
		})
	}

	for _, raw := range []string{
		"192.0.0.9", "192.0.0.10",
		"2001:1::1", "2001:1::2", "2001:1::3",
	} {
		ip := net.ParseIP(raw)
		if err := checkProviderUpstreamLiteralDial(ip, nil); err != nil {
			t.Fatalf("expected globally reachable target %s to pass dial classification, got %v", raw, err)
		}
		host := raw
		if ip.To4() == nil {
			host = "[" + raw + "]"
		}
		if err := ValidateProviderUpstreamBaseURL("https://" + host + "/v1"); err != nil {
			t.Fatalf("expected globally reachable target %s to pass save-time validation, got %v", raw, err)
		}
	}
}
