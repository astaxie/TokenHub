package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// validateProviderUpstreamBaseURL guards against server-side request forgery
// (SSRF) when the gateway calls an administrator-supplied custom provider base
// URL. Public endpoints must use HTTPS before credentials are attached; HTTP
// is reserved for explicitly allowed loopback/private literal endpoints. The
// guard also forbids credentials embedded in the URL and rejects literal IP
// hosts in loopback, private, link-local and curated
// high-risk/non-provider ranges (including cloud metadata). Hostnames receive
// the same address policy after resolution at dial time.
//
// Trade-off: loopback/localhost is allowed only when the operator explicitly
// opts in with TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK. The default remains
// closed because a compromised administrator session must not be able to turn
// an arbitrary provider into a loopback SSRF primitive. Operators that need to
// reach providers on a private network list the trusted ranges in
// TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS and use literal IPs from those
// ranges; the allowlist only ever applies to literal IPs an administrator
// typed, never to resolved hostnames (DNS rebinding) or redirect targets.
// Redirect re-validation passes nil for allowedPrivate and false for
// allowLocalhost: a public URL must not be able to bounce requests into the
// internal network or onto a loopback service, even when the operator
// allowlists private ranges or runs a local provider.
func validateProviderUpstreamBaseURL(endpoint *url.URL, allowedPrivate []*net.IPNet, allowLocalhost bool) error {
	if endpoint == nil {
		return NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	scheme := strings.ToLower(strings.TrimSpace(endpoint.Scheme))
	if scheme != "https" && scheme != "http" {
		return NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid_scheme", "Base URL must use http or https")
	}
	if endpoint.User != nil {
		return NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL must not embed credentials")
	}
	host := endpoint.Hostname()
	if host == "" {
		return NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	plaintextAllowed := false
	if isLocalProviderHostname(host) {
		if !allowLocalhost {
			return NewHTTPError(http.StatusBadRequest, "provider_base_url_not_allowed", "Base URL host must not be a loopback address")
		}
		plaintextAllowed = true
	} else if ip := net.ParseIP(host); ip != nil {
		if isAllowlistedPrivateProviderUpstreamIP(ip, allowedPrivate) {
			plaintextAllowed = true
		} else if isDisallowedProviderUpstreamIP(ip) {
			return NewHTTPError(http.StatusBadRequest, "provider_base_url_not_allowed", "Base URL host must not be a private, loopback, or link-local address")
		}
	}
	if scheme == "http" && !plaintextAllowed {
		return NewHTTPError(http.StatusBadRequest, "provider_base_url_insecure_scheme", "Public provider base URLs must use https")
	}
	return nil
}

// validateProviderUpstreamBaseURLString parses raw and applies
// validateProviderUpstreamBaseURL. An empty URL passes: adapters fall back to
// their built-in default endpoint when none is configured, and that default is
// validated wherever it is actually used.
func validateProviderUpstreamBaseURLString(raw string, allowedPrivate []*net.IPNet, allowLocalhost bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	return validateProviderUpstreamBaseURL(endpoint, allowedPrivate, allowLocalhost)
}

// allowedProviderUpstreamCIDRs parses TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS
// (comma/semicolon/whitespace-separated CIDRs). The allowlist only relaxes the
// RFC1918/ULA private-range check for literal IPs; loopback, link-local
// (cloud metadata), CGNAT and other curated high-risk/non-provider ranges stay
// rejected
// regardless of the configured ranges. Invalid entries are skipped so a typo
// cannot accidentally widen or disable validation.
// ValidateProviderUpstreamBaseURL applies the save-time provider base URL
// guard with the process-wide allowlist and the localhost exception, for
// callers that persist providers outside the admin HTTP path (for example
// migration sinks writing directly to the store).
func ValidateProviderUpstreamBaseURL(raw string) error {
	return validateProviderUpstreamBaseURLString(raw, allowedProviderUpstreamCIDRs(), providerUpstreamLoopbackAllowed())
}

func providerUpstreamLoopbackAllowed() bool {
	return getenvBool("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", false)
}

func allowedProviderUpstreamCIDRs() []*net.IPNet {
	entries := getenvList("TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS")
	blocks := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		if _, block, err := net.ParseCIDR(entry); err == nil {
			blocks = append(blocks, block)
		} else {
			log.Printf("[tokenhub] ignoring invalid TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS entry %q: %v", entry, err)
		}
	}
	return blocks
}

// isAllowlistedPrivateProviderUpstreamIP reports whether ip is an RFC1918/ULA
// private address inside the operator-configured allowlist. Only private
// addresses qualify: loopback, link-local (metadata), unspecified and
// curated high-risk/non-provider ranges can never be allowlisted.
func isAllowlistedPrivateProviderUpstreamIP(ip net.IP, allowedPrivate []*net.IPNet) bool {
	if isAlwaysDeniedProviderUpstreamIP(ip) {
		return false
	}
	if _, translated := providerUpstreamEmbeddedNAT64IPv4(ip); translated {
		return false
	}
	if nat64LocalUsePrefix.Contains(ip) {
		return false
	}
	if !ip.IsPrivate() {
		return false
	}
	for _, block := range allowedPrivate {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// isLocalProviderHostname reports whether the host is an explicit loopback
// identifier that the project intentionally permits for local/self-hosted
// providers (matching the localhost exceptions used for Codex endpoints).
func isLocalProviderHostname(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// disallowedProviderUpstreamCIDRs lists curated ranges that net.IP's
// private/loopback/link-local predicates do not cover and that are unsafe or
// never meaningful provider endpoints. This is intentionally a trust-boundary
// policy, not an exhaustive mirror of the IANA special-purpose registries.
// It covers this-host (0.0.0.0/8), the shared/CGNAT
// range (100.64.0.0/10, which hosts Alibaba Cloud's metadata endpoint
// 100.100.100.200), the IPv4/IPv6 documentation ranges (192.0.2.0/24,
// 198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32), the benchmarking range
// (198.18.0.0/15), the reserved range (240.0.0.0/4, which includes the
// limited-broadcast address), the deprecated 6to4 relay-anycast allocation,
// the deprecated IPv6 site-local range (fec0::/10, still routable inside
// sites that kept it), and AWS's IPv6 instance-metadata endpoint. NAT64 ranges
// are handled separately so a
// configured RFC 6052 prefix can distinguish public from private embedded
// IPv4 targets. IPv4-mapped IPv6 spellings need no entry: To4-based
// predicates already classify ::ffff:10.x as private.
var disallowedProviderUpstreamCIDRs = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",
		"100.64.0.0/10",
		"192.0.0.0/29",
		"192.0.0.8/32",
		"192.0.0.170/31",
		"192.0.2.0/24",
		"192.88.99.0/24",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4",
		"100::/64",
		"100:0:0:1::/64",
		"2001:2::/48",
		"2001:db8::/32",
		"3fff::/20",
		"5f00::/16",
		"fec0::/10",
		"fd00:ec2::254/128",
	}
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		if _, block, err := net.ParseCIDR(cidr); err == nil {
			blocks = append(blocks, block)
		}
	}
	return blocks
}()

func isAlwaysDeniedProviderUpstreamIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, block := range disallowedProviderUpstreamCIDRs {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// isDisallowedProviderUpstreamIP applies the non-bypassable special-use
// checks first, then classifies an RFC 6052 NAT64 address by its embedded IPv4
// target. Addresses in the RFC 8215 local-use allocation remain denied unless
// an operator configures the actual translation prefix.
func isDisallowedProviderUpstreamIP(ip net.IP) bool {
	if isAlwaysDeniedProviderUpstreamIP(ip) {
		return true
	}
	if embedded, translated := providerUpstreamEmbeddedNAT64IPv4(ip); translated {
		return embedded == nil || isDisallowedProviderUpstreamIP(embedded)
	}
	if nat64LocalUsePrefix.Contains(ip) {
		return true
	}
	return ip.IsPrivate()
}

// errProviderUpstreamDialDisallowed is returned by the dial guard when a
// literal or resolved address must not be connected to.
var errProviderUpstreamDialDisallowed = errors.New("provider base URL resolved to a disallowed address")

// checkProviderUpstreamLiteralDial decides whether a literal-IP host may be
// dialed: operator-allowlisted private ranges pass, disallowed special-use
// addresses fail, and anything else (public unicast) passes. Loopback
// identifiers are handled by isLocalProviderHostname before this runs, and
// hostname resolution results are filtered separately after LookupIPAddr.
func checkProviderUpstreamLiteralDial(ip net.IP, allowedPrivate []*net.IPNet) error {
	if isAllowlistedPrivateProviderUpstreamIP(ip, allowedPrivate) {
		return nil
	}
	if isDisallowedProviderUpstreamIP(ip) {
		return errProviderUpstreamDialDisallowed
	}
	return nil
}

// ssrfGuardedProviderClient returns an http.Client whose requests are guarded
// against the two SSRF bypasses that validateProviderUpstreamBaseURL alone
// cannot close:
//
//  1. DNS rebinding / hostname-to-private resolution: a hostname that passes
//     the literal-IP check can still resolve to a private or link-local
//     address. Closing this requires inspecting the resolved IPs at dial time.
//  2. Redirect following: http clients follow 3xx by default, so a public URL
//     that returns a redirect to a private target would bypass validation.
//
// When the caller passes nil or http.DefaultClient, we build a dedicated
// Transport whose DialContext resolves the
// hostname, drops any private or link-local candidates, and refuses to connect
// when none are allowed; otherwise it dials one of the validated addresses
// directly, so the checked IP is the one actually connected to. When the
// caller passes a custom client (used by tests with in-process httptest
// servers) we keep its Transport as the underlying sender so it keeps working,
// while still applying request and redirect validation. This keeps the dial
// guard strictly on the production path without breaking custom transports.
//
// In both cases the CheckRedirect hook re-runs validateProviderUpstreamBaseURL
// on every redirect target so a hop to a private or non-http(s) URL is refused.
func ssrfGuardedProviderClient(client *http.Client) *http.Client {
	allowedPrivate := allowedProviderUpstreamCIDRs()
	guard := &http.Client{
		// Re-validate every redirect target before following it, with no
		// allowlist and no localhost exception.
		CheckRedirect: strictProviderUpstreamRedirect,
	}
	if client == nil || client == http.DefaultClient {
		guard.Transport = guardProviderUpstreamRequests(ssrfGuardedProviderTransport(allowedPrivate), allowedPrivate)
		return guard
	}
	// Custom client (tests): preserve its Transport and timeouts underneath the
	// request and redirect guards.
	guard.Transport = guardProviderUpstreamRequests(client.Transport, allowedPrivate)
	guard.Timeout = client.Timeout
	guard.Jar = client.Jar
	return guard
}

// upstreamLookupFunc resolves a host, matching net.Resolver.LookupIPAddr.
type upstreamLookupFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// upstreamDialFunc dials an address, matching net.Dialer.DialContext.
type upstreamDialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type providerSyntheticDNSResolver interface {
	allowsResolvedIPContext(context.Context, net.IP) bool
}

// dialGuardedUpstream dials addr while enforcing the SSRF classification:
// loopback identifiers pass only after explicit operator opt-in, literal IPs
// are checked directly, and hostnames resolve to validated dial candidates,
// so the checked IP is the one actually connected to (DNS rebinding cannot
// slip a private address past the save-time check).
//
// Resolution and the whole fallback sequence share one budget, created before
// the lookup, so a stalled resolver cannot hold a streaming request (which
// carries no total deadline, and ResponseHeaderTimeout starts only after
// dialing) hostage. Inside the loop every candidate's slice is recomputed
// from the time actually remaining on that budget and the candidates left:
// a slow lookup followed by a silently black-holing first address must not
// starve the healthy candidates behind it (the Happy Eyeballs fallback the
// default transport would have provided).
func dialGuardedUpstream(ctx context.Context, network string, addr string, allowedPrivate []*net.IPNet, syntheticDNS providerSyntheticDNSResolver, budget time.Duration, lookup upstreamLookupFunc, dial upstreamDialFunc) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// Loopback is disabled by default and requires an explicit operator opt-in.
	if isLocalProviderHostname(host) {
		if providerUpstreamLoopbackAllowed() {
			return dial(ctx, network, addr)
		}
		return nil, errProviderUpstreamDialDisallowed
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := checkProviderUpstreamLiteralDial(ip, allowedPrivate); err != nil {
			return nil, err
		}
		return dial(ctx, network, addr)
	}
	dialCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	ips, err := lookup(dialCtx, host)
	if err != nil {
		return nil, err
	}
	var allowed []net.IPAddr
	for _, ip := range ips {
		if !isDisallowedProviderUpstreamIP(ip.IP) || syntheticDNS != nil && syntheticDNS.allowsResolvedIPContext(dialCtx, ip.IP) {
			allowed = append(allowed, ip)
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("provider base URL host %q resolves only to disallowed addresses", host)
	}
	return raceValidatedUpstreamCandidates(dialCtx, network, port, allowed, dial)
}

// upstreamDialFallbackDelay mirrors the default transport's Happy Eyeballs
// stagger: the next validated candidate starts racing after this delay (or
// immediately once an earlier candidate has failed), so one silently
// black-holing address — an unreachable AAAA record is the common case —
// costs hundreds of milliseconds, not a share of the whole dial budget.
const upstreamDialFallbackDelay = 300 * time.Millisecond

// errUpstreamDialRaceLost marks an attempt that connected after another
// validated candidate had already won the race; its connection is closed
// instead of being handed out.
var errUpstreamDialRaceLost = errors.New("superseded by a faster validated address")

// raceValidatedUpstreamCandidates dials validated addresses with staggered
// parallel attempts inside the shared budget: the first candidate starts
// immediately, later ones join after the fallback delay or as soon as an
// earlier attempt fails, and the first success wins while the rest are
// cancelled. Only validated addresses are ever dialed, preserving the
// dial-what-was-checked guarantee. The winner flag is what keeps losing
// attempts leak-free: a goroutine that connects after the race was decided
// closes its own connection, so near-simultaneous successes cannot strand
// sockets in the buffered results channel.
func raceValidatedUpstreamCandidates(ctx context.Context, network, port string, allowed []net.IPAddr, dial upstreamDialFunc) (net.Conn, error) {
	type dialOutcome struct {
		conn net.Conn
		err  error
	}
	results := make(chan dialOutcome, len(allowed))
	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()
	var winnerMu sync.Mutex
	won := false
	stagger := time.NewTimer(0)
	defer stagger.Stop()

	// drain collects the outcomes of attempts still in flight after the race
	// is abandoned (caller cancelled or budget exhausted) and closes any
	// connection they managed to establish. A success pushed to the buffered
	// channel right as the outer select takes the cancellation branch would
	// otherwise be stranded with nobody left to close it. The channel is
	// buffered for every attempt, so in-flight goroutines never block sending.
	drain := func(pending int) {
		go func() {
			for ; pending > 0; pending-- {
				if outcome := <-results; outcome.conn != nil {
					_ = outcome.conn.Close()
				}
			}
		}()
	}

	launched := 0
	pending := 0
	launchNow := true
	var lastErr error
	for launched < len(allowed) {
		if !launchNow {
			select {
			case outcome := <-results:
				pending--
				if outcome.err == nil {
					return outcome.conn, nil
				}
				lastErr = outcome.err
				launchNow = true // a failure falls back immediately
				continue
			case <-stagger.C:
			case <-ctx.Done():
				drain(pending)
				if lastErr != nil {
					return nil, lastErr
				}
				return nil, ctx.Err()
			}
		}
		launchNow = false
		candidate := allowed[launched]
		launched++
		pending++
		go func() {
			conn, err := dial(raceCtx, network, net.JoinHostPort(candidate.IP.String(), port))
			winnerMu.Lock()
			switch {
			case err != nil:
				// A plain failure: nothing to close.
			case won:
				// Another candidate already won; close rather than leak.
				err = errUpstreamDialRaceLost
				_ = conn.Close()
				conn = nil
			default:
				won = true
			}
			winnerMu.Unlock()
			results <- dialOutcome{conn: conn, err: err}
		}()
		stagger.Reset(upstreamDialFallbackDelay)
	}
	for pending > 0 {
		select {
		case outcome := <-results:
			pending--
			if outcome.err == nil {
				return outcome.conn, nil
			}
			lastErr = outcome.err
		case <-ctx.Done():
			drain(pending)
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// ssrfGuardedProviderTransport builds a Transport whose DialContext resolves
// the destination hostname and refuses to connect when every resolved IP is a
// disallowed (private/loopback/link-local/unspecified/special-use) address,
// mirroring the localhost exception from validateProviderUpstreamBaseURL. Each
// validated candidate is dialed in turn, so the checked IP is the one actually
// connected to and a single unreachable address does not fail the request.
//
// The private-range allowlist only applies to literal IPs (allowedPrivate).
// Hostname results remain denied by default and may pass only through the
// separately configured synthetic-DNS policy, which is intended for Fake-IP
// pools intercepted by a transparent proxy.
//
// Proxying is disabled on purpose: with an HTTP(S)_PROXY configured, DialContext
// would receive the proxy address and the guard would validate the proxy rather
// than the request target, letting the proxy's own DNS resolution bypass the
// check. Provider catalog fetches therefore always connect directly.
//
// The idle pool is sized for a gateway workload: unlike a general-purpose
// client, this transport fans a large amount of concurrent traffic out over a
// handful of upstream hosts. The standard library's default of two idle
// connections per host would retire every connection above that as soon as it
// goes idle, so a busy host pays a fresh TCP and TLS handshake on nearly every
// request. These limits cap how many idle connections are kept for reuse, not
// how many requests may run at once; MaxConnsPerHost stays unset so the guard
// never throttles concurrency. IdleConnTimeout stays at the standard 90
// seconds — including on the fallback path, where a zero value would otherwise
// keep the whole enlarged pool open forever — so connections to a host that
// falls quiet are still released.
func ssrfGuardedProviderTransport(allowedPrivate []*net.IPNet, syntheticDNS ...providerSyntheticDNSResolver) *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	var transport *http.Transport
	if ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{IdleConnTimeout: 90 * time.Second}
	}
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = 64
	transport.MaxIdleConns = 256
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	resolver := net.DefaultResolver
	var syntheticDNSPolicy providerSyntheticDNSResolver
	if len(syntheticDNS) > 0 {
		syntheticDNSPolicy = syntheticDNS[0]
	}
	transport.DialContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
		return dialGuardedUpstream(ctx, network, addr, allowedPrivate, syntheticDNSPolicy, dialer.Timeout, resolver.LookupIPAddr, dialer.DialContext)
	}
	return transport
}
