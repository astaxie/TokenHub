package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticDNSSettings(enabled bool, cidrs string) AdminResource {
	return AdminResource{
		ID:     gatewaySettingsID,
		Name:   "Gateway Base Settings",
		Status: StatusActive,
		Fields: map[string]any{
			syntheticDNSEnabledField:      enabled,
			syntheticDNSCIDRsField:        cidrs,
			syntheticDNSAllowPrivateField: false,
		},
	}
}

func syntheticDNSSettingsWithPrivateTrust(enabled bool, cidrs string) AdminResource {
	setting := syntheticDNSSettings(enabled, cidrs)
	setting.Fields[syntheticDNSAllowPrivateField] = true
	return setting
}

func TestProviderSyntheticDNSPolicyAllowsOnlyConfiguredResolvedAddresses(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", syntheticDNSSettings(true, "198.18.0.0/15"))
	policy := newProviderSyntheticDNSPolicy(store)

	for _, candidate := range []string{"198.18.0.1", "198.19.255.254"} {
		if !policy.allowsResolvedIP(net.ParseIP(candidate)) {
			t.Fatalf("expected configured synthetic address %s to pass", candidate)
		}
	}
	for _, candidate := range []string{"10.0.0.1", "fc00::1", "169.254.169.254", "127.0.0.1", "64:ff9b::a9fe:a9fe"} {
		if policy.allowsResolvedIP(net.ParseIP(candidate)) {
			t.Fatalf("expected unconfigured or protected address %s to stay blocked", candidate)
		}
	}
	if err := checkProviderUpstreamLiteralDial(net.ParseIP("198.18.0.1"), nil); !errors.Is(err, errProviderUpstreamDialDisallowed) {
		t.Fatalf("expected literal synthetic IP to stay rejected, got %v", err)
	}
}

func TestProviderSyntheticDNSPrivateRangesRequireUnsafeTrust(t *testing.T) {
	for _, cidr := range []string{"10.0.0.0/8", "fc00::/18"} {
		err := validateProviderSyntheticDNSSettings(syntheticDNSSettings(true, cidr))
		if code := AsHTTPError(err).Code; code != "provider_synthetic_dns_private_cidr_requires_unsafe_mode" {
			t.Fatalf("expected %q to require unsafe private-range trust, got %s (%v)", cidr, code, err)
		}
		if err := validateProviderSyntheticDNSSettings(syntheticDNSSettingsWithPrivateTrust(true, cidr)); err != nil {
			t.Fatalf("expected unsafe private-range trust to accept %q, got %v", cidr, err)
		}
	}

	store := NewMemoryStore()
	setting := store.CreateResource("settings", syntheticDNSSettings(true, "10.0.0.0/8"))
	policy := newProviderSyntheticDNSPolicy(store)
	if policy.allowsResolvedIP(net.ParseIP("10.0.0.1")) {
		t.Fatal("expected ordinary synthetic DNS mode to keep RFC1918 blocked")
	}
	setting.Fields[syntheticDNSAllowPrivateField] = true
	if _, err := store.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}
	policy.applySetting(&setting)
	if !policy.allowsResolvedIP(net.ParseIP("10.0.0.1")) {
		t.Fatal("expected explicitly enabled unsafe private-range trust to allow RFC1918")
	}
}

func TestProviderSyntheticDNSPolicyRefreshesExpiredSnapshotWithoutInvalidation(t *testing.T) {
	store := NewMemoryStore()
	setting := store.CreateResource("settings", syntheticDNSSettings(false, defaultSyntheticDNSCIDRs))
	policy := newProviderSyntheticDNSPolicy(store)
	address := net.ParseIP("198.18.0.1")
	if policy.allowsResolvedIP(address) {
		t.Fatal("expected disabled policy to reject the synthetic address")
	}
	setting.Fields[syntheticDNSEnabledField] = true
	if _, err := store.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}
	policy.mu.Lock()
	policy.loadedAt = time.Now().Add(-syntheticDNSPolicyRefreshInterval)
	policy.mu.Unlock()
	if !policy.allowsResolvedIP(address) {
		t.Fatal("expected an expired snapshot to refresh from the shared store")
	}
}

type timeoutSyntheticDNSStore struct {
	Store
	calls int
}

