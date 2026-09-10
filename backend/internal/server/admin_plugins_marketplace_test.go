package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginsGetExposesSafeMarketplaceMetadata(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	err := server.pluginRegistry.Register(pluginmeta.Descriptor{
		ID:      "tokenhub.marketplace.rich",
		Name:    "Rich Marketplace",
		Version: "1.0.0",
		Source:  pluginmeta.SourceMarketplace,
		Status:  pluginmeta.StatusEnabled,
		Distribution: &pluginmeta.Distribution{
			MarketplaceURL: "https://plugins.example/tokenhub.marketplace.rich?token=distribution-secret",
			RepositoryURL:  "file:///Users/asta/private/plugin",
			DownloadURL:    "https://plugins.example/download/rich.zip?token=download-secret",
			ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SignatureURL:   "https://plugins.example/signature/rich.sig?token=signature-secret",
			HomepageURL:    "https://plugins.example/rich#credential-secret",
			License:        "Apache-2.0",
		},
		Marketplace: &pluginmeta.MarketplaceMetadata{
			Summary:    "Adds rich marketplace presentation fields.",
			Categories: []string{"provider", "privacy"},
			Screenshots: []pluginmeta.MarketplaceScreenshot{{
				URL:          "https://cdn.example/rich.png?X-Amz-Signature=signed-secret",
				ThumbnailURL: "file:///Users/asta/private/thumb.png",
				Alt:          "Rich plugin overview",
				Caption:      "Dashboard preview",
				Locale:       "en-US",
				Width:        1280,
				Height:       720,
			}},
			Localizations: map[string]pluginmeta.MarketplaceLocalization{
				"en-US": {
					Name:         "Marketplace Plugin",
					Summary:      "Shows marketplace metadata",
					Description:  "Used for plugin marketplace presentation",
					ReleaseNotes: "Initial release",
				},
			},
			Compatibility: &pluginmeta.MarketplaceCompatibility{
				Verdict: pluginmeta.MarketplaceCompatibilityNeedsReview,
				Badges: []pluginmeta.MarketplaceCompatibilityBadge{{
					ID:    "core",
					Label: "Needs review",
					Tone:  "warning",
					URL:   "http://plugins.example/insecure-badge",
				}},
			},
			Publisher: &pluginmeta.MarketplacePublisher{
				ID:         "tokenhub",
				Name:       "TokenHub",
				URL:        "https://publisher.example/tokenhub?token=publisher-secret",
				SupportURL: "file:///Users/asta/private/support",
				ContactURL: "https://publisher.example/contact#contact-secret",
				Verified:   true,
			},
			Advisories: []pluginmeta.MarketplaceAdvisory{{
				ID:          "ADV-1",
				Severity:    "medium",
				Title:       "Review before enabling",
				URL:         "https://plugins.example/advisories/ADV-1?token=advisory-secret",
				PublishedAt: "2026-08-01",
				UpdatedAt:   "2026-08-02",
			}},
			ReleaseNotes: []pluginmeta.MarketplaceReleaseNote{{
				Version: "1.0.0",
				Date:    "2026-08-01",
				Title:   "Initial release",
				Notes:   "Adds marketplace metadata.",
				URL:     "https://plugins.example/releases/1.0.0?token=release-secret",
				Items:   []string{"metadata", "badges"},
			}},
		},
		Kinds: []pluginmeta.Kind{pluginmeta.KindExtension},
	})
	if err != nil {
		t.Fatalf("register marketplace plugin: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugins: expected 200, got %d: %s", response.Code, response.Body)
	}
	for _, secret := range []string{
		"distribution-secret",
		"download-secret",
		"signature-secret",
		"signed-secret",
		"credential-secret",
		"publisher-secret",
		"contact-secret",
		"advisory-secret",
		"release-secret",
		"file:///Users/asta",
		"checksum_sha256",
		"download_url",
		"signature_url",
	} {
		if strings.Contains(response.Body, secret) {
			t.Fatalf("GET /api/admin/plugins leaked %q in response: %s", secret, response.Body)
		}
	}

	var body struct {
		Data []adminPluginDescriptorResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode plugin response: %v", err)
	}
	plugin, ok := adminPluginByID(body.Data, "tokenhub.marketplace.rich")
	if !ok {
		t.Fatalf("marketplace plugin missing from response: %+v", body.Data)
	}
	if plugin.Distribution == nil ||
		plugin.Distribution.MarketplaceURL != "https://plugins.example/tokenhub.marketplace.rich" ||
		plugin.Distribution.RepositoryURL != "" ||
		plugin.Distribution.HomepageURL != "https://plugins.example/rich" ||
		plugin.Distribution.License != "Apache-2.0" {
		t.Fatalf("sanitized distribution = %+v", plugin.Distribution)
	}
	if plugin.Marketplace == nil {
		t.Fatal("marketplace metadata missing from admin plugin response")
	}
	metadata := plugin.Marketplace
	if metadata.Summary != "Adds rich marketplace presentation fields." || len(metadata.Categories) != 2 {
		t.Fatalf("marketplace summary/categories = %+v", metadata)
	}
	if len(metadata.Screenshots) != 1 ||
		metadata.Screenshots[0].URL != "https://cdn.example/rich.png" ||
		metadata.Screenshots[0].ThumbnailURL != "" ||
		metadata.Screenshots[0].Alt != "Rich plugin overview" ||
		metadata.Screenshots[0].Width != 1280 {
		t.Fatalf("sanitized screenshots = %+v", metadata.Screenshots)
	}
	if localization := metadata.Localizations["en-US"]; localization.Name != "Marketplace Plugin" || localization.ReleaseNotes != "Initial release" {
		t.Fatalf("marketplace localizations = %+v", metadata.Localizations)
	}
	if metadata.Compatibility == nil ||
		metadata.Compatibility.Verdict != pluginmeta.MarketplaceCompatibilityNeedsReview ||
		len(metadata.Compatibility.Badges) != 1 ||
		metadata.Compatibility.Badges[0].URL != "" {
		t.Fatalf("marketplace compatibility = %+v", metadata.Compatibility)
	}
	if metadata.Publisher == nil ||
		metadata.Publisher.URL != "https://publisher.example/tokenhub" ||
		metadata.Publisher.SupportURL != "" ||
		metadata.Publisher.ContactURL != "https://publisher.example/contact" ||
		!metadata.Publisher.Verified {
		t.Fatalf("marketplace publisher = %+v", metadata.Publisher)
	}
	if len(metadata.Advisories) != 1 || metadata.Advisories[0].URL != "https://plugins.example/advisories/ADV-1" {
		t.Fatalf("marketplace advisories = %+v", metadata.Advisories)
	}
	if len(metadata.ReleaseNotes) != 1 || metadata.ReleaseNotes[0].URL != "https://plugins.example/releases/1.0.0" || len(metadata.ReleaseNotes[0].Items) != 2 {
		t.Fatalf("marketplace release notes = %+v", metadata.ReleaseNotes)
	}
}

func adminPluginByID(items []adminPluginDescriptorResponse, id string) (adminPluginDescriptorResponse, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return adminPluginDescriptorResponse{}, false
}
