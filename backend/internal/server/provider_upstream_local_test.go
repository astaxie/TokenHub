package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func useAutoLocalUpstreams(t *testing.T) {
	t.Helper()
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "auto")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "false")
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_PROXY_LOCAL", "false")
}

func TestLocalUpstreamDefaultsAndLegacyRestrictions(t *testing.T) {
	useAutoLocalUpstreams(t)
	for _, host := range []string{"localhost", "127.0.0.1", "127.0.0.2", "[::ffff:127.0.0.1]", "[::1]", "10.2.3.4", "172.16.0.4", "192.168.2.4", "[fd12::4]"} {
		if err := ValidateProviderUpstreamBaseURL("http://" + host + ":8000/v1"); err != nil {
			t.Errorf("default local URL %s: %v", host, err)
		}
	}
	for _, host := range []string{"169.254.169.254", "100.100.100.200", "[fd00:ec2::254]", "0.0.0.0", "224.0.0.1", "8.8.8.8"} {
		if err := ValidateProviderUpstreamBaseURL("http://" + host + "/v1"); err == nil {
			t.Errorf("unsafe/public plaintext URL %s was accepted", host)
		}
	}
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "192.168.2.4/32")
	if err := ValidateProviderUpstreamBaseURL("http://192.168.2.4/v1"); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"192.168.2.5", "10.2.3.4", "127.0.0.1"} {
		if err := ValidateProviderUpstreamBaseURL("http://" + host + "/v1"); err == nil {
			t.Errorf("legacy restriction was lost for %s", host)
		}
	}
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "invalid-cidr")
	if err := ValidateProviderUpstreamBaseURL("http://192.168.2.4/v1"); err == nil {
		t.Fatal("invalid nonempty allowlist enabled unrestricted private access")
	}
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS", "")
	for _, mode := range []string{"strict", "invalid-mode"} {
		t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", mode)
		for _, host := range []string{"127.0.0.1", "192.168.2.4"} {
			if err := ValidateProviderUpstreamBaseURL("http://" + host + "/v1"); err == nil {
				t.Errorf("%s mode allowed %s", mode, host)
			}
		}
	}
}

func TestLocalHTTPHostnameValidationAndPinnedDial(t *testing.T) {
	useAutoLocalUpstreams(t)
	for _, host := range []string{"host.docker.internal", "model-server", "models.corp.example"} {
		t.Run(host, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, "http://"+host+":8000/v1/models", nil)
			prepared, err := prepareProviderHTTPRequest(request, allowedProviderUpstreamCIDRs(), func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("192.168.2.4")}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			sentinel := errors.New("test dial reached")
			_, err = dialGuardedUpstream(prepared.Context(), "tcp", host+":8000", allowedProviderUpstreamCIDRs(), nil, time.Second,
				func(context.Context, string) ([]net.IPAddr, error) {
					t.Error("pinned request performed a second lookup")
					return nil, nil
				},
				func(_ context.Context, _, address string) (net.Conn, error) {
					called = true
					if address != "192.168.2.4:8000" {
						t.Errorf("dialed %s", address)
					}
					return nil, sentinel
				})
			if !called || !errors.Is(err, sentinel) {
				t.Fatalf("dial called=%v err=%v", called, err)
			}
		})
	}
	for _, addresses := range [][]string{{"8.8.8.8"}, {"192.168.2.4", "8.8.8.8"}, {"192.168.2.4", "169.254.169.254"}, {"fd00:ec2::254"}, {}} {
		request, _ := http.NewRequest(http.MethodPost, "http://models.example/v1/chat/completions", strings.NewReader("sensitive body"))
		request.Header.Set("Authorization", "Bearer test-key")
		transport := guardProviderUpstreamRequests(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			t.Error("unsafe HTTP hostname reached sender")
			return nil, errors.New("unexpected send")
		}), allowedProviderUpstreamCIDRs()).(*providerUpstreamPolicyTransport)
		transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
			var ips []net.IPAddr
			for _, value := range addresses {
				ips = append(ips, net.IPAddr{IP: net.ParseIP(value)})
			}
			return ips, nil
		}
		if _, err := transport.RoundTrip(request); AsHTTPError(err).Code != "provider_base_url_insecure_scheme" {
			t.Errorf("addresses %v: %v", addresses, err)
		}
	}
}

func TestLocalUpstreamProxyBypassAndExplicitProxy(t *testing.T) {
	useAutoLocalUpstreams(t)
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		if r.Method == http.MethodConnect {
			serveLocalHTTPReviewTunnel(t, w, r, nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	for _, force := range []string{"false", "true"} {
		t.Run(force, func(t *testing.T) {
			t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_PROXY_LOCAL", force)
			for _, host := range []string{"127.0.0.1", "[::1]", "192.168.2.4", "[fd12::4]", "host.docker.internal", "model-server", "models.corp.example"} {
				directCalls := 0
				before := proxyRequests.Load()
				transport := providerTransportWithProxy(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					directCalls++
					return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
				}), nil, func(*http.Request) (*url.URL, error) { return proxyURL, nil }).(*providerEnvironmentProxyTransport)
				transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("192.168.2.4")}}, nil
				}
				request, _ := http.NewRequest(http.MethodGet, "http://"+host+":8000/v1/models", nil)
				response, err := transport.RoundTrip(request)
				if err != nil {
					t.Fatalf("%s: %v", host, err)
				}
				_ = response.Body.Close()
				transport.CloseIdleConnections()
				if force == "false" && (directCalls != 1 || proxyRequests.Load() != before) {
					t.Errorf("%s did not bypass proxy", host)
				}
				if force == "true" && (directCalls != 0 || proxyRequests.Load() != before+1) {
					t.Errorf("%s did not use forced proxy", host)
				}
			}
		})
	}
}

func TestLocalHTTPHostnameRealRequestAndRebinding(t *testing.T) {
	useAutoLocalUpstreams(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "model-server:") {
			t.Errorf("Host was not preserved: %s", r.Host)
		}
		_, _ = io.WriteString(w, "local response")
	}))
	defer upstream.Close()
	endpoint, _ := url.Parse(upstream.URL)
	allowed := allowedProviderUpstreamCIDRs()
	direct := guardProviderUpstreamRequests(ssrfGuardedProviderTransport(allowed), allowed)
	transport := providerTransportWithProxy(direct, nil, func(*http.Request) (*url.URL, error) { return nil, nil }).(*providerEnvironmentProxyTransport)
	defer transport.CloseIdleConnections()
	lookups := 0
	transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		lookups++
		ip := "127.0.0.1"
		if lookups > 1 {
			ip = "8.8.8.8"
		}
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	client := &http.Client{Transport: transport, CheckRedirect: strictProviderUpstreamRedirect}
	response, err := client.Get("http://model-server:" + endpoint.Port() + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "local response" || lookups != 1 {
		t.Fatalf("body=%s lookups=%d", body, lookups)
	}
	if _, err := client.Get("http://model-server:" + endpoint.Port() + "/v1/models"); err == nil {
		t.Fatal("public DNS rebinding reused a previously validated HTTP connection")
	}
}