func (store *timeoutSyntheticDNSStore) ListResourcesContext(ctx context.Context, kind string) ([]AdminResource, error) {
	store.calls++
	if store.calls == 1 {
		return store.Store.ListResources(kind), nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestProviderSyntheticDNSPolicyBoundsSlowRefreshAndKeepsLastSnapshot(t *testing.T) {
	base := NewMemoryStore()
	base.CreateResource("settings", syntheticDNSSettings(true, defaultSyntheticDNSCIDRs))
	store := &timeoutSyntheticDNSStore{Store: base}
	policy := newProviderSyntheticDNSPolicy(store)
	policy.mu.Lock()
	policy.loadedAt = time.Now().Add(-syntheticDNSPolicyRefreshInterval)
	policy.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	if !policy.allowsResolvedIPContext(ctx, net.ParseIP("198.18.0.1")) {
		t.Fatal("expected a failed refresh to retain the last valid policy snapshot")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("expected the refresh to obey its context, took %s", elapsed)
	}
}

type controlledSyntheticDNSRefreshStore struct {
	Store
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (store *controlledSyntheticDNSRefreshStore) ListResourcesContext(ctx context.Context, kind string) ([]AdminResource, error) {
	if store.calls.Add(1) == 1 {
		return store.Store.ListResources(kind), nil
	}
	snapshot := store.Store.ListResources(kind)
	close(store.started)
	select {
	case <-store.release:
		return snapshot, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestProviderSyntheticDNSPolicyDoesNotOverwriteLocalUpdateWithStaleRefresh(t *testing.T) {
	base := NewMemoryStore()
	setting := base.CreateResource("settings", syntheticDNSSettings(false, defaultSyntheticDNSCIDRs))
	store := &controlledSyntheticDNSRefreshStore{
		Store:   base,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	policy := newProviderSyntheticDNSPolicy(store)
	policy.mu.Lock()
	policy.loadedAt = time.Now().Add(-syntheticDNSPolicyRefreshInterval)
	policy.mu.Unlock()

	refreshed := make(chan struct{})
	go func() {
		policy.refresh(context.Background())
		close(refreshed)
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the stale refresh")
	}
	setting.Fields[syntheticDNSEnabledField] = true
	if _, err := base.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}
	policy.applySetting(&setting)
	close(store.release)
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the stale refresh to finish")
	}
	if !policy.allowsResolvedIP(net.ParseIP("198.18.0.1")) {
		t.Fatal("expected the newer local update to win over the stale refresh result")
	}
}

func TestDialGuardedUpstreamUsesSyntheticDNSOnlyForHostnameResults(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", syntheticDNSSettings(true, defaultSyntheticDNSCIDRs))
	policy := newProviderSyntheticDNSPolicy(store)
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}, nil
	}
	dialed := ""
	dial := func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = addr
		return nil, errors.New("dial reached")
	}
	_, err := dialGuardedUpstream(context.Background(), "tcp", "provider.example:443", nil, policy, time.Second, lookup, dial)
	if err == nil || !strings.Contains(err.Error(), "dial reached") || dialed != "198.18.0.1:443" {
		t.Fatalf("expected configured hostname result to reach the dialer, got addr=%q err=%v", dialed, err)
	}

	dialed = ""
	_, err = dialGuardedUpstream(context.Background(), "tcp", "198.18.0.1:443", nil, policy, time.Second, lookup, dial)
	if !errors.Is(err, errProviderUpstreamDialDisallowed) || dialed != "" {
		t.Fatalf("expected literal address to fail before dialing, got addr=%q err=%v", dialed, err)
	}
}

func TestValidateProviderSyntheticDNSSettingsRejectsUnsafeCIDRs(t *testing.T) {
	for _, test := range []struct {
		cidrs string
		code  string
	}{
		{"", "provider_synthetic_dns_cidrs_required"},
		{"not-a-cidr", "provider_synthetic_dns_cidrs_invalid"},
		{"0.0.0.0/0", "provider_synthetic_dns_cidrs_too_broad"},
		{"169.254.0.0/16", "provider_synthetic_dns_cidrs_not_allowed"},
		{"fc00::/7", "provider_synthetic_dns_cidrs_too_broad"},
		{"10.0.0.0/8", "provider_synthetic_dns_private_cidr_requires_unsafe_mode"},
		{"fc00::/18", "provider_synthetic_dns_private_cidr_requires_unsafe_mode"},
	} {
		err := validateProviderSyntheticDNSSettings(syntheticDNSSettings(true, test.cidrs))
		if code := AsHTTPError(err).Code; code != test.code {
			t.Fatalf("expected %q to fail with %s, got %s (%v)", test.cidrs, test.code, code, err)
		}
	}
	for _, cidrs := range []string{"198.18.0.0/15", "28.0.0.0/8"} {
		if err := validateProviderSyntheticDNSSettings(syntheticDNSSettings(true, cidrs)); err != nil {
			t.Fatalf("expected explicit synthetic range %q to be accepted, got %v", cidrs, err)
		}
	}
	t.Setenv(providerUpstreamNAT64PrefixEnv, "fd00:64::/64")
	err := validateProviderSyntheticDNSSettings(syntheticDNSSettingsWithPrivateTrust(true, "fd00:64::/64"))
	if code := AsHTTPError(err).Code; code != "provider_synthetic_dns_cidrs_not_allowed" {
		t.Fatalf("expected the configured NAT64 prefix to stay protected, got %s (%v)", code, err)
	}
}

func TestAdminSettingsRejectInvalidSyntheticDNSCIDR(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	setting := store.ListResources("settings")[0]
	setting.Fields[syntheticDNSEnabledField] = true
	setting.Fields[syntheticDNSCIDRsField] = "169.254.0.0/16"
	response := doJSON(t, New(store).Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, setting, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "provider_synthetic_dns_cidrs_not_allowed") {
		t.Fatalf("expected invalid synthetic range to be rejected, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminSettingsRequireUnsafeTrustForPrivateSyntheticDNSCIDR(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	setting := store.ListResources("settings")[0]
	setting.Fields[syntheticDNSEnabledField] = true
	setting.Fields[syntheticDNSCIDRsField] = "10.0.0.0/8"

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, setting, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "provider_synthetic_dns_private_cidr_requires_unsafe_mode") {
		t.Fatalf("expected ordinary mode to reject the private range, got %d: %s", response.Code, response.Body)
	}

	setting.Fields[syntheticDNSAllowPrivateField] = true
	response = doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, setting, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected explicit unsafe trust to accept the private range, got %d: %s", response.Code, response.Body)
	}
	if !server.syntheticDNSPolicy.allowsResolvedIP(net.ParseIP("10.0.0.1")) {
		t.Fatal("expected the saved unsafe setting to apply to the runtime policy")
	}
}

func TestAdminSettingsApplySyntheticDNSPolicyWithoutRestart(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	address := net.ParseIP("198.18.0.1")
	if server.syntheticDNSPolicy.allowsResolvedIP(address) {
		t.Fatal("expected the seeded policy to start disabled")
	}
	setting := store.ListResources("settings")[0]
	setting.Fields[syntheticDNSEnabledField] = true
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, setting, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected valid setting update, got %d: %s", response.Code, response.Body)
	}
	if !server.syntheticDNSPolicy.allowsResolvedIP(address) {
		t.Fatal("expected the saved setting to affect new dials without a restart")
	}
}

