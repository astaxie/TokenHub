package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	gatewaySettingsID                 = "cfg_gateway"
	syntheticDNSEnabledField          = "provider_synthetic_dns_enabled"
	syntheticDNSCIDRsField            = "provider_synthetic_dns_cidrs"
	syntheticDNSAllowPrivateField     = "provider_synthetic_dns_allow_private_ranges"
	defaultSyntheticDNSCIDRs          = "198.18.0.0/15"
	syntheticDNSPolicyRefreshInterval = 5 * time.Second
	syntheticDNSPolicyRefreshTimeout  = 2 * time.Second
	maxProviderSyntheticDNSCIDRCount  = 16
)

// providerSyntheticDNSPolicy is the deliberately narrow exception for proxy
// clients that replace real DNS answers with synthetic addresses. It applies
// only after a hostname lookup; literal-IP provider URLs never consult it.
// The short cache avoids a database read for every new TCP connection while
// still converging settings across replicas without a restart.
type providerSyntheticDNSPolicy struct {
	store Store

	mu          sync.Mutex
	loadedAt    time.Time
	snapshot    providerSyntheticDNSSnapshot
	refreshDone chan struct{}
	revision    uint64

	transportsMu sync.Mutex
	transports   []*providerUpstreamTransportPool
}

// providerSyntheticDNSSnapshot is immutable after construction. Each
// transport generation captures one snapshot so a settings change cannot
// alter the SSRF decision of a request that already selected that generation.
type providerSyntheticDNSSnapshot struct {
	blocks             []*net.IPNet
	allowPrivateRanges bool
}

func (snapshot providerSyntheticDNSSnapshot) allowsResolvedIPContext(_ context.Context, ip net.IP) bool {
	if ip == nil || isNonBypassableProviderSyntheticDNSIP(ip) {
		return false
	}
	if ip.IsPrivate() && !snapshot.allowPrivateRanges {
		return false
	}
	for _, block := range snapshot.blocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func newProviderSyntheticDNSPolicy(store Store) *providerSyntheticDNSPolicy {
	policy := &providerSyntheticDNSPolicy{store: store}
	policy.refresh(context.Background())
	return policy
}

func (policy *providerSyntheticDNSPolicy) allowsResolvedIP(ip net.IP) bool {
	return policy.allowsResolvedIPContext(context.Background(), ip)
}

func (policy *providerSyntheticDNSPolicy) allowsResolvedIPContext(ctx context.Context, ip net.IP) bool {
	if policy == nil {
		return false
	}
	return policy.configuredSnapshot(ctx).allowsResolvedIPContext(ctx, ip)
}

type providerSyntheticDNSContextStore interface {
	ListResourcesContext(context.Context, string) ([]AdminResource, error)
}

func (policy *providerSyntheticDNSPolicy) configuredSnapshot(ctx context.Context) providerSyntheticDNSSnapshot {
	if policy == nil || policy.store == nil {
		return providerSyntheticDNSSnapshot{}
	}
	policy.mu.Lock()
	if !policy.loadedAt.IsZero() && time.Since(policy.loadedAt) < syntheticDNSPolicyRefreshInterval {
		snapshot := policy.snapshot
		policy.mu.Unlock()
		return snapshot
	}
	if policy.refreshDone != nil {
		done := policy.refreshDone
		policy.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
		}
		policy.mu.Lock()
		snapshot := policy.snapshot
		policy.mu.Unlock()
		return snapshot
	}
	policy.refreshDone = make(chan struct{})
	done := policy.refreshDone
	revision := policy.revision
	policy.mu.Unlock()

	refreshCtx, cancel := context.WithTimeout(ctx, syntheticDNSPolicyRefreshTimeout)
	defer cancel()
	settings, err := policy.listSettings(refreshCtx)
	var snapshot providerSyntheticDNSSnapshot
	if err == nil {
		snapshot = providerSyntheticDNSSnapshotFromSettings(settings)
	}

	policy.mu.Lock()
	changed := false
	staleRefresh := policy.revision != revision
	if err == nil && !staleRefresh {
		changed = !providerSyntheticDNSSnapshotsEqual(policy.snapshot, snapshot)
		policy.snapshot = snapshot
	} else if err != nil && !staleRefresh {
		log.Printf("[tokenhub] failed to refresh Provider synthetic DNS settings: %v", err)
	}
	if !staleRefresh {
		policy.loadedAt = time.Now()
	}
	policy.refreshDone = nil
	close(done)
	current := policy.snapshot
	policy.mu.Unlock()
	if changed {
		policy.rotateTransports(current)
	}
	return current
}

