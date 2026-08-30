package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginPermissionDiffInstallPreviewDownloadsArchiveAndReportsAddedPermissions(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.install", "Permission Install", "1.0.0", `
permissions:
  data:
    read:
      - provider_credentials
  network:
    allow:
      - https://api.example.com/v1?token=download-secret
`),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	pluginDir := t.TempDir()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/permission-diff", map[string]any{
		"download_url":    upstream.URL + "/install.zip?token=admin-secret",
		"checksum_sha256": adminSHA256Hex(archive),
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST install permission diff: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginPermissionDiffPreviewResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode permission diff response: %v", err)
	}
	if body.Data.Operation != "install" || body.Data.PluginID != "tokenhub.permission.install" || body.Data.CandidateVersion != "1.0.0" {
		t.Fatalf("response identity = %+v", body.Data)
	}
	if body.Data.PermissionDiff.Verdict != pluginmeta.PermissionDiffVerdictApprovalRequired ||
		body.Data.PermissionDiff.ReasonCode != pluginmeta.PermissionDiffReasonSecretPermissionAdded ||
		body.Data.PermissionDiff.Summary.Added != 2 {
		t.Fatalf("permission diff = %+v, want secret approval with two added permissions", body.Data.PermissionDiff)
	}
	if body.Data.PermissionDiff.Added[1].Name != "https://api.example.com/v1" {
		t.Fatalf("network permission = %+v, want sanitized URL", body.Data.PermissionDiff.Added[1])
	}
	if body.Data.Trust.Verdict != pluginmeta.TrustVerdictTrusted || !body.Data.Trust.ChecksumPresent || body.Data.Trust.SignaturePresent {
		t.Fatalf("trust = %+v, want checksum-trusted unsigned preview", body.Data.Trust)
	}
	if entries, err := os.ReadDir(pluginDir); err != nil || len(entries) != 0 {
		t.Fatalf("plugin dir entries = %v err=%v, want no package mutation", entries, err)
	}
}

func TestAdminPluginPermissionDiffUpdatePreviewComparesInstalledAndCandidatePermissions(t *testing.T) {
	pluginDir := t.TempDir()
	currentArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.update", "Permission Update", "1.0.0", `
permissions:
  data:
    read:
      - usage
`),
	})
	if _, err := pluginmeta.NewRuntime(pluginDir).InstallZipArchive(currentArchive, pluginmeta.InstallOptions{}); err != nil {
		t.Fatalf("install current package: %v", err)
	}
	candidateArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.update", "Permission Update", "1.1.0", `
permissions:
  data:
    read:
      - usage
      - request_body
`),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(candidateArchive)
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.permission.update/permission-diff", map[string]any{
		"download_url":    upstream.URL + "/update.zip",
		"checksum_sha256": adminSHA256Hex(candidateArchive),
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST update permission diff: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginPermissionDiffPreviewResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode permission diff response: %v", err)
	}
	if body.Data.Operation != "update" || body.Data.CurrentVersion != "1.0.0" || body.Data.CandidateVersion != "1.1.0" {
		t.Fatalf("response versions = %+v", body.Data)
	}
	if body.Data.PermissionDiff.Summary.Added != 1 || body.Data.PermissionDiff.Summary.Unchanged != 1 ||
		body.Data.PermissionDiff.ReasonCode != pluginmeta.PermissionDiffReasonSensitivePermissionAdded {
		t.Fatalf("permission diff = %+v, want one sensitive addition and one unchanged", body.Data.PermissionDiff)
	}
	data, err := os.ReadFile(filepath.Join(pluginDir, "tokenhub.permission.update", "plugin.yaml"))
	if err != nil {
		t.Fatalf("read installed package: %v", err)
	}
	if strings.Contains(string(data), "1.1.0") {
		t.Fatalf("preview mutated installed package manifest: %s", data)
	}
}

func TestAdminPluginPermissionDiffUpdatePreviewRejectsPluginIDMismatch(t *testing.T) {
	pluginDir := t.TempDir()
	currentArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.current", "Permission Current", "1.0.0", ""),
	})
	if _, err := pluginmeta.NewRuntime(pluginDir).InstallZipArchive(currentArchive, pluginmeta.InstallOptions{}); err != nil {
		t.Fatalf("install current package: %v", err)
	}
	candidateArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.other", "Permission Other", "1.1.0", ""),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(candidateArchive)
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.permission.current/permission-diff", map[string]any{
		"download_url":    upstream.URL + "/other.zip",
		"checksum_sha256": adminSHA256Hex(candidateArchive),
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "plugin_id_mismatch") {
		t.Fatalf("POST mismatched permission diff: expected 400 plugin_id_mismatch, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginPermissionDiffUpdatePreviewRejectsMissingInstalledPlugin(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.permission.missing/permission-diff", map[string]any{
		"download_url":    "https://plugins.example/missing.zip",
		"checksum_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, "dev_admin_token")
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body, "plugin_not_found") {
		t.Fatalf("POST missing permission diff: expected 404 plugin_not_found, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginPermissionDiffPreviewVerifiesSignedMarketplaceArtifact(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.signed", "Permission Signed", "1.0.0", ""),
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
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/permission-diff", map[string]any{
		"download_url":         upstream.URL + "/signed.zip",
		"checksum_sha256":      adminSHA256Hex(archive),
		"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
		"signature_url":        upstream.URL + "/signed.zip.sig?token=signature-secret",
		"signature_key_id":     keyID,
		"signature_public_key": publicKey,
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST signed permission diff: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginPermissionDiffPreviewResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode permission diff response: %v", err)
	}
	if body.Data.Trust.Verdict != pluginmeta.TrustVerdictTrusted || !body.Data.Trust.SignaturePresent ||
		body.Data.Trust.SignatureAlgorithm != pluginmeta.PluginSignatureAlgorithmEd25519 || body.Data.Trust.SignatureKeyID != keyID {
		t.Fatalf("trust = %+v, want signed trusted artifact", body.Data.Trust)
	}
}

func TestAdminPluginPermissionDiffPreviewDoesNotLeakURLsChecksumsCommandsPathsAndSecrets(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginPermissionManifest("tokenhub.permission.redacted", "Permission Redacted", "1.0.0", `
entry:
  backend:
    protocol: stdio-json-v1
    command: ./bin/run --token command-secret
permissions:
  network:
    allow:
      - https://user:pass@api.example.com/v1?token=network-secret#fragment
`),
	})
	keyID, publicKey, signature := adminPluginArtifactSignatureForTest(t, archive)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redacted.zip":
			_, _ = w.Write(archive)
		case "/redacted.zip.sig":
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	server.pluginInstallClient = upstream.Client()
	checksum := adminSHA256Hex(archive)

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/permission-diff", map[string]any{
		"download_url":         upstream.URL + "/redacted.zip?token=download-secret",
		"checksum_sha256":      checksum,
		"trust_policy":         string(pluginmeta.TrustPolicySignedMarketplace),
		"signature_url":        upstream.URL + "/redacted.zip.sig?token=signature-secret",
		"signature_key_id":     keyID,
		"signature_public_key": publicKey,
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST redacted permission diff: expected 200, got %d: %s", response.Code, response.Body)
	}
	body := response.Body
	for _, forbidden := range []string{
		"download-secret",
		"signature-secret",
		"network-secret",
		"command-secret",
		checksum,
		publicKey,
		"redacted.zip",
		"redacted.zip.sig",
		"./bin/run",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("permission diff response leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "https://api.example.com/v1") {
		t.Fatalf("permission diff response = %s, want sanitized network target", body)
	}
}

func adminPluginPermissionManifest(id string, name string, version string, extra string) string {
	return `
schema_version: 1
id: ` + id + `
name: ` + name + `
version: ` + version + `
tokenhub:
  plugin_api: v1
kinds:
  - extension
` + extra
}
