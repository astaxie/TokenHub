package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const providerProxyTestTimeout = 15 * time.Second

type providerEgressTestRequest struct {
	ProviderID string         `json:"provider_id"`
	Fields     map[string]any `json:"fields"`
}

func (s *Server) handleAdminProviderEgressTestPost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	var req providerEgressTestRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	provider, found := s.store.GetProvider(strings.TrimSpace(req.ProviderID))
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found"))
		return
	}
	target, err := providerEgressTestTarget(provider)
	if err != nil {
		writeError(w, r, err)
		return
	}
	setting := AdminResource{ID: gatewaySettingsID, Status: StatusActive, Fields: req.Fields}
	if current, findErr := s.findResource("settings", gatewaySettingsID); findErr == nil {
		setting.Fields = preserveAdminResourceSecrets("settings", current.Fields, req.Fields)
	}
	setting.Fields = normalizeProviderProxySettingsFields(setting.Fields)
	if err := validateProviderProxySettings(setting, s.store); err != nil {
		writeError(w, r, err)
		return
	}
	snapshot, err := providerProxySnapshotFromSetting(setting, s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if snapshot.mode != providerEgressModeConfigured || snapshot.proxyURL == nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "provider_proxy_test_requires_configured_proxy", "Proxy test requires configured proxy mode"))
		return
	}
	started := time.Now()
	if err := testProviderProxyConnection(r.Context(), snapshot.proxyURL, target, s.syntheticDNSPolicy); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "provider_id": provider.ID, "target_host": target.Hostname(),
		"mode": snapshot.mode, "latency_ms": time.Since(started).Milliseconds(),
	})
}

func providerEgressTestTarget(provider Provider) (*url.URL, error) {
	raw := strings.TrimSpace(provider.BaseURL)
	if raw == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_base_url_required", "Provider base URL is required for proxy testing")
	}
	target, err := url.Parse(raw)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_base_url_invalid", "Base URL is invalid")
	}
	if err := validateProviderUpstreamBaseURL(target, allowedProviderUpstreamCIDRs(), providerUpstreamLoopbackAllowed()); err != nil {
		return nil, err
	}
	return target, nil
}

func testProviderProxyConnection(parent context.Context, proxyURL, target *url.URL, syntheticDNS providerSyntheticDNSResolver) error {
	ctx, cancel := context.WithTimeout(parent, providerProxyTestTimeout)
	defer cancel()
	targetPort := target.Port()
	if targetPort == "" {
		targetPort = map[string]string{"http": "80", "https": "443"}[strings.ToLower(target.Scheme)]
	}
	targets, err := resolveProviderProxyTargetIPs(ctx, target.Hostname(), allowedProviderUpstreamCIDRs(), syntheticDNS, net.DefaultResolver.LookupIPAddr)
	if err != nil {
		return err
	}
	connection, err := dialProviderProxyTunnel(ctx, &net.Dialer{}, providerProxyTestTimeout, proxyURL, targetPort, targets, nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if strings.EqualFold(target.Scheme, "https") {
		tlsTarget := tls.Client(connection, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.Hostname()})
		if err := tlsTarget.HandshakeContext(ctx); err != nil {
			return newProviderProxyTransportError("connect", err)
		}
	}
	return nil
}
