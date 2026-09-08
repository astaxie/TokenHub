package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMarketplaceDescriptorsFromChannelIndexProjectsReleaseMetadata(t *testing.T) {
	index := validMarketplaceIndexForTest()
	release := &index.Plugins[0].Releases[0]
	release.Artifacts[0] = MarketplaceArtifact{
		Target: "any",
		URL:    "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_any.zip",
		Size:   2048,
		SHA256: strings.Repeat("6", 64),
		Signature: MarketplaceArtifactSignature{
			Algorithm: PluginSignatureAlgorithmEd25519,
			KeyID:     "tokenhub-official-2026",
			URL:       "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_any.zip.sig",
		},
	}

	descriptors, err := MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil {
		t.Fatalf("project channel index: %v", err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("descriptors = %d, want 1", len(descriptors))
	}
	descriptor := descriptors[0]
	if descriptor.ID != "tokenhub.openai-codex" || descriptor.Name != "OpenAI Codex Subscription" || descriptor.Version != "1.0.0" || descriptor.Source != SourceMarketplace {
		t.Fatalf("descriptor identity = %+v", descriptor)
	}
	if descriptor.Distribution == nil ||
		descriptor.Distribution.DownloadURL != "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_any.zip" ||
		descriptor.Distribution.ChecksumSHA256 != strings.Repeat("6", 64) ||
		descriptor.Distribution.SignatureAlgorithm != PluginSignatureAlgorithmEd25519 ||
		descriptor.Distribution.SignatureKeyID != "tokenhub-official-2026" {
		t.Fatalf("descriptor distribution = %+v", descriptor.Distribution)
	}
	metadata := descriptor.Marketplace
	if metadata == nil || metadata.Summary != "Adds Codex subscription capabilities." || len(metadata.Categories) != 2 {
		t.Fatalf("marketplace metadata = %+v", metadata)
	}
	if metadata.Publisher == nil || !metadata.Publisher.Verified || metadata.Publisher.ID != "tokenhub-official" {
		t.Fatalf("publisher = %+v", metadata.Publisher)
	}
	if metadata.Compatibility == nil || metadata.Compatibility.Verdict != MarketplaceCompatibilityCompatible {
		t.Fatalf("compatibility = %+v", metadata.Compatibility)
	}
	if len(metadata.ReleaseNotes) != 1 ||
		metadata.ReleaseNotes[0].URL != "https://plugins.example/tokenhub.openai-codex/1.0.0/release-notes.md" ||
		len(metadata.ReleaseNotes[0].Items) != 1 ||
		metadata.ReleaseNotes[0].Items[0] != "sha256:"+strings.Repeat("e", 64) {
		t.Fatalf("release notes = %+v", metadata.ReleaseNotes)
	}
}

func TestMarketplaceDescriptorsFromChannelIndexPrefersExactRuntimeArtifact(t *testing.T) {
	index := validMarketplaceIndexForTest()
	release := &index.Plugins[0].Releases[0]
	artifact := release.Artifacts[0]
	artifact.Target = "any"
	artifact.URL = "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_any.zip"
	artifact.Signature.URL = artifact.URL + ".sig"
	exact := artifact
	exact.Target = runtime.GOOS + "-" + runtime.GOARCH
	exact.URL = "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_" + exact.Target + ".zip"
	exact.Signature.URL = exact.URL + ".sig"
	release.Artifacts = []MarketplaceArtifact{artifact, exact}

	descriptors, err := MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil {
		t.Fatalf("project channel index: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].Distribution == nil || descriptors[0].Distribution.DownloadURL != exact.URL {
		t.Fatalf("descriptor distribution = %+v, want exact runtime artifact %s", descriptors, exact.URL)
	}
}

func TestMarketplaceDescriptorsFromChannelIndexRejectsOtherPlatformArtifact(t *testing.T) {
	index := validMarketplaceIndexForTest()
	release := &index.Plugins[0].Releases[0]
	otherTarget := "linux-amd64"
	if runtime.GOOS+"-"+runtime.GOARCH == otherTarget {
		otherTarget = "darwin-arm64"
	}
	release.Artifacts[0].Target = otherTarget

	descriptors, err := MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil {
		t.Fatalf("project channel index: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].Distribution != nil {
		t.Fatalf("descriptor distribution = %+v, want no installable artifact", descriptors)
	}
	compatibility := descriptors[0].Marketplace.Compatibility
	if compatibility == nil || compatibility.Verdict != MarketplaceCompatibilityIncompatible {
		t.Fatalf("descriptor compatibility = %+v, want incompatible", compatibility)
	}
}

func TestMarketplaceDescriptorsFromChannelIndexFlagsReviewAdvisories(t *testing.T) {
	index := validMarketplaceIndexForTest()
	index.Plugins[0].Releases[0].Review = MarketplaceReleaseReview{
		Status:           MarketplaceReviewRestricted,
		ReviewedAt:       "2026-08-29T07:30:00Z",
		PermissionChange: "sensitive",
	}

	descriptors, err := MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil {
		t.Fatalf("project restricted channel index: %v", err)
	}
	metadata := descriptors[0].Marketplace
	if metadata == nil || metadata.Compatibility == nil || metadata.Compatibility.Verdict != MarketplaceCompatibilityNeedsReview {
		t.Fatalf("compatibility = %+v", metadata)
	}
	if len(metadata.Advisories) != 1 ||
		metadata.Advisories[0].Severity != "high" ||
		metadata.Advisories[0].PublishedAt != "2026-08-29T07:30:00Z" {
		t.Fatalf("advisories = %+v", metadata.Advisories)
	}
}

func TestMarketplaceDescriptorsFromChannelIndexPrefersHighestApprovedRelease(t *testing.T) {
	index := validMarketplaceIndexForTest()
	approved := index.Plugins[0].Releases[0]
	revoked := approved
	revoked.Artifacts = append([]MarketplaceArtifact(nil), approved.Artifacts...)
	revoked.Version = "1.1.0"
	revoked.Artifacts[0].URL = "https://plugins.example/tokenhub.openai-codex/1.1.0/tokenhub.openai-codex_1.1.0_linux-amd64.zip"
	revoked.Artifacts[0].Signature.URL = "https://plugins.example/tokenhub.openai-codex/1.1.0/tokenhub.openai-codex_1.1.0_linux-amd64.zip.sig"
	revoked.PublishedAt = "2026-08-30T07:00:00Z"
	revoked.Review = MarketplaceReleaseReview{
		Status:           MarketplaceReviewRevoked,
		ReviewedAt:       "2026-08-30T07:30:00Z",
		PermissionChange: "none",
	}
	index.Plugins[0].Latest = "1.1.0"
	index.Plugins[0].Releases = append(index.Plugins[0].Releases, revoked)

	descriptors, err := MarketplaceDescriptorsFromChannelIndex(index)
	if err != nil {
		t.Fatalf("project channel index: %v", err)
	}
	if descriptors[0].Version != "1.0.0" {
		t.Fatalf("descriptor version = %q, want highest approved release", descriptors[0].Version)
	}
}

func TestMarketplaceListConsumesChannelIndexFromOfflineMirror(t *testing.T) {
	indexBytes, err := json.Marshal(validMarketplaceIndexForTest())
	if err != nil {
		t.Fatalf("marshal channel index: %v", err)
	}
	mirrorPath := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(mirrorPath, indexBytes, 0o644); err != nil {
		t.Fatalf("write offline mirror: %v", err)
	}

	descriptors, err := NewMarketplace(mirrorPath, nil).List(context.Background())
	if err != nil {
		t.Fatalf("list offline mirror: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].ID != "tokenhub.openai-codex" || descriptors[0].Distribution == nil {
		t.Fatalf("offline mirror descriptors = %+v", descriptors)
	}
}

func TestDecodeOnlineMarketplaceIndexSanitizesLegacyFormats(t *testing.T) {
	descriptor := Descriptor{
		ID:      "tokenhub.legacy-marketplace",
		Name:    "Legacy Marketplace Plugin",
		Version: "1.0.0",
		Source:  SourceMarketplace,
		Distribution: &Distribution{
			DownloadURL:    "https://attacker.example/plugin.zip",
			ChecksumSHA256: strings.Repeat("a", 64),
		},
		Marketplace: &MarketplaceMetadata{
			Publisher:     &MarketplacePublisher{ID: "untrusted", Verified: true},
			Compatibility: &MarketplaceCompatibility{Verdict: MarketplaceCompatibilityCompatible},
		},
	}
	formats := map[string]any{
		"descriptor array": []Descriptor{descriptor},
		"plugins object":   MarketplaceIndex{Plugins: []Descriptor{descriptor}},
	}
	for name, format := range formats {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(format)
			if err != nil {
				t.Fatalf("marshal legacy index: %v", err)
			}
			descriptors, err := decodeOnlineMarketplaceIndex(data)
			if err != nil {
				t.Fatalf("decode online legacy index: %v", err)
			}
			if len(descriptors) != 1 {
				t.Fatalf("descriptors = %d, want 1", len(descriptors))
			}
			got := descriptors[0]
			if got.Distribution != nil {
				t.Fatalf("unverified distribution = %+v, want nil", got.Distribution)
			}
			if got.Marketplace == nil || got.Marketplace.Publisher == nil || got.Marketplace.Publisher.Verified {
				t.Fatalf("unverified publisher = %+v", got.Marketplace)
			}
			if got.Marketplace.Compatibility == nil || got.Marketplace.Compatibility.Verdict != MarketplaceCompatibilityUnknown {
				t.Fatalf("unverified compatibility = %+v", got.Marketplace.Compatibility)
			}
		})
	}
}

func TestDecodeMarketplaceIndexRejectsInvalidChannelIndexBeforeLegacyFallback(t *testing.T) {
	data := []byte(`{"schema_version":2,"repository_id":"tokenhub-official-marketplace","channel":"stable","sequence":1,"plugins":[{"id":"tokenhub.blank"}]}`)

	_, err := decodeMarketplaceIndex(data)
	if err == nil {
		t.Fatal("invalid channel index decoded through legacy fallback")
	}
	if !strings.Contains(err.Error(), "unsupported marketplace index schema_version 2") {
		t.Fatalf("error = %q", err.Error())
	}
}
