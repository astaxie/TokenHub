package server

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Auto mode supports administrator-configured self-hosted services. Strict
// mode retains the legacy literal-only exceptions. Unknown modes fail closed.
func providerUpstreamAutoAccess() bool {
	mode := strings.ToLower(strings.TrimSpace(getenv("TOKENHUB_PROVIDER_UPSTREAM_ACCESS_MODE", "auto")))
	return mode == "" || mode == "auto"
}

func providerUpstreamLocalIPAllowed(ip net.IP, allowedPrivate []*net.IPNet) bool {
	if ip.IsLoopback() {
		return providerUpstreamLoopbackAllowed()
	}
	return isAllowlistedPrivateProviderUpstreamIP(ip, allowedPrivate)
}

func providerUpstreamResolvedIPAllowed(ctx context.Context, ip net.IP, allowedPrivate []*net.IPNet, syntheticDNS providerSyntheticDNSResolver) bool {
	return !isDisallowedProviderUpstreamIP(ip) ||
		providerUpstreamAutoAccess() && providerUpstreamLocalIPAllowed(ip, allowedPrivate) ||
		syntheticDNS != nil && syntheticDNS.allowsResolvedIPContext(ctx, ip)
}

func providerPinnedIPAllowed(ctx context.Context, host string, ip net.IP, allowedPrivate []*net.IPNet, syntheticDNS providerSyntheticDNSResolver) bool {
	if isLocalProviderHostname(host) {
		return providerUpstreamLoopbackAllowed() && ip.IsLoopback()
	}
	if literal := net.ParseIP(host); literal != nil {
		return literal.Equal(ip) && checkProviderUpstreamLiteralDial(ip, allowedPrivate) == nil
	}
	return providerUpstreamResolvedIPAllowed(ctx, ip, allowedPrivate, syntheticDNS)
}

type providerPinnedTargetsKey struct{}

type providerPinnedTargets struct {
	host string
	ips  []net.IP
}

func pinProviderDirectTargets(request *http.Request, ips []net.IP) *http.Request {
	targets := providerPinnedTargets{host: request.URL.Hostname(), ips: ips}
	return request.WithContext(context.WithValue(request.Context(), providerPinnedTargetsKey{}, targets))
}

func providerRequestTargets(ctx context.Context, host string) []net.IP {
	if targets, ok := ctx.Value(providerPinnedTargetsKey{}).(providerPinnedTargets); ok && strings.EqualFold(targets.host, host) {
		return targets.ips
	}
	return nil
}

func providerTargetsAreLocal(ips []net.IP, allowedPrivate []*net.IPNet) bool {
	if len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !providerUpstreamLocalIPAllowed(ip, allowedPrivate) {
			return false
		}
	}
	return true
}

func providerTargetsAreSynthetic(ctx context.Context, ips []net.IP, policy providerSyntheticDNSResolver) bool {
	if policy != nil {
		for _, ip := range ips {
			if policy.allowsResolvedIPContext(ctx, ip) {
				return true
			}
		}
	}
	return false
}

func providerHTTPHostname(endpoint *url.URL) bool {
	return strings.EqualFold(endpoint.Scheme, "http") && net.ParseIP(endpoint.Hostname()) == nil && !isLocalProviderHostname(endpoint.Hostname())
}

// Resolve plaintext hostnames before any credentials or body can be sent.
// Every answer must be an authorized local address; mixed public/private DNS
// answers cannot send plaintext credentials to a public fallback. The direct
// dialer and proxy use these same answers without resolving the hostname again.
func prepareProviderHTTPRequest(request *http.Request, allowedPrivate []*net.IPNet, lookup upstreamLookupFunc, policies ...providerSyntheticDNSResolver) (*http.Request, error) {
	if !providerHTTPHostname(request.URL) {
		return request, nil
	}
	ips := providerRequestTargets(request.Context(), request.URL.Hostname())
	if len(ips) == 0 {
		ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		defer cancel()
		addresses, err := lookup(ctx, request.URL.Hostname())
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			ips = append(ips, address.IP)
		}
	}
	for _, policy := range policies {
		if providerTargetsAreSynthetic(request.Context(), ips, policy) {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_base_url_insecure_scheme", "Synthetic DNS provider hostnames require HTTPS")
		}
	}
	if !providerUpstreamAutoAccess() || !providerTargetsAreLocal(ips, allowedPrivate) {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_base_url_insecure_scheme", "HTTP provider hostnames must resolve only to permitted local addresses; public endpoints require HTTPS")
	}
	return pinProviderDirectTargets(request, ips), nil
}
