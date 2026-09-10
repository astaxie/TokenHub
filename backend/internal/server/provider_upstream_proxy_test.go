package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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

type providerProxySettingsStore struct {
	Store
	fail bool
}

func usePublicProxyTargetLookup(transport http.RoundTripper) {
	transport.(*providerEnvironmentProxyTransport).lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
}

func (store *providerProxySettingsStore) ListResourcesContext(_ context.Context, kind string) ([]AdminResource, error) {
	if store.fail && kind == "settings" {
		return nil, errors.New("temporary settings read failure")
	}
	return store.Store.ListResourcesChecked(kind)
}

func TestProviderTransportUsesConfiguredForwardProxy(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests.Add(1)
		if request.Method != http.MethodConnect {
			t.Errorf("expected HTTPS proxy CONNECT, got %s", request.Method)
		}
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	var directRequests atomic.Int32
	direct := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		directRequests.Add(1)
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})
	transport := providerTransportWithProxy(direct, nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	})
	usePublicProxyTargetLookup(transport)
	request, err := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("expected the test proxy to reject the CONNECT request")
	}
	if proxyRequests.Load() != 1 {
		t.Fatalf("expected one request through the forward proxy, got %d", proxyRequests.Load())
	}
	if directRequests.Load() != 0 {
		t.Fatalf("expected the guarded direct transport to be bypassed, got %d requests", directRequests.Load())
	}
}

func TestProviderProxyRejectsDNSRebindingBeforeProxyConnect(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyRequests.Add(1)
		if request.Host != "8.8.8.8:443" {
			t.Errorf("proxy CONNECT target = %q, want validated IP", request.Host)
		}
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	}).(*providerEnvironmentProxyTransport)
	var lookups atomic.Int32
	transport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}
	request, err := http.NewRequest(http.MethodGet, "https://attacker.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = transport.RoundTrip(request)
	if proxyRequests.Load() != 1 {
		t.Fatalf("first public resolution made %d proxy requests, want 1", proxyRequests.Load())
	}
	_, err = transport.RoundTrip(request)
	if err == nil || !strings.Contains(err.Error(), "resolves only to disallowed addresses") {
		t.Fatalf("rebound private resolution error = %v", err)
	}
	if proxyRequests.Load() != 1 {
		t.Fatalf("rebound private target reached proxy; requests = %d", proxyRequests.Load())
	}
}

func TestProviderProxyRejectsLegacyPublicHTTPBeforeSendingCredentials(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		proxyRequests.Add(1)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	})
	request, err := http.NewRequest(http.MethodGet, "http://public.example/v1/models", nil)
	usePublicProxyTargetLookup(transport)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer provider-secret")
	_, err = transport.RoundTrip(request)
	if AsHTTPError(err).Code != "provider_base_url_insecure_scheme" {
		t.Fatalf("legacy public HTTP error = %v", err)
	}
	if proxyRequests.Load() != 0 {
		t.Fatalf("legacy public HTTP sent credentials to proxy; requests = %d", proxyRequests.Load())
	}
}

func TestProviderTargetTLSFailureIsNotClassifiedAsProxyEgress(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack proxy connection: %v", err)
			return
		}
		_, _ = fmt.Fprint(connection, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = connection.Close()
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	})
	usePublicProxyTargetLookup(transport)
	request, err := http.NewRequest(http.MethodGet, "https://provider.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.RoundTrip(request)
	if err == nil {
		t.Fatal("expected Provider TLS handshake to fail")
	}
	if providerErrorDisposition(err) == ProviderErrorEgress {
		t.Fatalf("Provider TLS failure was classified as proxy egress: %v", err)
	}
}

func TestProviderProxySilentConnectTimesOutAndClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close proxy listener: %v", err)
		}
	})
	connectionClosed := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			connectionClosed <- acceptErr
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				connectionClosed <- readErr
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, readErr := reader.ReadByte()
		connectionClosed <- readErr
	}()
	proxyURL := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	}).(*providerEnvironmentProxyTransport)
	transport.connectTimeout = 50 * time.Millisecond
	usePublicProxyTargetLookup(transport)
	request, err := http.NewRequest(http.MethodGet, "https://provider.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.RoundTrip(request)
	if AsHTTPError(err).Code != "provider_proxy_timeout" || providerErrorDisposition(err) != ProviderErrorEgress {
		t.Fatalf("silent proxy error = %#v", err)
	}
	select {
	case closeErr := <-connectionClosed:
		if closeErr == nil {
			t.Fatal("silent proxy connection closed without EOF/error")
		}
	case <-time.After(time.Second):
		t.Fatal("silent proxy connection was not closed after timeout")
	}
}

