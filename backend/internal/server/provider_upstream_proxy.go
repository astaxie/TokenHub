package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type providerProxySelector func(*http.Request) (*url.URL, error)

type selectedProviderProxyContextKey struct{}

const (
	providerProxyConnectTimeout = 15 * time.Second
	providerProxyPoolLimit      = 128
)

// providerEnvironmentProxyTransport keeps proxy and direct egress as separate
// connection pools. Direct requests retain TokenHub's guarded DialContext;
// proxied requests resolve and validate the target locally, then pin the proxy
// request or CONNECT tunnel to the checked IP while preserving Host and SNI.
type providerEnvironmentProxyTransport struct {
	direct         http.RoundTripper
	selectProxy    providerProxySelector
	configure      func(*http.Transport)
	lookup         upstreamLookupFunc
	syntheticDNS   providerSyntheticDNSResolver
	proxyDialer    *net.Dialer
	connectTimeout time.Duration
	proxiedMu      sync.Mutex
	proxiedByHost  map[string]*http.Transport
}

func providerTransportWithEnvironmentProxy(direct http.RoundTripper, configure func(*http.Transport), policies ...*providerProxyPolicy) http.RoundTripper {
	selectProxy := providerProxySelector(http.ProxyFromEnvironment)
	var policy *providerProxyPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	if policy != nil {
		selectProxy = policy.proxyForRequest
	}
	transport := providerTransportWithProxy(direct, configure, selectProxy)
	if policy != nil {
		policy.registerTransport(transport.(providerProxyIdleCloser))
	}
	return transport
}

func providerTransportWithProxy(direct http.RoundTripper, configure func(*http.Transport), selectProxy providerProxySelector) http.RoundTripper {
	if direct == nil {
		direct = http.DefaultTransport
	}
	if selectProxy == nil {
		selectProxy = http.ProxyFromEnvironment
	}
	return &providerEnvironmentProxyTransport{
		direct:         direct,
		selectProxy:    selectProxy,
		configure:      configure,
		lookup:         net.DefaultResolver.LookupIPAddr,
		proxyDialer:    &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
		connectTimeout: providerProxyConnectTimeout,
		proxiedByHost:  make(map[string]*http.Transport),
	}
}

func (transport *providerEnvironmentProxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	if err := validateProviderUpstreamBaseURL(request.URL, allowedProviderUpstreamCIDRs(), providerUpstreamLoopbackAllowed()); err != nil {
		return nil, err
	}
	proxyURL, err := transport.selectProxy(request)
	if err != nil {
		if providerErrorDisposition(err) == ProviderErrorEgress {
			return nil, err
		}
		return nil, newProviderProxyTransportError("config", err)
	}
	if proxyURL == nil {
		return transport.direct.RoundTrip(request)
	}
	targets, targetPort, err := transport.resolveProxyTargets(request)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(request.URL.Scheme, "https") {
		response, err := transport.proxyTunnelTransport(proxyURL, request.URL.Host, targetPort, targets).RoundTrip(request)
		if err != nil {
			if egressErr := providerEgressFailure(err); egressErr != nil {
				return nil, egressErr
			}
			return nil, err
		}
		return response, nil
	}
	pinnedRequest := pinProviderProxyTarget(request, targets[0], targetPort)
	ctx := context.WithValue(request.Context(), selectedProviderProxyContextKey{}, proxyURL)
	pinnedRequest = pinnedRequest.Clone(ctx)
	response, err := transport.proxyTransportForHost(request.URL.Hostname()).RoundTrip(pinnedRequest)
	if err != nil {
		if egressErr := providerEgressFailure(err); egressErr != nil {
			return nil, egressErr
		}
		return nil, newProviderProxyTransportError("connect", err)
	}
	if response.StatusCode == http.StatusProxyAuthRequired {
		_ = response.Body.Close()
		return nil, newProviderProxyTransportError("auth", nil)
	}
	return response, nil
}

func (*providerEnvironmentProxyTransport) providerEgressTransport() {}

