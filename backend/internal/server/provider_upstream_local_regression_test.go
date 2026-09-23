package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestStrictLocalProxyBypassRetainsExplicitLiteralExceptions(t *testing.T) {
	useAutoLocalUpstreams(t)
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "strict")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "true")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "192.168.2.4/32")
	for _, host := range []string{"localhost", "127.0.0.1", "192.168.2.4"} {
		request, _ := http.NewRequest(http.MethodGet, "http://"+host+":8000/v1/models", nil)
		ip := net.ParseIP(host)
		if host == "localhost" {
			ip = net.ParseIP("127.0.0.1")
		}
		prepared := pinProviderDirectTargets(request, []net.IP{ip})
		called := false
		sentinel := errors.New("dial reached")
		_, err := dialGuardedUpstream(prepared.Context(), "tcp", host+":8000", allowedProviderUpstreamCIDRs(), nil, time.Second, nil,
			func(context.Context, string, string) (net.Conn, error) { called = true; return nil, sentinel })
		if !called || !errors.Is(err, sentinel) {
			t.Errorf("%s lost its explicit exception: %v", host, err)
		}
	}
}

func TestSyntheticDNSIsNotAPlaintextOrProxyBypassException(t *testing.T) {
	useAutoLocalUpstreams(t)
	for _, pool := range []string{"10.0.0.0/8", "198.18.0.0/15"} {
		t.Run(pool, func(t *testing.T) {
			_, block, _ := net.ParseCIDR(pool)
			ip := block.IP
			snapshot := providerSyntheticDNSSnapshot{blocks: []*net.IPNet{block}, allowPrivateRanges: true}
			lookup := func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: ip}}, nil }
			request, _ := http.NewRequest(http.MethodGet, "http://public-provider.example/v1/models", nil)
			if _, err := prepareProviderHTTPRequest(request, allowedProviderUpstreamCIDRs(), lookup, snapshot); err == nil {
				t.Fatal("synthetic DNS pool granted plaintext permission")
			}
			proxied := false
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxied = true
				if r.Method != http.MethodConnect {
					t.Errorf("method=%s", r.Method)
				}
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer proxy.Close()
			proxyURL, _ := url.Parse(proxy.URL)
			transport := providerTransportWithProxy(roundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Error("synthetic DNS incorrectly bypassed proxy")
				return nil, errors.New("unexpected direct request")
			}), nil, func(*http.Request) (*url.URL, error) { return proxyURL, nil }).(*providerEnvironmentProxyTransport)
			defer transport.CloseIdleConnections()
			transport.lookup = lookup
			transport.syntheticDNS = snapshot
			request.URL.Scheme = "https"
			_, _ = transport.RoundTrip(request)
			if !proxied {
				t.Fatal("synthetic DNS request did not reach selected proxy")
			}
		})
	}
}

func TestInjectedHTTPTransportDialsPreflightIP(t *testing.T) {
	useAutoLocalUpstreams(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	endpoint, _ := url.Parse(upstream.URL)
	base := upstream.Client().Transport.(*http.Transport).Clone()
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != endpoint.Host {
			t.Errorf("unvalidated address reached injected dialer: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	guarded := guardCustomProviderTransport(base, allowedProviderUpstreamCIDRs()).(*providerUpstreamPolicyTransport)
	guarded.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	client := &http.Client{Transport: guarded}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://model-server:" + endpoint.Port() + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}
