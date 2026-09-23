package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

const (
	providerEgressModeField       = "provider_egress_mode"
	providerProxyProtocolField    = "provider_proxy_protocol"
	providerProxyHostField        = "provider_proxy_host"
	providerProxyPortField        = "provider_proxy_port"
	providerProxyAuthEnabledField = "provider_proxy_auth_enabled"
	providerProxyUsernameField    = "provider_proxy_username"
	providerProxyPasswordField    = "provider_proxy_password"
	providerEgressModeEnvironment = "inherit_environment"
	providerEgressModeDirect      = "direct"
	providerEgressModeConfigured  = "configured_proxy"
	providerProxyRefreshInterval  = 5 * time.Second
	providerProxyRefreshTimeout   = 2 * time.Second
)

type providerProxySnapshot struct {
	mode      string
	proxyURL  *url.URL
	proxyHost string
}

type providerProxyIdleCloser interface {
	CloseIdleConnections()
}

type providerProxyPolicy struct {
	store            Store
	environmentProxy providerProxySelector

	mu          sync.Mutex
	snapshot    providerProxySnapshot
	loaded      bool
	loadedAt    time.Time
	refreshDone chan struct{}
	revision    uint64

	transportsMu sync.Mutex
	transports   []providerProxyIdleCloser
}

func newProviderProxyPolicy(store Store) *providerProxyPolicy {
	environmentProxy := httpproxy.FromEnvironment().ProxyFunc()
	policy := &providerProxyPolicy{
		store: store,
		environmentProxy: func(request *http.Request) (*url.URL, error) {
			return environmentProxy(request.URL)
		},
	}
	_, _ = policy.configuredSnapshot(context.Background())
	return policy
}

func (policy *providerProxyPolicy) proxyForRequest(request *http.Request) (*url.URL, error) {
	if policy == nil {
		return http.ProxyFromEnvironment(request)
	}
	snapshot, err := policy.configuredSnapshot(request.Context())
	if err != nil {
		return nil, err
	}
	switch snapshot.mode {
	case providerEgressModeDirect:
		return nil, nil
	case providerEgressModeConfigured:
		return snapshot.proxyURL, nil
	default:
		proxyURL, proxyErr := policy.environmentProxy(request)
		if proxyErr != nil {
			return nil, proxyErr
		}
		if proxyURL != nil && !strings.EqualFold(proxyURL.Scheme, "http") && !strings.EqualFold(proxyURL.Scheme, "https") {
			return nil, newProviderProxyConfigurationError("Provider environment proxy must use http or https")
		}
		return proxyURL, nil
	}
}

func (policy *providerProxyPolicy) configuredSnapshot(ctx context.Context) (providerProxySnapshot, error) {
	if policy == nil || policy.store == nil {
		return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy settings are unavailable")
	}
	policy.mu.Lock()
	if policy.loaded && time.Since(policy.loadedAt) < providerProxyRefreshInterval {
		snapshot := policy.snapshot
		policy.mu.Unlock()
		return snapshot, nil
	}
	if policy.refreshDone != nil {
		done := policy.refreshDone
		policy.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return providerProxySnapshot{}, ctx.Err()
		}
		policy.mu.Lock()
		snapshot, loaded := policy.snapshot, policy.loaded
		policy.mu.Unlock()
		if !loaded {
			return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy settings have not been loaded")
		}
		return snapshot, nil
	}
	policy.refreshDone = make(chan struct{})
	done := policy.refreshDone
	revision := policy.revision
	policy.mu.Unlock()

	refreshCtx, cancel := context.WithTimeout(ctx, providerProxyRefreshTimeout)
	defer cancel()
	settings, err := policy.listSettings(refreshCtx)
	var snapshot providerProxySnapshot
	if err == nil {
		snapshot, err = providerProxySnapshotFromSettings(settings, policy.store)
	}

	policy.mu.Lock()
	stale := policy.revision != revision
	changed := false
	if err == nil && !stale {
		changed = !providerProxySnapshotsEqual(policy.snapshot, snapshot) || !policy.loaded
		policy.snapshot = snapshot
		policy.loaded = true
		policy.loadedAt = time.Now()
	} else if !stale {
		policy.loadedAt = time.Now()
		log.Printf("[tokenhub] Provider proxy settings refresh failed: stage=config code=provider_proxy_config_error")
	}
	loaded := policy.loaded
	current := policy.snapshot
	policy.refreshDone = nil
	close(done)
	policy.mu.Unlock()
	if changed {
		policy.closeIdleConnections()
	}
	if loaded {
		return current, nil
	}
	return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy settings could not be loaded")
}

func (policy *providerProxyPolicy) listSettings(ctx context.Context) ([]AdminResource, error) {
	if store, ok := policy.store.(providerSyntheticDNSContextStore); ok {
		return store.ListResourcesContext(ctx, "settings")
	}
	return policy.store.ListResourcesChecked("settings")
}

func providerProxySnapshotFromSettings(settings []AdminResource, store Store) (providerProxySnapshot, error) {
	for _, setting := range settings {
		if setting.ID == gatewaySettingsID && setting.Status == StatusActive {
			return providerProxySnapshotFromSetting(setting, store)
		}
	}
	return providerProxySnapshot{mode: providerEgressModeEnvironment}, nil
}

