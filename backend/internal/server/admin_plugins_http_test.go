package server

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginInstallPostDownloadsAndInstallsPackage(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"bundle/plugin.yaml": adminPluginManifest("tokenhub.marketplace.kimi", "Marketplace Kimi", "1.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":    upstream.URL + "/kimi.zip",
		"checksum_sha256": adminSHA256Hex(archive),
		"reason":          "installed from marketplace",
		"enable":          false,
		"replace":         false,
	}, "dev_admin_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/admin/plugins/install: expected 201, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if body.Data.Plugin.ID != "tokenhub.marketplace.kimi" || body.Data.Plugin.Status != pluginmeta.StatusDisabled || !body.Data.RestartRequired {
		t.Fatalf("install response = %+v, want disabled plugin requiring restart", body.Data)
	}
	if !body.Data.Plugin.Lifecycle.RestartRequired || body.Data.Plugin.Lifecycle.AuditEvent != pluginmeta.PackageLifecycleInstalled {
		t.Fatalf("install lifecycle = %+v, want installed restart-required event", body.Data.Plugin.Lifecycle)
	}
	stateData, err := os.ReadFile(filepath.Join(pluginDir, "tokenhub.marketplace.kimi", "plugin.state.json"))
	if err != nil {
		t.Fatalf("read installed package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode installed package state: %v", err)
	}
	if state.Status != pluginmeta.StatusDisabled || state.Reason != "installed from marketplace" ||
		!state.RestartRequired || state.AuditEvent != pluginmeta.PackageLifecycleInstalled {
		t.Fatalf("installed package state = %+v", state)
	}
}

func TestAdminPluginInstallPostRejectsUnsafeDownloadURL(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":    "http://plugins.example/kimi.zip",
		"checksum_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST unsafe plugin install: expected 400, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginInstallPostRejectsChecksumMismatch(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.bad-checksum", "Bad Checksum", "1.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":    upstream.URL + "/plugin.zip",
		"checksum_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST checksum mismatch plugin install: expected 400, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginInstallPostVerifiesSignedMarketplaceArtifact(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.signed", "Signed Marketplace", "1.0.0"),
	})
	keyID, publicKey, signature := adminPluginArtifactSignatureForTest(t, archive)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/signed.zip":
			_, _ = w.Write(archive)
		case "/signed.zip.sig":
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":         upstream.URL + "/signed.zip",
		"checksum_sha256":      adminSHA256Hex(archive),
		"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
		"signature_url":        upstream.URL + "/signed.zip.sig",
		"signature_key_id":     keyID,
		"signature_public_key": publicKey,
		"enable":               true,
		"replace":              false,
	}, "dev_admin_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("POST signed plugin install: expected 201, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if body.Data.Plugin.ID != "tokenhub.marketplace.signed" || body.Data.Plugin.Status != pluginmeta.StatusEnabled {
		t.Fatalf("install response = %+v, want enabled signed plugin", body.Data)
	}
	stateData, err := os.ReadFile(filepath.Join(pluginDir, "tokenhub.marketplace.signed", "plugin.state.json"))
	if err != nil {
		t.Fatalf("read signed installed package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode signed installed package state: %v", err)
	}
	if state.Status != pluginmeta.StatusEnabled || !state.RestartRequired || state.AuditEvent != pluginmeta.PackageLifecycleInstalled {
		t.Fatalf("signed installed package state = %+v, want enabled installed restart-required state", state)
	}
}

func TestAdminPluginInstallPostRejectsSignedMarketplaceSignatureMismatch(t *testing.T) {
	signedArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.signed", "Signed Marketplace", "1.0.0"),
	})
	tamperedArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.signed", "Signed Marketplace", "1.0.1"),
	})
	keyID, publicKey, signature := adminPluginArtifactSignatureForTest(t, signedArchive)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/signed.zip":
			_, _ = w.Write(tamperedArchive)
		case "/signed.zip.sig":
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":         upstream.URL + "/signed.zip",
		"checksum_sha256":      adminSHA256Hex(tamperedArchive),
		"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
		"signature_url":        upstream.URL + "/signed.zip.sig",
		"signature_key_id":     keyID,
		"signature_public_key": publicKey,
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST signed plugin install with mismatched signature: expected 400, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "plugin_signature_verification_failed") {
		t.Fatalf("response body = %s, want signature verification failure", response.Body)
	}
}

