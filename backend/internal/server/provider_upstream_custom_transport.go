package server

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Injected ordinary HTTP clients need the same validated dial path as the
// production pool. Preserve custom TLS settings and test dialers, but never
// let a second DNS lookup replace HTTP preflight's pinned local addresses.
func guardCustomProviderTransport(next http.RoundTripper, allowed []*net.IPNet) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	base, ok := next.(*http.Transport)
	if !ok {
		// Non-network RoundTrippers are explicit caller-provided implementations.
		return guardProviderUpstreamRequests(next, allowed)
	}
	guarded := base.Clone()
	proxy := guarded.Proxy
	guarded.Proxy = nil
	dial := guarded.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	guarded.DialTLS = nil
	guarded.DialTLSContext = nil
	guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialGuardedUpstream(ctx, network, address, allowed, nil, 30*time.Second, net.DefaultResolver.LookupIPAddr, dial)
	}
	direct := guardProviderUpstreamRequests(guarded, allowed)
	if proxy == nil {
		return direct
	}
	return providerTransportWithProxy(direct, func(transport *http.Transport) {
		transport.TLSClientConfig = guarded.TLSClientConfig
	}, proxy)
}