func TestProviderProxyTransportPoolIsBoundedAndCleared(t *testing.T) {
	proxyURL := &url.URL{Scheme: "http", Host: "proxy.example:8080", User: url.UserPassword("proxy-user", "proxy-password")}
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	}).(*providerEnvironmentProxyTransport)
	for index := 0; index < providerProxyPoolLimit+20; index++ {
		target := net.ParseIP(fmt.Sprintf("8.8.%d.%d", index/250, index%250+1))
		transport.proxyTunnelTransport(proxyURL, "provider.example:443", "443", []net.IP{target})
	}
	transport.proxiedMu.Lock()
	poolSize := len(transport.proxiedByHost)
	for key := range transport.proxiedByHost {
		if strings.Contains(key, "proxy-user") || strings.Contains(key, "proxy-password") {
			transport.proxiedMu.Unlock()
			t.Fatalf("proxy pool key retained credentials: %q", key)
		}
	}
	transport.proxiedMu.Unlock()
	if poolSize != providerProxyPoolLimit {
		t.Fatalf("proxy pool size = %d, want bounded size %d", poolSize, providerProxyPoolLimit)
	}
	transport.CloseIdleConnections()
	transport.proxiedMu.Lock()
	defer transport.proxiedMu.Unlock()
	if len(transport.proxiedByHost) != 0 {
		t.Fatalf("proxy pool retained %d generations after settings rotation", len(transport.proxiedByHost))
	}
}

func TestProviderProxyPinsConnectIPAndPreservesHostAndSNI(t *testing.T) {
	serverNames := make(chan string, 1)
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "example.com" {
			t.Errorf("Provider Host = %q, want example.com", request.Host)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	provider.StartTLS()
	defer provider.Close()
	providerConfig := provider.TLS.Clone()
	provider.TLS.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		serverNames <- hello.ServerName
		configured := providerConfig.Clone()
		configured.GetConfigForClient = nil
		return configured, nil
	}

	connectTargets := make(chan string, 2)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connectTargets <- request.Host
		if request.Host == "[2001:4860:4860::8888]:443" {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		providerConnection, err := net.Dial("tcp", provider.Listener.Addr().String())
		if err != nil {
			t.Errorf("dial test Provider: %v", err)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		clientConnection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			_ = providerConnection.Close()
			t.Errorf("hijack proxy connection: %v", err)
			return
		}
		defer clientConnection.Close()
		defer providerConnection.Close()
		_, _ = fmt.Fprint(clientConnection, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(providerConnection, clientConnection) }()
		_, _ = io.Copy(clientConnection, providerConnection)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	transport := providerTransportWithProxy(nil, func(candidate *http.Transport) {
		candidate.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	})
	transport.(*providerEnvironmentProxyTransport).lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("2001:4860:4860::8888")},
			{IP: net.ParseIP("8.8.8.8")},
		}, nil
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("Provider status = %d, want 204", response.StatusCode)
	}
	if target := <-connectTargets; target != "[2001:4860:4860::8888]:443" {
		t.Fatalf("first proxy CONNECT target = %q, want first validated IP", target)
	}
	if target := <-connectTargets; target != "8.8.8.8:443" {
		t.Fatalf("fallback proxy CONNECT target = %q, want second validated IP", target)
	}
	if serverName := <-serverNames; serverName != "example.com" {
		t.Fatalf("Provider TLS SNI = %q, want example.com", serverName)
	}
}

func TestCodexSubscriptionResponsesInheritsEnvironmentProxy(t *testing.T) {
	for _, proxyVariables := range []struct {
		name  string
		http  string
		https string
	}{
		{name: "uppercase", http: "HTTP_PROXY", https: "HTTPS_PROXY"},
		{name: "lowercase", http: "http_proxy", https: "https_proxy"},
	} {
		t.Run(proxyVariables.name, func(t *testing.T) {
			connectTargets := make(chan string, 1)
			proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodConnect {
					t.Errorf("environment proxy method = %s, want CONNECT", request.Method)
				}
				connectTargets <- request.Host
				writer.WriteHeader(http.StatusBadGateway)
			}))
			defer proxy.Close()

			for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
				t.Setenv(name, "")
			}
			t.Setenv(proxyVariables.http, proxy.URL)
			t.Setenv(proxyVariables.https, proxy.URL)

			server, _, secret := newCodexCompatibilityRouteTestServer(t, nil)
			t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
			proxyTransport := mustCodexSubscriptionAdapterForTest(t, server).Client.Transport.(*providerEnvironmentProxyTransport)
			proxyTransport.lookup = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			}
			response := doCodexCompatibilityRouteJSON(t, server.Handler(), "/v1/responses", map[string]any{
				"model": codexCompatibilityRouteModel,
				"input": "verify the inherited environment proxy",
			}, secret, "environment-proxy-regression")
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "provider_proxy_connect_failed") {
				t.Fatalf("environment proxy failure = %d: %s", response.Code, response.Body.String())
			}

			select {
			case target := <-connectTargets:
				if target != "8.8.8.8:443" {
					t.Fatalf("environment proxy CONNECT target = %q, want validated IP", target)
				}
			case <-time.After(time.Second):
				t.Fatalf("Codex Subscription request did not use %s", proxyVariables.https)
			}
		})
	}
}