func TestAdminPluginInstallPostRejectsSignedMarketplaceMissingPublicKey(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.signed", "Signed Marketplace", "1.0.0"),
	})
	keyID, _, signature := adminPluginArtifactSignatureForTest(t, archive)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/signed.zip":
			_, _ = w.Write(archive)
		case "/signed.zip.sig":
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":     upstream.URL + "/signed.zip",
		"checksum_sha256":  adminSHA256Hex(archive),
		"trust_policy":     string(pluginmeta.TrustPolicySignedMarketplace),
		"signature_url":    upstream.URL + "/signed.zip.sig",
		"signature_key_id": keyID,
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST signed plugin install without public key: expected 400, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "plugin_signature_required") {
		t.Fatalf("response body = %s, want signature required failure", response.Body)
	}
}

func TestAdminPluginInstallPostRejectsSignedMarketplaceTrustFailures(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.signed", "Signed Marketplace", "1.0.0"),
	})
	keyID, publicKey, signature := adminPluginArtifactSignatureForTest(t, archive)
	otherKeyID := "tokenhub-other-2026"
	_, otherPublicKey, otherSignature := adminPluginArtifactSignatureForTestWithKeyID(t, archive, otherKeyID)
	_, mismatchedPublicKey, _ := adminPluginArtifactSignatureForTestWithKeyID(t, archive, keyID)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/signed.zip":
			_, _ = w.Write(archive)
		case "/signed.zip.sig":
			_, _ = w.Write(signature)
		case "/other-key.sig":
			_, _ = w.Write(otherSignature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	for _, tc := range []struct {
		name     string
		payload  map[string]any
		wantCode string
	}{
		{
			name: "missing signature url",
			payload: map[string]any{
				"download_url":         upstream.URL + "/signed.zip",
				"checksum_sha256":      adminSHA256Hex(archive),
				"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
				"signature_key_id":     keyID,
				"signature_public_key": publicKey,
			},
			wantCode: "plugin_signature_required",
		},
		{
			name: "missing signature key id",
			payload: map[string]any{
				"download_url":         upstream.URL + "/signed.zip",
				"checksum_sha256":      adminSHA256Hex(archive),
				"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
				"signature_url":        upstream.URL + "/signed.zip.sig",
				"signature_public_key": publicKey,
			},
			wantCode: "plugin_signature_required",
		},
		{
			name: "signature envelope key id mismatch",
			payload: map[string]any{
				"download_url":         upstream.URL + "/signed.zip",
				"checksum_sha256":      adminSHA256Hex(archive),
				"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
				"signature_url":        upstream.URL + "/other-key.sig",
				"signature_key_id":     keyID,
				"signature_public_key": otherPublicKey,
			},
			wantCode: "plugin_signature_verification_failed",
		},
		{
			name: "public key mismatch",
			payload: map[string]any{
				"download_url":         upstream.URL + "/signed.zip",
				"checksum_sha256":      adminSHA256Hex(archive),
				"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
				"signature_url":        upstream.URL + "/signed.zip.sig",
				"signature_key_id":     keyID,
				"signature_public_key": mismatchedPublicKey,
			},
			wantCode: "plugin_signature_verification_failed",
		},
		{
			name: "malformed public key",
			payload: map[string]any{
				"download_url":         upstream.URL + "/signed.zip",
				"checksum_sha256":      adminSHA256Hex(archive),
				"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
				"signature_url":        upstream.URL + "/signed.zip.sig",
				"signature_key_id":     keyID,
				"signature_public_key": "not base64",
			},
			wantCode: "invalid_plugin_signature_key",
		},
		{
			name: "signature download failure",
			payload: map[string]any{
				"download_url":         upstream.URL + "/signed.zip",
				"checksum_sha256":      adminSHA256Hex(archive),
				"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
				"signature_url":        upstream.URL + "/missing.sig",
				"signature_key_id":     keyID,
				"signature_public_key": publicKey,
			},
			wantCode: "plugin_signature_download_failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
			server.pluginInstallClient = upstream.Client()

			response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", tc.payload, "dev_admin_token")
			if response.Code != http.StatusBadRequest && response.Code != http.StatusBadGateway {
				t.Fatalf("POST signed plugin install: expected failure, got %d: %s", response.Code, response.Body)
			}
			if !strings.Contains(response.Body, tc.wantCode) {
				t.Fatalf("response body = %s, want code %q", response.Body, tc.wantCode)
			}
		})
	}
}