func TestAdminSettingsPostValidatesSyntheticDNSCIDR(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	setting := syntheticDNSSettings(true, "169.254.0.0/16")
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/resources/settings", setting, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "provider_synthetic_dns_cidrs_not_allowed") {
		t.Fatalf("expected POST upsert to enforce the same validation as PATCH, got %d: %s", response.Code, response.Body)
	}
}

type failingSyntheticDNSCreateStore struct {
	Store
}

func (store failingSyntheticDNSCreateStore) CreateResourceChecked(kind string, resource AdminResource) (AdminResource, error) {
	if kind == "settings" && resource.ID == gatewaySettingsID {
		return AdminResource{}, errors.New("synthetic DNS settings write failed")
	}
	return store.Store.CreateResourceChecked(kind, resource)
}

func TestAdminSettingsPostDoesNotApplySyntheticDNSPolicyWhenPersistenceFails(t *testing.T) {
	base := NewMemoryStore()
	if err := BootstrapBaseData(base); err != nil {
		t.Fatal(err)
	}
	server := New(failingSyntheticDNSCreateStore{Store: base})
	address := net.ParseIP("198.18.0.1")
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/resources/settings", syntheticDNSSettings(true, defaultSyntheticDNSCIDRs), "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected failed persistence to return 500, got %d: %s", response.Code, response.Body)
	}
	if server.syntheticDNSPolicy.allowsResolvedIP(address) {
		t.Fatal("expected failed persistence to leave the runtime policy disabled")
	}
}