func (policy *providerSyntheticDNSPolicy) refresh(ctx context.Context) {
	if policy == nil {
		return
	}
	_ = policy.configuredSnapshot(ctx)
}

func (policy *providerSyntheticDNSPolicy) listSettings(ctx context.Context) ([]AdminResource, error) {
	if store, ok := policy.store.(providerSyntheticDNSContextStore); ok {
		return store.ListResourcesContext(ctx, "settings")
	}
	return policy.store.ListResources("settings"), nil
}

func providerSyntheticDNSSnapshotFromSettings(settings []AdminResource) providerSyntheticDNSSnapshot {
	for _, setting := range settings {
		if setting.ID != gatewaySettingsID || setting.Status != StatusActive || !truthyField(setting.Fields, syntheticDNSEnabledField) {
			continue
		}
		allowPrivate := truthyField(setting.Fields, syntheticDNSAllowPrivateField)
		blocks, err := parseProviderSyntheticDNSCIDRs(stringField(setting.Fields, syntheticDNSCIDRsField), allowPrivate)
		if err == nil {
			return providerSyntheticDNSSnapshot{blocks: blocks, allowPrivateRanges: allowPrivate}
		}
		return providerSyntheticDNSSnapshot{}
	}
	return providerSyntheticDNSSnapshot{}
}

func (policy *providerSyntheticDNSPolicy) applySetting(setting *AdminResource) {
	if policy == nil {
		return
	}
	var snapshot providerSyntheticDNSSnapshot
	if setting != nil {
		snapshot = providerSyntheticDNSSnapshotFromSettings([]AdminResource{*setting})
	}
	policy.mu.Lock()
	changed := !providerSyntheticDNSSnapshotsEqual(policy.snapshot, snapshot)
	policy.revision++
	policy.snapshot = snapshot
	policy.loadedAt = time.Now()
	policy.mu.Unlock()
	if changed {
		policy.rotateTransports(snapshot)
	}
}

func providerSyntheticDNSSnapshotsEqual(left, right providerSyntheticDNSSnapshot) bool {
	if left.allowPrivateRanges != right.allowPrivateRanges || len(left.blocks) != len(right.blocks) {
		return false
	}
	for index := range left.blocks {
		if left.blocks[index].String() != right.blocks[index].String() {
			return false
		}
	}
	return true
}

func (policy *providerSyntheticDNSPolicy) registerTransport(transport *providerUpstreamTransportPool) {
	if policy == nil || transport == nil {
		return
	}
	policy.transportsMu.Lock()
	policy.transports = append(policy.transports, transport)
	policy.transportsMu.Unlock()
}

func (policy *providerSyntheticDNSPolicy) rotateTransports(snapshot providerSyntheticDNSSnapshot) {
	if policy == nil {
		return
	}
	policy.transportsMu.Lock()
	transports := append([]*providerUpstreamTransportPool(nil), policy.transports...)
	policy.transportsMu.Unlock()
	for _, transport := range transports {
		transport.rotate(snapshot)
	}
}

func parseProviderSyntheticDNSCIDRs(raw string, allowPrivate bool) ([]*net.IPNet, error) {
	entries := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(entries) == 0 {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_synthetic_dns_cidrs_required", "At least one synthetic DNS CIDR is required when the exception is enabled")
	}
	if len(entries) > maxProviderSyntheticDNSCIDRCount {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_synthetic_dns_cidrs_invalid", fmt.Sprintf("At most %d synthetic DNS CIDRs are allowed", maxProviderSyntheticDNSCIDRCount))
	}
	blocks := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		_, block, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_synthetic_dns_cidrs_invalid", fmt.Sprintf("Invalid synthetic DNS CIDR %q", entry))
		}
		prefix, bits := block.Mask.Size()
		if (bits == 32 && prefix < 8) || (bits == 128 && prefix < 18) {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_synthetic_dns_cidrs_too_broad", fmt.Sprintf("Synthetic DNS CIDR %q is too broad", entry))
		}
		if providerSyntheticDNSCIDRContainsNonBypassableAddress(block) {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_synthetic_dns_cidrs_not_allowed", fmt.Sprintf("Synthetic DNS CIDR %q overlaps a protected address range", entry))
		}
		if !allowPrivate && providerSyntheticDNSCIDRContainsPrivateAddress(block) {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_synthetic_dns_private_cidr_requires_unsafe_mode", fmt.Sprintf("Synthetic DNS CIDR %q overlaps a private address range; enable unsafe private-range trust explicitly", entry))
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

var providerSyntheticDNSPrivateCIDRs = mustProviderUpstreamCIDRs([]string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"fc00::/7",
})

