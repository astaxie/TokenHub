package server

import (
	"context"
	"net"
	"net/http"
	"sync"
)

// providerUpstreamTransportPool swaps the underlying connection pool whenever
// the synthetic-DNS policy changes. New requests immediately use the new pool;
// requests already in flight keep their original transport and may finish
// normally. Closing the retired pool's idle connections prevents a revoked
// synthetic range from being reused by a later request.
type providerUpstreamTransportPool struct {
	mu      sync.RWMutex
	current *http.Transport
	factory func(providerSyntheticDNSSnapshot) *http.Transport
	policy  *providerSyntheticDNSPolicy
}

func newProviderUpstreamTransportPool(policy *providerSyntheticDNSPolicy, factory func(providerSyntheticDNSSnapshot) *http.Transport) *providerUpstreamTransportPool {
	snapshot := policy.configuredSnapshot(context.Background())
	pool := &providerUpstreamTransportPool{
		current: factory(snapshot),
		factory: factory,
		policy:  policy,
	}
	policy.registerTransport(pool)
	return pool
}

func (pool *providerUpstreamTransportPool) RoundTrip(request *http.Request) (*http.Response, error) {
	if pool.policy != nil && request != nil {
		pool.policy.refresh(request.Context())
	}
	pool.mu.RLock()
	transport := pool.current
	pool.mu.RUnlock()
	return transport.RoundTrip(request)
}

func (pool *providerUpstreamTransportPool) CloseIdleConnections() {
	pool.mu.RLock()
	transport := pool.current
	pool.mu.RUnlock()
	transport.CloseIdleConnections()
}

func (pool *providerUpstreamTransportPool) rotate(snapshot providerSyntheticDNSSnapshot) {
	if pool == nil || pool.factory == nil {
		return
	}
	replacement := pool.factory(snapshot)
	pool.mu.Lock()
	retired := pool.current
	pool.current = replacement
	pool.mu.Unlock()
	if retired != nil {
		retired.CloseIdleConnections()
	}
}

func rotatingProviderUpstreamTransport(allowedPrivate []*net.IPNet, policy *providerSyntheticDNSPolicy, configure func(*http.Transport)) http.RoundTripper {
	factory := func(snapshot providerSyntheticDNSSnapshot) *http.Transport {
		transport := ssrfGuardedProviderTransport(allowedPrivate, snapshot)
		if configure != nil {
			configure(transport)
		}
		return transport
	}
	if policy == nil {
		return factory(providerSyntheticDNSSnapshot{})
	}
	return newProviderUpstreamTransportPool(policy, factory)
}