func TestAdminSettingsDeleteDisablesSyntheticDNSPolicyImmediately(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	setting := store.ListResources("settings")[0]
	setting.Fields[syntheticDNSEnabledField] = true
	if _, err := store.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	address := net.ParseIP("198.18.0.1")
	if !server.syntheticDNSPolicy.allowsResolvedIP(address) {
		t.Fatal("expected the policy to start enabled")
	}
	response := doJSON(t, server.Handler(), http.MethodDelete, "/api/admin/resources/settings/"+gatewaySettingsID, nil, "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected settings deletion to succeed, got %d: %s", response.Code, response.Body)
	}
	if server.syntheticDNSPolicy.allowsResolvedIP(address) {
		t.Fatal("expected deleting the gateway settings to disable the policy immediately")
	}
}

type delayedSyntheticDNSUpdateStore struct {
	Store
	committed chan struct{}
	release   chan struct{}
}

func (store *delayedSyntheticDNSUpdateStore) UpdateResource(kind string, id string, patch AdminResource) (AdminResource, error) {
	resource, err := store.Store.UpdateResource(kind, id, patch)
	if err == nil && kind == "settings" && id == gatewaySettingsID && truthyField(patch.Fields, syntheticDNSEnabledField) {
		close(store.committed)
		<-store.release
	}
	return resource, err
}

func TestConcurrentAdminSettingsMutationsApplyInCommitOrder(t *testing.T) {
	base := NewMemoryStore()
	if err := BootstrapBaseData(base); err != nil {
		t.Fatal(err)
	}
	store := &delayedSyntheticDNSUpdateStore{
		Store:     base,
		committed: make(chan struct{}),
		release:   make(chan struct{}),
	}
	server := New(store)
	responses := make(chan responseBody, 2)
	go func() {
		responses <- doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, syntheticDNSSettings(true, defaultSyntheticDNSCIDRs), "")
	}()
	select {
	case <-store.committed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first settings commit")
	}
	go func() {
		responses <- doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/resources/settings/"+gatewaySettingsID, syntheticDNSSettings(false, defaultSyntheticDNSCIDRs), "")
	}()
	close(store.release)
	for range 2 {
		select {
		case response := <-responses:
			if response.Code != http.StatusOK {
				t.Fatalf("expected concurrent settings update to succeed, got %d: %s", response.Code, response.Body)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for concurrent settings updates")
		}
	}
	if server.syntheticDNSPolicy.allowsResolvedIP(net.ParseIP("198.18.0.1")) {
		t.Fatal("expected the later disabled setting to win in memory as well as in the store")
	}
}

func TestBootstrapBackfillsProviderSyntheticDNSSettings(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID: gatewaySettingsID, Name: "Existing settings", Status: StatusActive,
		Fields: map[string]any{"public_base_url": "https://gateway.example.com"},
	})
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	setting := store.ListResources("settings")[0]
	if _, ok := setting.Fields[syntheticDNSEnabledField]; !ok {
		t.Fatalf("expected enabled flag to be backfilled: %+v", setting.Fields)
	}
	if got := stringField(setting.Fields, syntheticDNSCIDRsField); got != defaultSyntheticDNSCIDRs {
		t.Fatalf("expected default CIDR to be backfilled, got %q", got)
	}
	if truthyField(setting.Fields, syntheticDNSAllowPrivateField) {
		t.Fatal("expected unsafe private-range trust to be backfilled as disabled")
	}
}

func TestSeedDemoDataPersistsPrivateSyntheticDNSSettingAsDisabled(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	settings := store.ListResources("settings")
	var gatewaySetting *AdminResource
	for index := range settings {
		if settings[index].ID == gatewaySettingsID {
			gatewaySetting = &settings[index]
			break
		}
	}
	if gatewaySetting == nil {
		t.Fatal("expected demo seed to persist gateway settings")
	}
	value, ok := gatewaySetting.Fields[syntheticDNSAllowPrivateField]
	if !ok {
		t.Fatalf("expected demo seed to persist %q explicitly", syntheticDNSAllowPrivateField)
	}
	if truthyField(map[string]any{syntheticDNSAllowPrivateField: value}, syntheticDNSAllowPrivateField) {
		t.Fatal("expected demo seed to keep private-range trust disabled")
	}
}