func providerProxySnapshotFromSetting(setting AdminResource, store Store) (providerProxySnapshot, error) {
	mode := strings.ToLower(strings.TrimSpace(stringField(setting.Fields, providerEgressModeField)))
	if mode == "" {
		mode = providerEgressModeEnvironment
	}
	snapshot := providerProxySnapshot{mode: mode}
	switch mode {
	case providerEgressModeEnvironment, providerEgressModeDirect:
		return snapshot, nil
	case providerEgressModeConfigured:
	default:
		return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider egress mode is invalid")
	}

	protocol := strings.ToLower(strings.TrimSpace(stringField(setting.Fields, providerProxyProtocolField)))
	if protocol != "http" && protocol != "https" {
		return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy protocol must be http or https")
	}
	host := strings.TrimSpace(stringField(setting.Fields, providerProxyHostField))
	if !validProviderProxyHost(host) {
		return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy host is invalid")
	}
	port, err := strconv.Atoi(strings.TrimSpace(stringField(setting.Fields, providerProxyPortField)))
	if err != nil || port < 1 || port > 65535 {
		return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy port is invalid")
	}
	proxyURL := &url.URL{Scheme: protocol, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if truthyField(setting.Fields, providerProxyAuthEnabledField) {
		username := strings.TrimSpace(stringField(setting.Fields, providerProxyUsernameField))
		password := stringField(setting.Fields, providerProxyPasswordField)
		if strings.HasPrefix(password, "enc:v1:") {
			codec, ok := store.(adminResourceSecretCodec)
			if !ok {
				return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy credential storage is unavailable")
			}
			password = codec.revealAdminResourceSecret(password)
		}
		if username == "" || password == "" {
			return providerProxySnapshot{}, newProviderProxyConfigurationError("Provider proxy username and password are required when authentication is enabled")
		}
		proxyURL.User = url.UserPassword(username, password)
	}
	snapshot.proxyURL = proxyURL
	snapshot.proxyHost = host
	return snapshot, nil
}

func validProviderProxyHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " /\\@?#") {
		return false
	}
	testURL, err := url.Parse("http://" + net.JoinHostPort(host, "80"))
	return err == nil && testURL.Hostname() == host
}

func validateProviderProxySettings(setting AdminResource, store Store) error {
	if setting.ID != "" && setting.ID != gatewaySettingsID {
		return nil
	}
	if _, err := providerProxySnapshotFromSetting(setting, store); err != nil {
		return NewHTTPError(http.StatusBadRequest, "provider_proxy_settings_invalid", AsHTTPError(err).Message)
	}
	return nil
}

func normalizeProviderProxySettingsFields(fields map[string]any) map[string]any {
	if fields == nil || truthyField(fields, providerProxyAuthEnabledField) {
		return fields
	}
	normalized := cloneAdminResourceFields(fields)
	delete(normalized, providerProxyUsernameField)
	delete(normalized, providerProxyPasswordField)
	return normalized
}

func ensureProviderProxySettings(store Store) error {
	settings, err := store.ListResourcesChecked("settings")
	if err != nil {
		return fmt.Errorf("read Provider proxy settings for backfill: %w", err)
	}
	for _, setting := range settings {
		if setting.ID != gatewaySettingsID {
			continue
		}
		if setting.Fields == nil {
			setting.Fields = map[string]any{}
		}
		if _, ok := setting.Fields[providerEgressModeField]; ok {
			return nil
		}
		setting.Fields[providerEgressModeField] = providerEgressModeEnvironment
		if _, err := store.UpdateResource("settings", setting.ID, setting); err != nil {
			return fmt.Errorf("backfill Provider proxy settings: %w", err)
		}
		return nil
	}
	return nil
}

func providerProxySnapshotsEqual(left, right providerProxySnapshot) bool {
	if left.mode != right.mode || left.proxyHost != right.proxyHost {
		return false
	}
	leftURL, rightURL := "", ""
	if left.proxyURL != nil {
		leftURL = left.proxyURL.String()
	}
	if right.proxyURL != nil {
		rightURL = right.proxyURL.String()
	}
	return leftURL == rightURL
}

func (policy *providerProxyPolicy) applySetting(setting *AdminResource) {
	if policy == nil {
		return
	}
	var (
		snapshot providerProxySnapshot
		err      error
	)
	if setting == nil || setting.Status != StatusActive {
		snapshot = providerProxySnapshot{mode: providerEgressModeEnvironment}
	} else {
		snapshot, err = providerProxySnapshotFromSetting(*setting, policy.store)
	}
	if err != nil {
		log.Printf("[tokenhub] Provider proxy settings apply failed: stage=config code=provider_proxy_config_error")
		return
	}
	policy.mu.Lock()
	changed := !policy.loaded || !providerProxySnapshotsEqual(policy.snapshot, snapshot)
	policy.revision++
	policy.snapshot = snapshot
	policy.loaded = true
	policy.loadedAt = time.Now()
	policy.mu.Unlock()
	if changed {
		policy.closeIdleConnections()
	}
}

func (policy *providerProxyPolicy) registerTransport(transport providerProxyIdleCloser) {
	if policy == nil || transport == nil {
		return
	}
	policy.transportsMu.Lock()
	policy.transports = append(policy.transports, transport)
	policy.transportsMu.Unlock()
}

func (policy *providerProxyPolicy) closeIdleConnections() {
	policy.transportsMu.Lock()
	transports := append([]providerProxyIdleCloser(nil), policy.transports...)
	policy.transportsMu.Unlock()
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}

func newProviderProxyConfigurationError(message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusServiceUnavailable, "provider_proxy_config_error", message),
		Disposition: ProviderErrorEgress,
	}
}