func TestAdminPluginUpdatePostDownloadsAndReplacesPackage(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "kimi")
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.kimi", "Marketplace Kimi", "1.1.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
distribution:
  download_url: `+upstream.URL+`/kimi-1.1.0.zip
  checksum_sha256: `+adminSHA256Hex(archive)+`
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	if err := os.WriteFile(filepath.Join(localPluginDir, "plugin.state.json"), []byte(`{"status":"disabled","reason":"operator disabled"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.marketplace.kimi/update", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/plugins/{id}/update: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if body.Data.Plugin.Version != "1.1.0" || body.Data.Plugin.Status != pluginmeta.StatusDisabled || !body.Data.RestartRequired || !body.Data.Replaced {
		t.Fatalf("update response = %+v, want replaced disabled updated package", body.Data)
	}
	if !body.Data.Plugin.Lifecycle.RestartRequired || body.Data.Plugin.Lifecycle.AuditEvent != pluginmeta.PackageLifecyclePendingRestart {
		t.Fatalf("update lifecycle = %+v, want pending restart event", body.Data.Plugin.Lifecycle)
	}
	updatedPluginDir := filepath.Join(pluginDir, "tokenhub.marketplace.kimi")
	stateData, err := os.ReadFile(filepath.Join(updatedPluginDir, "plugin.state.json"))
	if err != nil {
		t.Fatalf("read updated package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode updated package state: %v", err)
	}
	if state.Status != pluginmeta.StatusDisabled || state.Reason != "operator disabled" ||
		!state.RestartRequired || state.AuditEvent != pluginmeta.PackageLifecyclePendingRestart {
		t.Fatalf("updated package state = %+v", state)
	}
	if _, err := os.Stat(localPluginDir); !os.IsNotExist(err) {
		t.Fatalf("old package directory still exists after update: %v", err)
	}
}

func TestAdminPluginUpdatePostVerifiesSignedMarketplaceDistribution(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "tokenhub.marketplace.signed")
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.signed", "Signed Marketplace", "1.1.0"),
	})
	keyID, publicKey, signature := adminPluginArtifactSignatureForTest(t, archive)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/signed-1.1.0.zip":
			_, _ = w.Write(archive)
		case "/signed-1.1.0.zip.sig":
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.marketplace.signed
name: Signed Marketplace
version: 1.0.0
distribution:
  download_url: `+upstream.URL+`/signed-1.1.0.zip
  checksum_sha256: `+adminSHA256Hex(archive)+`
  signature_url: `+upstream.URL+`/signed-1.1.0.zip.sig
  signature_algorithm: ed25519
  signature_key_id: `+keyID+`
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.marketplace.signed/update", map[string]any{
		"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
		"signature_public_key": publicKey,
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST signed plugin update: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if body.Data.Plugin.Version != "1.1.0" || !body.Data.Replaced {
		t.Fatalf("update response = %+v, want signed marketplace replacement", body.Data)
	}
	stateData, err := os.ReadFile(filepath.Join(localPluginDir, "plugin.state.json"))
	if err != nil {
		t.Fatalf("read signed updated package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode signed updated package state: %v", err)
	}
	if state.Status != pluginmeta.StatusEnabled || !state.RestartRequired || state.AuditEvent != pluginmeta.PackageLifecyclePendingRestart {
		t.Fatalf("signed updated package state = %+v, want enabled pending restart state", state)
	}
}

func TestAdminPluginUpdatePostAcceptsMarketplaceDistributionOverride(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "kimi")
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.kimi", "Marketplace Kimi", "1.2.0"),
	})
	var requestedPath string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
distribution:
  download_url: https://plugins.example/kimi-1.0.0.zip
  checksum_sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.marketplace.kimi/update", map[string]any{
		"download_url":    upstream.URL + "/kimi-1.2.0.zip",
		"checksum_sha256": adminSHA256Hex(archive),
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/plugins/{id}/update with override: expected 200, got %d: %s", response.Code, response.Body)
	}
	if requestedPath != "/kimi-1.2.0.zip" {
		t.Fatalf("update downloaded %q, want marketplace override path", requestedPath)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if body.Data.Plugin.Version != "1.2.0" || !body.Data.Replaced {
		t.Fatalf("update response = %+v, want marketplace replacement", body.Data)
	}
}

func TestAdminPluginStatePatchWritesLocalPackageState(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "privacy")
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.local-privacy
name: Local Privacy
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.local-privacy/state", map[string]any{
		"status": "disabled",
		"reason": "disable before upgrade",
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH /api/admin/plugins/{id}/state: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginStateResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.PluginID != "tokenhub.local-privacy" || body.Data.Status != pluginmeta.StatusDisabled || !body.Data.RestartRequired {
		t.Fatalf("state response = %+v, want disabled restart-required response", body.Data)
	}
	data, err := os.ReadFile(filepath.Join(localPluginDir, "plugin.state.json"))
	if err != nil {
		t.Fatalf("read package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode package state: %v", err)
	}
	if state.Status != pluginmeta.StatusDisabled || state.Reason != "disable before upgrade" {
		t.Fatalf("package state = %+v, want disabled state file", state)
	}
}

func TestAdminPluginsGetExposesLifecycleTrustAndCompatibilitySummaries(t *testing.T) {
	pluginDir := t.TempDir()
	for _, fixture := range []struct {
		dir   string
		id    string
		state string
	}{
		{
			dir:   "pending",
			id:    "tokenhub.local-pending",
			state: `{"status":"pending_restart","reason":"installed update","audit_event":"pending_restart"}`,
		},
		{
			dir:   "failed",
			id:    "tokenhub.local-failed",
			state: `{"status":"failed_validation","health":"unhealthy","last_error_code":"plugin_api_unsupported","audit_event":"validation_failed"}`,
		},
		{
			dir:   "rollback",
			id:    "tokenhub.local-rollback",
			state: `{"status":"rollback_available","rollback_version":"1.0.0","audit_event":"rollback_available"}`,
		},
		{
			dir:   "mandatory",
			id:    "tokenhub.local-mandatory",
			state: `{"status":"mandatory","health":"healthy"}`,
		},
	} {
		pluginPath := filepath.Join(pluginDir, fixture.dir)
		writeServerPluginManifest(t, pluginPath, adminPluginManifestWithDistribution(fixture.id, "Lifecycle Plugin", "1.0.0"))
		if err := os.WriteFile(filepath.Join(pluginPath, "plugin.state.json"), []byte(fixture.state), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugins: expected 200, got %d: %s", response.Code, response.Body)
	}
	for _, secret := range []string{
		"download-secret",
		"signature-secret",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"download_url",
		"signature_url",
		"checksum_sha256",
	} {
		if strings.Contains(response.Body, secret) {
			t.Fatalf("GET /api/admin/plugins leaked %q in response: %s", secret, response.Body)
		}
	}
	var body struct {
		Data []adminPluginDescriptorResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	plugins := map[string]adminPluginDescriptorResponse{}
	for _, plugin := range body.Data {
		plugins[plugin.ID] = plugin
	}
	pending := plugins["tokenhub.local-pending"]
	if pending.Status != pluginmeta.StatusPendingRestart || !pending.RestartRequired || pending.Loadable {
		t.Fatalf("pending plugin = %+v, want pending restart and non-loadable", pending)
	}
	failed := plugins["tokenhub.local-failed"]
	if failed.Health != pluginmeta.PackageHealthUnhealthy || failed.LastErrorCode != string(pluginmeta.PluginErrorAPIUnsupported) || failed.Loadable {
		t.Fatalf("failed plugin = %+v, want unhealthy failed validation", failed)
	}
	rollback := plugins["tokenhub.local-rollback"]
	if !rollback.RollbackAvailable || rollback.RollbackVersion != "1.0.0" || !rollback.Loadable {
		t.Fatalf("rollback plugin = %+v, want rollback available and loadable", rollback)
	}
	mandatory := plugins["tokenhub.local-mandatory"]
	if !mandatory.Mandatory || mandatory.Health != pluginmeta.PackageHealthHealthy || !mandatory.Loadable {
		t.Fatalf("mandatory plugin = %+v, want mandatory healthy loadable", mandatory)
	}
	for _, plugin := range []adminPluginDescriptorResponse{pending, failed, rollback, mandatory} {
		if plugin.Compatibility.Verdict != "compatible" || plugin.Compatibility.PluginAPI != pluginmeta.CurrentPluginAPI {
			t.Fatalf("plugin compatibility = %+v, want compatible current API", plugin.Compatibility)
		}
		if plugin.Trust.Verdict != pluginmeta.TrustVerdictUnverified || !plugin.Trust.ChecksumPresent || !plugin.Trust.SignaturePresent {
			t.Fatalf("plugin trust = %+v, want unverified package with checksum/signature summary", plugin.Trust)
		}
		if plugin.Distribution == nil || plugin.Distribution.DownloadURL != "" || plugin.Distribution.SignatureURL != "" || plugin.Distribution.ChecksumSHA256 != "" {
			t.Fatalf("plugin distribution = %+v, want sanitized admin distribution", plugin.Distribution)
		}
	}
}

func TestAdminPluginsGetReflectsBuiltinCodexUIContributions(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token"})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugins: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data []adminPluginDescriptorResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode plugin response: %v", err)
	}
	codex, ok := adminPluginByID(body.Data, "tokenhub.provider.openai-codex")
	if !ok {
		t.Fatal("Codex plugin descriptor is missing")
	}
	for _, capability := range []pluginmeta.CapabilityDescriptor{
		{Kind: "admin_ui", Name: string(pluginmeta.SlotProviderFormSection), Subject: "provider-setup", Value: "openai_codex.oauth.start"},
		{Kind: "admin_ui", Name: string(pluginmeta.SlotProviderResourceFormSection), Subject: "fingerprint"},
		{Kind: "admin_ui", Name: string(pluginmeta.SlotProviderModelPanel), Subject: "image-capability", Value: "openai_codex.image_capability.configure"},
		{Kind: "admin_ui", Name: string(pluginmeta.SlotProviderResourcePanel), Subject: "quota", Value: "openai_codex.quota.read"},
	} {
		if !descriptorHasPluginCapability(codex.Descriptor, capability) {
			t.Fatalf("Codex plugin descriptor missing UI capability %+v in %+v", capability, codex.Capabilities)
		}
	}
	for _, secret := range []string{"download_url", "signature_url", "checksum_sha256"} {
		if strings.Contains(response.Body, secret) {
			t.Fatalf("Codex admin plugin payload leaked %q: %s", secret, response.Body)
		}
	}
}

func TestAdminPluginDeleteUninstallsLocalPackage(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "privacy")
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.local-privacy
name: Local Privacy
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	response := doJSON(t, server.Handler(), http.MethodDelete, "/api/admin/plugin-packages/tokenhub.local-privacy", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("DELETE /api/admin/plugin-packages/{id}: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginUninstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.PluginID != "tokenhub.local-privacy" || !body.Data.RestartRequired {
		t.Fatalf("delete response = %+v, want restart-required uninstall response", body.Data)
	}
	if _, err := os.Stat(localPluginDir); !os.IsNotExist(err) {
		t.Fatalf("plugin package still exists after delete: %v", err)
	}
}

func TestAdminPluginDeleteRejectsMissingPackage(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	response := doJSON(t, server.Handler(), http.MethodDelete, "/api/admin/plugin-packages/tokenhub.missing", nil, "dev_admin_token")
	if response.Code != http.StatusNotFound {
		t.Fatalf("DELETE missing plugin: expected 404, got %d: %s", response.Code, response.Body)
	}
}

func TestRemoveSupersededPluginPackageDirKeepsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, tc := range []struct {
		name         string
		previousDir  func(t *testing.T) string
		installedDir func(t *testing.T) string
	}{
		{
			name: "empty previous dir",
			previousDir: func(t *testing.T) string {
				return ""
			},
			installedDir: func(t *testing.T) string {
				return filepath.Join(root, "installed")
			},
		},
		{
			name: "previous dir is root",
			previousDir: func(t *testing.T) string {
				return root
			},
			installedDir: func(t *testing.T) string {
				return filepath.Join(root, "installed")
			},
		},
		{
			name: "previous dir outside root",
			previousDir: func(t *testing.T) string {
				return filepath.Join(outside, "previous")
			},
			installedDir: func(t *testing.T) string {
				return filepath.Join(root, "installed")
			},
		},
		{
			name: "previous dir equals installed dir",
			previousDir: func(t *testing.T) string {
				return filepath.Join(root, "same")
			},
			installedDir: func(t *testing.T) string {
				return filepath.Join(root, "same")
			},
		},
		{
			name: "installed dir nested under previous dir",
			previousDir: func(t *testing.T) string {
				return filepath.Join(root, "previous")
			},
			installedDir: func(t *testing.T) string {
				return filepath.Join(root, "previous", "installed")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousDir := tc.previousDir(t)
			installedDir := tc.installedDir(t)
			if previousDir != "" {
				if err := os.MkdirAll(previousDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(previousDir, "keep.txt"), []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if installedDir != "" {
				if err := os.MkdirAll(installedDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if err := removeSupersededPluginPackageDir(root, previousDir, installedDir); err != nil {
				t.Fatalf("remove superseded package dir: %v", err)
			}
			if previousDir != "" {
				if _, err := os.Stat(previousDir); err != nil {
					t.Fatalf("previous dir was removed or inaccessible: %v", err)
				}
			}
		})
	}
}

func TestRemoveSupersededPluginPackageDirRemovesOnlySupersededChild(t *testing.T) {
	root := t.TempDir()
	previousDir := filepath.Join(root, "legacy")
	installedDir := filepath.Join(root, "tokenhub.marketplace.kimi")
	if err := os.MkdirAll(previousDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previousDir, "remove.txt"), []byte("remove"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := removeSupersededPluginPackageDir(root, previousDir, installedDir); err != nil {
		t.Fatalf("remove superseded package dir: %v", err)
	}
	if _, err := os.Stat(previousDir); !os.IsNotExist(err) {
		t.Fatalf("previous dir still exists after cleanup: %v", err)
	}
	if _, err := os.Stat(installedDir); err != nil {
		t.Fatalf("installed dir was removed: %v", err)
	}
}

func adminPluginZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func adminPluginManifest(id string, name string, version string) string {
	return `
schema_version: 1
id: ` + id + `
name: ` + name + `
version: ` + version + `
tokenhub:
  plugin_api: v1
kinds:
  - extension
`
}

func adminPluginManifestWithDistribution(id string, name string, version string) string {
	return `
schema_version: 1
id: ` + id + `
name: ` + name + `
version: ` + version + `
distribution:
  marketplace_url: https://plugins.example/marketplace/` + id + `
  repository_url: https://github.com/tokenhub/` + id + `
  download_url: https://plugins.example/download/` + id + `.zip?token=download-secret
  checksum_sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  signature_url: https://plugins.example/signatures/` + id + `.sig?token=signature-secret
  signature_algorithm: ed25519
  signature_key_id: tokenhub-test-key
  homepage_url: https://plugins.example/` + id + `
  license: Apache-2.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`
}

func adminSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func adminPluginArtifactSignatureForTest(t *testing.T, archive []byte) (string, string, []byte) {
	t.Helper()
	return adminPluginArtifactSignatureForTestWithKeyID(t, archive, "tokenhub-artifact-2026")
}

func adminPluginArtifactSignatureForTestWithKeyID(t *testing.T, archive []byte, keyID string) (string, string, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate artifact signing key: %v", err)
	}
	envelope, err := pluginmeta.NewMarketplaceSignatureEnvelope(archive, pluginmeta.MarketplaceArtifactMediaType, keyID, privateKey)
	if err != nil {
		t.Fatalf("create artifact signature envelope: %v", err)
	}
	signature, err := pluginmeta.EncodeMarketplaceSignatureEnvelope(envelope)
	if err != nil {
		t.Fatalf("encode artifact signature envelope: %v", err)
	}
	return keyID, base64.StdEncoding.EncodeToString(publicKey), signature
}

func TestAdminPluginStatePatchRejectsInvalidState(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	missing := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.local-privacy/state", map[string]any{}, "dev_admin_token")
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("PATCH missing plugin state: expected 400, got %d: %s", missing.Code, missing.Body)
	}

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.local-privacy/state", map[string]any{
		"status": "paused",
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid plugin state: expected 400, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginStatePatchRejectsMissingPlugin(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.missing/state", map[string]any{
		"status": "disabled",
	}, "dev_admin_token")
	if response.Code != http.StatusNotFound {
		t.Fatalf("PATCH missing plugin state: expected 404, got %d: %s", response.Code, response.Body)
	}
}
