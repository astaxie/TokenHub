package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginMarketplaceGetAnnotatesInstalledPlugins(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "kimi"), `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	available := pluginmeta.Descriptor{
		ID:      "tokenhub.marketplace.kimi",
		Name:    "Marketplace Kimi",
		Version: "1.1.0",
		Source:  pluginmeta.SourceMarketplace,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindExtension},
		Distribution: &pluginmeta.Distribution{
			DownloadURL:    "https://plugins.example/kimi-1.1.0.zip",
			ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"plugins": []pluginmeta.Descriptor{available}})
	}))
	defer upstream.Close()

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir, PluginMarketplaceURL: upstream.URL + "/index.json"})
	server.pluginMarketplaceClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-marketplace", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugin-marketplace: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginMarketplaceResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode marketplace response: %v", err)
	}
	if !body.Data.Available || body.Data.SourceURL != upstream.URL+"/index.json" {
		t.Fatalf("marketplace response = %+v", body.Data)
	}
	if len(body.Data.Plugins) != 1 || !body.Data.Plugins[0].Installed || !body.Data.Plugins[0].UpdateAvailable || body.Data.Plugins[0].InstalledVersion != "1.0.0" {
		t.Fatalf("marketplace plugin annotation = %+v", body.Data.Plugins)
	}
}

func TestAdminPluginMarketplaceGetConsumesChannelIndexMetadata(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "kimi"), `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := adminMarketplaceChannelIndexForTest(r.Host)
		if err := json.NewEncoder(w).Encode(index); err != nil {
			t.Fatalf("write channel index: %v", err)
		}
	}))
	defer upstream.Close()

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir, PluginMarketplaceURL: upstream.URL + "/index.json"})
	server.pluginMarketplaceClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-marketplace", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugin-marketplace channel index: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginMarketplaceResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode marketplace response: %v", err)
	}
	if len(body.Data.Plugins) != 1 || !body.Data.Plugins[0].Installed || !body.Data.Plugins[0].UpdateAvailable {
		t.Fatalf("marketplace plugin annotation = %+v", body.Data.Plugins)
	}
	plugin := body.Data.Plugins[0].Plugin
	if plugin.Version != "1.1.0" || plugin.Distribution == nil ||
		plugin.Distribution.ChecksumSHA256 != strings.Repeat("4", 64) ||
		plugin.Distribution.SignatureAlgorithm != pluginmeta.PluginSignatureAlgorithmEd25519 ||
		plugin.Distribution.SignatureKeyID != "tokenhub-official-2026" {
		t.Fatalf("marketplace plugin distribution = %+v", plugin)
	}
	if plugin.Marketplace == nil || plugin.Marketplace.Compatibility == nil ||
		plugin.Marketplace.Compatibility.Verdict != pluginmeta.MarketplaceCompatibilityNeedsReview {
		t.Fatalf("marketplace compatibility = %+v", plugin.Marketplace)
	}
	if plugin.Marketplace.Publisher == nil || !plugin.Marketplace.Publisher.Verified || plugin.Marketplace.Publisher.ID != "tokenhub-official" {
		t.Fatalf("marketplace publisher = %+v", plugin.Marketplace.Publisher)
	}
	if len(plugin.Marketplace.Advisories) != 1 || plugin.Marketplace.Advisories[0].Severity != "high" {
		t.Fatalf("marketplace advisories = %+v", plugin.Marketplace.Advisories)
	}
	if len(plugin.Marketplace.ReleaseNotes) != 1 ||
		plugin.Marketplace.ReleaseNotes[0].URL != "https://"+upstream.Listener.Addr().String()+"/tokenhub.marketplace.kimi/1.1.0/release-notes.md" {
		t.Fatalf("marketplace release notes = %+v", plugin.Marketplace.ReleaseNotes)
	}
}

func TestAdminPluginMarketplaceGetReportsSourceErrors(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "kimi"), `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	upstream := httptest.NewTLSServer(http.NotFoundHandler())
	defer upstream.Close()

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir, PluginMarketplaceURL: upstream.URL + "/index.json"})
	server.pluginMarketplaceClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-marketplace", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugin-marketplace: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginMarketplaceResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode marketplace response: %v", err)
	}
	if body.Data.Error == "" || body.Data.Available {
		t.Fatalf("expected source error in marketplace response, got %+v", body.Data)
	}
}

func adminMarketplaceChannelIndexForTest(host string) pluginmeta.MarketplaceChannelIndex {
	baseURL := "https://" + host + "/tokenhub.marketplace.kimi/1.1.0/"
	return pluginmeta.MarketplaceChannelIndex{
		SchemaVersion: pluginmeta.MarketplaceIndexSchemaVersion,
		RepositoryID:  "tokenhub-official-marketplace",
		Channel:       pluginmeta.MarketplaceChannelStable,
		Sequence:      2048,
		GeneratedAt:   "2026-08-30T01:00:00Z",
		ExpiresAt:     "2026-09-06T01:00:00Z",
		Plugins: []pluginmeta.MarketplaceIndexPlugin{{
			ID:     "tokenhub.marketplace.kimi",
			Origin: pluginmeta.MarketplaceOriginOfficial,
			Publisher: pluginmeta.MarketplaceIndexPublisher{
				ID:           "tokenhub-official",
				Verification: pluginmeta.MarketplacePublisherOfficial,
			},
			Listing: pluginmeta.MarketplaceIndexListing{
				Name:       "Marketplace Kimi",
				Summary:    "Adds Kimi subscription capabilities.",
				Categories: []string{"provider", "subscription"},
			},
			Latest: "1.1.0",
			Releases: []pluginmeta.MarketplaceIndexRelease{{
				Version:      "1.1.0",
				Channel:      pluginmeta.MarketplaceChannelStable,
				PublishedAt:  "2026-08-30T01:30:00Z",
				SourceCommit: "0123456789abcdef0123456789abcdef01234567",
				Compatibility: pluginmeta.MarketplaceReleaseCompatibility{
					PluginAPI:             pluginmeta.CurrentPluginAPI,
					ManifestSchemaVersion: pluginmeta.PluginManifestSchemaVersion,
					MinCore:               pluginmeta.CurrentCoreVersion,
					RequiredFeatures:      []string{pluginmeta.PluginFeatureMarketplaceDistribution},
				},
				ManifestSHA256:    strings.Repeat("2", 64),
				PermissionsSHA256: strings.Repeat("3", 64),
				Artifacts: []pluginmeta.MarketplaceArtifact{{
					Target: "any",
					URL:    baseURL + "tokenhub.marketplace.kimi_1.1.0_any.zip",
					Size:   1024,
					SHA256: strings.Repeat("4", 64),
					Signature: pluginmeta.MarketplaceArtifactSignature{
						Algorithm: pluginmeta.PluginSignatureAlgorithmEd25519,
						KeyID:     "tokenhub-official-2026",
						URL:       baseURL + "tokenhub.marketplace.kimi_1.1.0_any.zip.sig",
					},
				}},
				ReleaseNotes: &pluginmeta.MarketplaceObjectRef{
					URL:    baseURL + "release-notes.md",
					SHA256: strings.Repeat("5", 64),
				},
				Review: pluginmeta.MarketplaceReleaseReview{
					Status:           pluginmeta.MarketplaceReviewRestricted,
					ReviewedAt:       "2026-08-30T02:00:00Z",
					PermissionChange: "sensitive",
				},
			}},
		}},
	}
}