func (transport *providerEnvironmentProxyTransport) CloseIdleConnections() {
	if closer, ok := transport.direct.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	transport.proxiedMu.Lock()
	proxied := make([]*http.Transport, 0, len(transport.proxiedByHost))
	for _, candidate := range transport.proxiedByHost {
		proxied = append(proxied, candidate)
	}
	transport.proxiedByHost = make(map[string]*http.Transport)
	transport.proxiedMu.Unlock()
	for _, candidate := range proxied {
		candidate.CloseIdleConnections()
	}
}

func (transport *providerEnvironmentProxyTransport) resolveProxyTargets(request *http.Request) ([]net.IP, string, error) {
	host := request.URL.Hostname()
	port := request.URL.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[strings.ToLower(request.URL.Scheme)]
	}
	if port == "" {
		return nil, "", NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}

	lookupCtx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	targets, err := resolveProviderProxyTargetIPs(lookupCtx, host, allowedProviderUpstreamCIDRs(), transport.syntheticDNS, transport.lookup)
	if err != nil {
		return nil, "", err
	}
	return targets, port, nil
}

// pinProviderProxyTarget rewrites only the connection authority for a
// plaintext HTTP proxy request. Public Provider URLs must use HTTPS, so this
// path is limited to explicit loopback/private-literal operator exceptions.
func pinProviderProxyTarget(request *http.Request, target net.IP, port string) *http.Request {
	pinned := request.Clone(request.Context())
	pinnedURL := *request.URL
	pinnedURL.Host = net.JoinHostPort(target.String(), port)
	pinned.URL = &pinnedURL
	if pinned.Host == "" {
		pinned.Host = request.URL.Host
	}
	return pinned
}