func TestSeedDemoDataPreservesExistingProviderSyntheticDNSSettings(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	findGatewaySetting := func() AdminResource {
		t.Helper()
		for _, setting := range store.ListResources("settings") {
			if setting.ID == gatewaySettingsID {
				return setting
			}
		}
		t.Fatal("expected gateway settings")
		return AdminResource{}
	}
	setting := findGatewaySetting()
	setting.Fields[syntheticDNSEnabledField] = true
	setting.Fields[syntheticDNSCIDRsField] = "28.0.0.0/8"
	if _, err := store.UpdateResource("settings", gatewaySettingsID, setting); err != nil {
		t.Fatal(err)
	}

	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	setting = findGatewaySetting()
	if !truthyField(setting.Fields, syntheticDNSEnabledField) {
		t.Fatalf("expected demo seed to preserve the enabled setting, got %+v", setting.Fields)
	}
	if got := stringField(setting.Fields, syntheticDNSCIDRsField); got != "28.0.0.0/8" {
		t.Fatalf("expected demo seed to preserve the custom CIDR, got %q", got)
	}
}

type failingSyntheticDNSBackfillStore struct {
	Store
}

type failingSyntheticDNSSeedCreateStore struct {
	Store
}

func (store failingSyntheticDNSSeedCreateStore) CreateResourceChecked(kind string, resource AdminResource) (AdminResource, error) {
	if kind == "settings" && resource.ID == gatewaySettingsID {
		return AdminResource{}, errors.New("gateway settings create failed")
	}
	return store.Store.CreateResourceChecked(kind, resource)
}

func TestBootstrapReportsProviderSyntheticDNSSeedCreateFailure(t *testing.T) {
	store := NewMemoryStore()
	err := BootstrapBaseData(failingSyntheticDNSSeedCreateStore{Store: store})
	if err == nil || !strings.Contains(err.Error(), "seed gateway settings") {
		t.Fatalf("expected bootstrap to report the gateway settings create failure, got %v", err)
	}
}

func (store failingSyntheticDNSBackfillStore) UpdateResource(kind string, id string, patch AdminResource) (AdminResource, error) {
	if kind == "settings" && id == gatewaySettingsID {
		return AdminResource{}, errors.New("backfill failed")
	}
	return store.Store.UpdateResource(kind, id, patch)
}

func TestBootstrapReportsProviderSyntheticDNSBackfillFailure(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{
		ID: gatewaySettingsID, Name: "Existing settings", Status: StatusActive,
		Fields: map[string]any{"public_base_url": "https://gateway.example.com"},
	})
	err := BootstrapBaseData(failingSyntheticDNSBackfillStore{Store: store})
	if err == nil || !strings.Contains(err.Error(), "backfill Provider synthetic DNS settings") {
		t.Fatalf("expected bootstrap to report the backfill failure, got %v", err)
	}
}

type failingSyntheticDNSSettingsReadStore struct {
	Store
}

func (store failingSyntheticDNSSettingsReadStore) ListResourcesChecked(kind string) ([]AdminResource, error) {
	if kind == "settings" {
		return nil, errors.New("settings read failed")
	}
	return store.Store.ListResourcesChecked(kind)
}

func TestBootstrapReportsProviderSyntheticDNSSeedReadFailure(t *testing.T) {
	store := NewMemoryStore()
	err := BootstrapBaseData(failingSyntheticDNSSettingsReadStore{Store: store})
	if err == nil || !strings.Contains(err.Error(), "seed gateway settings") {
		t.Fatalf("expected bootstrap to report the gateway settings read failure, got %v", err)
	}
}

func TestEnsureProviderSyntheticDNSSettingsReportsBackfillReadFailure(t *testing.T) {
	store := NewMemoryStore()
	err := ensureProviderSyntheticDNSSettings(failingSyntheticDNSSettingsReadStore{Store: store})
	if err == nil || !strings.Contains(err.Error(), "read Provider synthetic DNS settings for backfill") {
		t.Fatalf("expected the backfill to report the settings read failure, got %v", err)
	}
}

func TestListResourcesCheckedPreservesStoreContext(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	contextual := store.WithContext(ctx)
	cancel()
	if _, err := contextual.ListResourcesChecked("settings"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the checked read to preserve the canceled store context, got %v", err)
	}
}