func providerSyntheticDNSCIDRContainsPrivateAddress(candidate *net.IPNet) bool {
	if candidate == nil {
		return true
	}
	for _, private := range providerSyntheticDNSPrivateCIDRs {
		if candidate.Contains(private.IP) || private.Contains(candidate.IP) {
			return true
		}
	}
	return false
}

// These ranges remain denied even when a synthetic-DNS exception is enabled.
// Private/ULA ranges are gated separately by the explicit high-risk trust
// setting. Benchmarking and documentation ranges are intentionally not here:
// proxy products can use them as configurable synthetic pools. The exception
// is still constrained to hostname resolution results.
var nonBypassableProviderSyntheticDNSCIDRs = mustProviderUpstreamCIDRs([]string{
	"0.0.0.0/8",
	"127.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"192.0.0.0/24",
	"192.88.99.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"100::/64",
	"100:0:0:1::/64",
	"2001:2::/48",
	"5f00::/16",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"fe80::/10",
	"ff00::/8",
	"fec0::/10",
	"fd00:ec2::254/128",
})

func mustProviderUpstreamCIDRs(cidrs []string) []*net.IPNet {
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func isNonBypassableProviderSyntheticDNSIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if _, translated := providerUpstreamEmbeddedNAT64IPv4(ip); translated || nat64LocalUsePrefix.Contains(ip) {
		return true
	}
	for _, block := range nonBypassableProviderSyntheticDNSCIDRs {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

func providerSyntheticDNSCIDRContainsNonBypassableAddress(candidate *net.IPNet) bool {
	if candidate == nil {
		return true
	}
	for _, protected := range nonBypassableProviderSyntheticDNSCIDRs {
		if candidate.Contains(protected.IP) || protected.Contains(candidate.IP) {
			return true
		}
	}
	if protected, _ := configuredProviderUpstreamNAT64Prefix(); protected != nil && (candidate.Contains(protected.IP) || protected.Contains(candidate.IP)) {
		return true
	}
	return false
}

func validateProviderSyntheticDNSSettings(resource AdminResource) error {
	if resource.ID != "" && resource.ID != gatewaySettingsID {
		return nil
	}
	if !truthyField(resource.Fields, syntheticDNSEnabledField) {
		return nil
	}
	allowPrivate := truthyField(resource.Fields, syntheticDNSAllowPrivateField)
	_, err := parseProviderSyntheticDNSCIDRs(stringField(resource.Fields, syntheticDNSCIDRsField), allowPrivate)
	return err
}

func ensureProviderSyntheticDNSSettings(store Store) error {
	settings, err := store.ListResourcesChecked("settings")
	if err != nil {
		return fmt.Errorf("read Provider synthetic DNS settings for backfill: %w", err)
	}
	for _, setting := range settings {
		if setting.ID != gatewaySettingsID {
			continue
		}
		if setting.Fields == nil {
			setting.Fields = map[string]any{}
		}
		changed := false
		if _, ok := setting.Fields[syntheticDNSEnabledField]; !ok {
			setting.Fields[syntheticDNSEnabledField] = false
			changed = true
		}
		if _, ok := setting.Fields[syntheticDNSCIDRsField]; !ok {
			setting.Fields[syntheticDNSCIDRsField] = defaultSyntheticDNSCIDRs
			changed = true
		}
		if _, ok := setting.Fields[syntheticDNSAllowPrivateField]; !ok {
			setting.Fields[syntheticDNSAllowPrivateField] = false
			changed = true
		}
		if changed {
			if _, err := store.UpdateResource("settings", setting.ID, setting); err != nil {
				return fmt.Errorf("backfill Provider synthetic DNS settings: %w", err)
			}
		}
		return nil
	}
	return nil
}