func resolveProviderProxyTargetIPs(ctx context.Context, host string, allowedPrivate []*net.IPNet, syntheticDNS providerSyntheticDNSResolver, lookup upstreamLookupFunc) ([]net.IP, error) {
	if isLocalProviderHostname(host) {
		if !providerUpstreamLoopbackAllowed() {
			return nil, errProviderUpstreamDialDisallowed
		}
		if literal := net.ParseIP(host); literal != nil {
			return []net.IP{literal}, nil
		}
		addresses, err := lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		var loopback []net.IP
		for _, address := range addresses {
			if address.IP.IsLoopback() {
				loopback = appendUniqueProviderProxyIP(loopback, address.IP)
			}
		}
		if len(loopback) > 0 {
			return loopback, nil
		}
		return nil, fmt.Errorf("provider base URL host %q did not resolve to loopback", host)
	}
	if literal := net.ParseIP(host); literal != nil {
		if err := checkProviderUpstreamLiteralDial(literal, allowedPrivate); err != nil {
			return nil, err
		}
		return []net.IP{literal}, nil
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var allowed []net.IP
	for _, address := range addresses {
		if !isDisallowedProviderUpstreamIP(address.IP) || syntheticDNS != nil && syntheticDNS.allowsResolvedIPContext(ctx, address.IP) {
			allowed = appendUniqueProviderProxyIP(allowed, address.IP)
		}
	}
	if len(allowed) > 0 {
		return allowed, nil
	}
	return nil, fmt.Errorf("provider base URL host %q resolves only to disallowed addresses", host)
}

func appendUniqueProviderProxyIP(addresses []net.IP, candidate net.IP) []net.IP {
	for _, existing := range addresses {
		if existing.Equal(candidate) {
			return addresses
		}
	}
	return append(addresses, candidate)
}

// proxyTunnelTransport returns a pool whose DialContext establishes a proxy
// CONNECT tunnel to one of the validated target IPs before net/http writes any
// Provider request bytes. The original request URL is untouched, so the inner
// TLS handshake and HTTP Host continue to use the Provider hostname.
func (transport *providerEnvironmentProxyTransport) proxyTunnelTransport(proxyURL *url.URL, targetAuthority, targetPort string, targets []net.IP) *http.Transport {
	canonicalTargets := make([]string, 0, len(targets))
	for _, target := range targets {
		canonicalTargets = append(canonicalTargets, target.String())
	}
	sort.Strings(canonicalTargets)
	key := "tunnel|" + providerProxyURLFingerprint(proxyURL) + "|" + strings.ToLower(targetAuthority) + "|" + strings.Join(canonicalTargets, ",")
	transport.proxiedMu.Lock()
	defer transport.proxiedMu.Unlock()
	if existing := transport.proxiedByHost[key]; existing != nil {
		return existing
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	var tunneled *http.Transport
	if ok {
		tunneled = base.Clone()
	} else {
		tunneled = &http.Transport{}
	}
	tunneled.Proxy = nil
	tunneled.MaxIdleConnsPerHost = 64
	tunneled.MaxIdleConns = 256
	if transport.configure != nil {
		transport.configure(tunneled)
	}
	proxyTLS := tunneled.TLSClientConfig
	tunneled.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialProviderProxyTunnel(ctx, transport.proxyDialer, transport.connectTimeout, proxyURL, targetPort, targets, proxyTLS)
	}
	transport.storeProxyTransportLocked(key, tunneled)
	return tunneled
}

func dialProviderProxyTunnel(ctx context.Context, dialer *net.Dialer, connectTimeout time.Duration, proxyURL *url.URL, targetPort string, targets []net.IP, tlsConfig *tls.Config) (net.Conn, error) {
	tunnelCtx, cancelTunnel := context.WithTimeout(ctx, connectTimeout)
	defer cancelTunnel()
	proxyAddress := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddress = net.JoinHostPort(proxyURL.Hostname(), map[string]string{"http": "80", "https": "443"}[strings.ToLower(proxyURL.Scheme)])
	}
	var lastErr error
	for index, target := range targets {
		deadline, _ := tunnelCtx.Deadline()
		attemptBudget := time.Until(deadline) / time.Duration(len(targets)-index)
		if attemptBudget <= 0 {
			lastErr = context.DeadlineExceeded
			break
		}
		attemptCtx, cancelAttempt := context.WithTimeout(tunnelCtx, attemptBudget)
		connection, err := dialer.DialContext(attemptCtx, "tcp", proxyAddress)
		if err != nil {
			cancelAttempt()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, newProviderProxyTransportError("connect", err)
		}
		deadlineConnection := connection
		stopCancellation := context.AfterFunc(attemptCtx, func() {
			_ = deadlineConnection.SetDeadline(time.Now())
		})
		if deadline, ok := attemptCtx.Deadline(); ok {
			_ = connection.SetDeadline(deadline)
		}
		if strings.EqualFold(proxyURL.Scheme, "https") {
			proxyTLS := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: proxyURL.Hostname(), NextProtos: []string{"http/1.1"}}
			if tlsConfig != nil {
				proxyTLS = tlsConfig.Clone()
				proxyTLS.ServerName = proxyURL.Hostname()
				proxyTLS.NextProtos = []string{"http/1.1"}
			}
			secured := tls.Client(connection, proxyTLS)
			if err := secured.HandshakeContext(attemptCtx); err != nil {
				stopCancellation()
				cancelAttempt()
				_ = connection.Close()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, newProviderProxyTransportError("connect", err)
			}
			connection = secured
		}
		targetAddress := net.JoinHostPort(target.String(), targetPort)
		header := make(http.Header)
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			credential := base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username() + ":" + password))
			header.Set("Proxy-Authorization", "Basic "+credential)
		}
		connectRequest := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: targetAddress}, Host: targetAddress, Header: header}
		if err := connectRequest.Write(connection); err != nil {
			stopCancellation()
			cancelAttempt()
			_ = connection.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		response, err := http.ReadResponse(bufio.NewReader(connection), connectRequest)
		if err != nil {
			stopCancellation()
			cancelAttempt()
			_ = connection.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if response.StatusCode == http.StatusProxyAuthRequired {
			stopCancellation()
			cancelAttempt()
			_ = connection.Close()
			_ = response.Body.Close()
			return nil, newProviderProxyTransportError("auth", nil)
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			if !stopCancellation() {
				cancelAttempt()
				_ = connection.Close()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				lastErr = context.DeadlineExceeded
				continue
			}
			cancelAttempt()
			_ = connection.SetDeadline(time.Time{})
			return connection, nil
		}
		stopCancellation()
		cancelAttempt()
		_ = connection.Close()
		_ = response.Body.Close()
		lastErr = fmt.Errorf("proxy CONNECT to validated target returned status %d", response.StatusCode)
	}
	if errors.Is(lastErr, context.DeadlineExceeded) {
		return nil, newProviderProxyTransportError("timeout", lastErr)
	}
	var networkError net.Error
	if errors.As(lastErr, &networkError) && networkError.Timeout() {
		return nil, newProviderProxyTransportError("timeout", lastErr)
	}
	return nil, newProviderProxyTransportError("connect", lastErr)
}