func TestProviderTransportUsesGuardedDirectPathWithoutProxy(t *testing.T) {
	var directRequests atomic.Int32
	direct := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		directRequests.Add(1)
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})
	transport := providerTransportWithProxy(direct, nil, func(*http.Request) (*url.URL, error) {
		return nil, nil
	})
	request, err := http.NewRequest(http.MethodGet, "https://api.example.com/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	if directRequests.Load() != 1 {
		t.Fatalf("expected one guarded direct request, got %d", directRequests.Load())
	}
}

func TestProviderProxyFailureIsResourceNeutralAndStopsFailover(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	transport := providerTransportWithProxy(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("direct path must not be used")
	}), nil, func(*http.Request) (*url.URL, error) {
		return proxyURL, nil
	})
	usePublicProxyTargetLookup(transport)
	request, err := http.NewRequest(http.MethodGet, "https://api.example.com/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.RoundTrip(request)
	if err == nil {
		t.Fatal("expected unreachable proxy to fail")
	}
	if got := providerErrorDisposition(err); got != ProviderErrorEgress {
		t.Fatalf("proxy error disposition = %q, want %q", got, ProviderErrorEgress)
	}
	if got := AsHTTPError(err).Code; got != "provider_proxy_connect_failed" {
		t.Fatalf("proxy error code = %q", got)
	}
	if outcome := providerAttemptOutcome(err); outcome != AttemptNeutral {
		t.Fatalf("proxy error outcome = %v, want neutral", outcome)
	}
	if shouldFailoverRoutedError(err, false) {
		t.Fatal("shared proxy failure must not try another Provider resource")
	}
}

func TestProviderProxyAuthenticationFailureHasSafeStageError(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Proxy-Authenticate", `Basic realm="provider-egress"`)
		writer.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	transport := providerTransportWithProxy(nil, nil, func(*http.Request) (*url.URL, error) { return proxyURL, nil })
	usePublicProxyTargetLookup(transport)
	request, _ := http.NewRequest(http.MethodGet, "https://provider.example/v1/models", nil)
	_, err := transport.RoundTrip(request)
	if AsHTTPError(err).Code != "provider_proxy_auth_failed" || providerErrorDisposition(err) != ProviderErrorEgress {
		t.Fatalf("proxy auth error = %#v", err)
	}
	if strings.Contains(err.Error(), proxyURL.String()) {
		t.Fatalf("proxy auth error leaked proxy URL: %v", err)
	}
}

func TestProviderProxyPolicyRefreshesSharedSettingsWithinFiveSeconds(t *testing.T) {
	base := NewMemoryStore()
	setting := AdminResource{ID: gatewaySettingsID, Name: "Gateway", Status: StatusActive, Fields: map[string]any{}}
	setConfiguredProxyFields(t, setting.Fields, "http://proxy-one.example:8080")
	base.CreateResource("settings", setting)
	policy := newProviderProxyPolicy(base)
	request, _ := http.NewRequest(http.MethodGet, "https://provider.example/v1/models", nil)

	first, err := policy.proxyForRequest(request)
	if err != nil || first.Host != "proxy-one.example:8080" {
		t.Fatalf("initial proxy = %v, %v", first, err)
	}
	setConfiguredProxyFields(t, setting.Fields, "http://proxy-two.example:8081")
	if _, err := base.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(providerProxyRefreshInterval + time.Second)
	for time.Now().Before(deadline) {
		current, refreshErr := policy.proxyForRequest(request)
		if refreshErr == nil && current != nil && current.Host == "proxy-two.example:8081" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("shared Provider proxy settings did not refresh within five seconds")
}

func TestProviderProxyPolicyKeepsLastKnownConfigurationOnTemporaryReadFailure(t *testing.T) {
	base := NewMemoryStore()
	setting := AdminResource{ID: gatewaySettingsID, Name: "Gateway", Status: StatusActive, Fields: map[string]any{}}
	setConfiguredProxyFields(t, setting.Fields, "http://proxy-known.example:8080")
	base.CreateResource("settings", setting)
	store := &providerProxySettingsStore{Store: base}
	policy := newProviderProxyPolicy(store)
	request, _ := http.NewRequest(http.MethodGet, "https://provider.example/v1/models", nil)

	store.fail = true
	policy.mu.Lock()
	policy.loadedAt = time.Now().Add(-providerProxyRefreshInterval)
	policy.mu.Unlock()
	proxyURL, err := policy.proxyForRequest(request)
	if err != nil || proxyURL == nil || proxyURL.Host != "proxy-known.example:8080" {
		t.Fatalf("last-known proxy = %v, %v", proxyURL, err)
	}
}

func TestProviderProxyPolicyFailsClosedBeforeFirstSuccessfulLoad(t *testing.T) {
	store := &providerProxySettingsStore{Store: NewMemoryStore(), fail: true}
	policy := newProviderProxyPolicy(store)
	request, _ := http.NewRequest(http.MethodGet, "https://provider.example/v1/models", nil)
	proxyURL, err := policy.proxyForRequest(request)
	if proxyURL != nil || AsHTTPError(err).Code != "provider_proxy_config_error" {
		t.Fatalf("unloaded policy = %v, %v", proxyURL, err)
	}
}