func providerProxyURLFingerprint(proxyURL *url.URL) string {
	sum := sha256.Sum256([]byte(proxyURL.String()))
	return hex.EncodeToString(sum[:])
}

func (transport *providerEnvironmentProxyTransport) storeProxyTransportLocked(key string, candidate *http.Transport) {
	if len(transport.proxiedByHost) >= providerProxyPoolLimit {
		for existingKey, existing := range transport.proxiedByHost {
			delete(transport.proxiedByHost, existingKey)
			existing.CloseIdleConnections()
			break
		}
	}
	transport.proxiedByHost[key] = candidate
}

func (transport *providerEnvironmentProxyTransport) proxyTransportForHost(host string) *http.Transport {
	key := strings.ToLower(strings.TrimSpace(host))
	transport.proxiedMu.Lock()
	defer transport.proxiedMu.Unlock()
	if existing := transport.proxiedByHost[key]; existing != nil {
		return existing
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	var proxied *http.Transport
	if ok {
		proxied = base.Clone()
	} else {
		proxied = &http.Transport{}
	}
	proxied.MaxIdleConnsPerHost = 64
	proxied.MaxIdleConns = 256
	proxied.Proxy = func(request *http.Request) (*url.URL, error) {
		proxyURL, _ := request.Context().Value(selectedProviderProxyContextKey{}).(*url.URL)
		return proxyURL, nil
	}
	if transport.configure != nil {
		transport.configure(proxied)
	}
	if proxied.TLSClientConfig == nil {
		proxied.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		proxied.TLSClientConfig = proxied.TLSClientConfig.Clone()
	}
	proxied.TLSClientConfig.ServerName = key
	proxied.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		plain, err := transport.proxyDialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		proxyHost, _, err := net.SplitHostPort(address)
		if err != nil {
			_ = plain.Close()
			return nil, err
		}
		proxyTLS := proxied.TLSClientConfig.Clone()
		proxyTLS.ServerName = proxyHost
		proxyTLS.NextProtos = []string{"http/1.1"}
		secured := tls.Client(plain, proxyTLS)
		if err := secured.HandshakeContext(ctx); err != nil {
			_ = plain.Close()
			return nil, err
		}
		return secured, nil
	}
	proxied.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
		if response == nil {
			return newProviderProxyTransportError("connect", nil)
		}
		if response.StatusCode == http.StatusProxyAuthRequired {
			return newProviderProxyTransportError("auth", nil)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return newProviderProxyTransportError("connect", nil)
		}
		return nil
	}
	transport.storeProxyTransportLocked(key, proxied)
	return proxied
}

func newProviderProxyTransportError(stage string, err error) error {
	status := http.StatusBadGateway
	code := "provider_proxy_connect_failed"
	message := "Provider proxy connection failed"
	switch stage {
	case "config":
		status = http.StatusServiceUnavailable
		code = "provider_proxy_config_error"
		message = "Provider proxy configuration is invalid"
	case "auth":
		code = "provider_proxy_auth_failed"
		message = "Provider proxy authentication failed"
	case "timeout":
		status = http.StatusGatewayTimeout
		code = "provider_proxy_timeout"
		message = "Provider proxy connection timed out"
	default:
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
			status = http.StatusGatewayTimeout
			code = "provider_proxy_timeout"
			message = "Provider proxy connection timed out"
		}
	}
	return &ProviderInvocationError{
		Err:         NewHTTPError(status, code, message),
		Disposition: ProviderErrorEgress,
	}
}
