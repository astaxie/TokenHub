package plugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDecodeMarketplaceIndexAcceptsGoldenFixtures(t *testing.T) {
	for _, fixture := range []string{"official-valid", "third-party-valid"} {
		t.Run(fixture, func(t *testing.T) {
			index, err := DecodeMarketplaceIndex(readMarketplaceFixture(t, fixture))
			if err != nil {
				t.Fatalf("decode marketplace index: %v", err)
			}
			if index.SchemaVersion != MarketplaceIndexSchemaVersion {
				t.Fatalf("schema version = %d", index.SchemaVersion)
			}
			if len(index.Plugins) != 1 {
				t.Fatalf("plugins = %d, want 1", len(index.Plugins))
			}
			canonical, err := CanonicalMarketplaceIndexJSON(index)
			if err != nil {
				t.Fatalf("canonical marketplace index: %v", err)
			}
			canonicalAgain, err := CanonicalMarketplaceIndexJSON(index)
			if err != nil {
				t.Fatalf("canonical marketplace index again: %v", err)
			}
			if string(canonical) != string(canonicalAgain) {
				t.Fatalf("canonical JSON is not deterministic:\n%s\n%s", canonical, canonicalAgain)
			}
		})
	}
}

func TestDecodeMarketplaceIndexRejectsInvalidGoldenFixtures(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    string
	}{
		{fixture: "duplicate-release", want: "release \"1.0.0\" is duplicated"},
		{fixture: "incompatible-api", want: "unsupported tokenhub.plugin_api"},
		{fixture: "mutable-url", want: "artifact url must be immutable HTTPS URL"},
		{fixture: "permission-escalation", want: "permission increase requires restricted review status"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			_, err := DecodeMarketplaceIndex(readMarketplaceFixture(t, tc.fixture))
			if err == nil {
				t.Fatal("invalid marketplace index decoded successfully")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateMarketplaceIndexRejectsAdversarialRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*MarketplaceChannelIndex)
		want   string
	}{
		{
			name: "duplicate plugin id",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins = append(index.Plugins, index.Plugins[0])
			},
			want: "plugin \"tokenhub.openai-codex\" is duplicated",
		},
		{
			name: "official namespace mismatch",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].ID = "com.example.codex"
			},
			want: "official plugins must use the tokenhub namespace",
		},
		{
			name: "third party reserved namespace",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Origin = MarketplaceOriginThirdParty
				index.Plugins[0].Publisher = MarketplaceIndexPublisher{ID: "example-corp", Verification: MarketplacePublisherVerified}
			},
			want: "third-party plugins cannot use the reserved tokenhub namespace",
		},
		{
			name: "unsupported index schema",
			mutate: func(index *MarketplaceChannelIndex) {
				index.SchemaVersion = 2
			},
			want: "unsupported marketplace index schema_version 2",
		},
		{
			name: "unsupported manifest schema",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Compatibility.ManifestSchemaVersion = 2
			},
			want: "unsupported manifest_schema_version 2",
		},
		{
			name: "invalid semver",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Version = "v1"
			},
			want: "version must be SemVer",
		},
		{
			name: "invalid core range",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Compatibility.MinCore = "0.8"
			},
			want: "tokenhub.min_core is invalid",
		},
		{
			name: "unsupported target",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Artifacts[0].Target = "solaris-amd64"
			},
			want: "unsupported target",
		},
		{
			name: "non HTTPS artifact URL",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Artifacts[0].URL = "http://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_linux-amd64.zip"
			},
			want: "artifact url must be an HTTPS URL",
		},
		{
			name: "checksum format",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Artifacts[0].SHA256 = strings.Repeat("A", 64)
			},
			want: "artifact sha256 must be a lowercase SHA-256",
		},
		{
			name: "unsupported required feature",
			mutate: func(index *MarketplaceChannelIndex) {
				index.Plugins[0].Releases[0].Compatibility.RequiredFeatures = []string{"telepathy_v9"}
			},
			want: "unsupported required feature",
		},
		{
			name: "plugin count limit",
			mutate: func(index *MarketplaceChannelIndex) {
				for len(index.Plugins) <= MaxMarketplacePlugins {
					plugin := validMarketplacePluginForTest("com.example.plugin-"+string(rune('a'+len(index.Plugins)%26))+"-"+strings.Repeat("x", len(index.Plugins)/26+1), MarketplaceOriginThirdParty, MarketplacePublisherVerified)
					index.Plugins = append(index.Plugins, plugin)
				}
			},
			want: "marketplace index cannot contain more than",
		},
		{
			name: "release count limit",
			mutate: func(index *MarketplaceChannelIndex) {
				for len(index.Plugins[0].Releases) <= MaxMarketplaceReleases {
					release := index.Plugins[0].Releases[0]
					release.Version = "1.0." + strings.Repeat("1", len(index.Plugins[0].Releases))
					index.Plugins[0].Releases = append(index.Plugins[0].Releases, release)
				}
			},
			want: "plugin cannot contain more than",
		},
		{
			name: "artifact count limit",
			mutate: func(index *MarketplaceChannelIndex) {
				for len(index.Plugins[0].Releases[0].Artifacts) <= MaxMarketplaceArtifacts {
					artifact := index.Plugins[0].Releases[0].Artifacts[0]
					artifact.Target = "linux-arm64"
					if len(index.Plugins[0].Releases[0].Artifacts)%2 == 0 {
						artifact.Target = "darwin-arm64"
					}
					artifact.SHA256 = strings.Repeat("7", 64)
					index.Plugins[0].Releases[0].Artifacts = append(index.Plugins[0].Releases[0].Artifacts, artifact)
				}
			},
			want: "release cannot contain more than",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := validMarketplaceIndexForTest()
			tc.mutate(&index)
			err := ValidateMarketplaceIndex(index)
			if err == nil {
				t.Fatal("marketplace index validation succeeded")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateMarketplaceReleaseManifestAcceptsMatchingPackageManifest(t *testing.T) {
	release := validMarketplaceIndexForTest().Plugins[0].Releases[0]
	manifest := parseMarketplaceManifestForTest(t, "tokenhub.openai-codex", release, release.Artifacts[0])

	if err := ValidateMarketplaceReleaseManifest("tokenhub.openai-codex", release, manifest); err != nil {
		t.Fatalf("validate marketplace release manifest: %v", err)
	}
}

func TestValidateMarketplaceReleaseManifestRejectsMismatches(t *testing.T) {
	release := validMarketplaceIndexForTest().Plugins[0].Releases[0]
	for _, tc := range []struct {
		name       string
		pluginID   string
		release    MarketplaceIndexRelease
		artifact   MarketplaceArtifact
		manifestID string
		want       string
	}{
		{
			name:       "plugin id mismatch",
			pluginID:   "tokenhub.openai-codex",
			release:    release,
			artifact:   release.Artifacts[0],
			manifestID: "tokenhub.other",
			want:       "does not match manifest id",
		},
		{
			name:       "version mismatch",
			pluginID:   "tokenhub.openai-codex",
			release:    withMarketplaceReleaseVersion(release, "1.1.0"),
			artifact:   release.Artifacts[0],
			manifestID: "tokenhub.openai-codex",
			want:       "does not match manifest version",
		},
		{
			name:       "distribution conflict",
			pluginID:   "tokenhub.openai-codex",
			release:    release,
			artifact:   withMarketplaceArtifactChecksum(release.Artifacts[0], strings.Repeat("9", 64)),
			manifestID: "tokenhub.openai-codex",
			want:       "package distribution claim conflicts",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := parseMarketplaceManifestForTest(t, tc.manifestID, release, tc.artifact)
			if err := ValidateMarketplaceReleaseManifest(tc.pluginID, tc.release, manifest); err == nil {
				t.Fatal("marketplace release manifest validation succeeded")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestCanonicalMarketplaceIndexJSONSortsSetLikeFields(t *testing.T) {
	index := validMarketplaceIndexForTest()
	index.Plugins = append(index.Plugins, validMarketplacePluginForTest("com.example.trace", MarketplaceOriginThirdParty, MarketplacePublisherVerified))
	index.Plugins[0].Listing.Categories = []string{"subscription", "provider"}
	index.Plugins[0].Releases[0].Compatibility.RequiredFeatures = []string{
		PluginFeatureStdioJSONV1,
		PluginFeatureMarketplaceDistribution,
	}
	index.Plugins[0].Releases[0].Artifacts = append(index.Plugins[0].Releases[0].Artifacts, MarketplaceArtifact{
		Target: "any",
		URL:    "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_any.zip",
		Size:   512,
		SHA256: strings.Repeat("6", 64),
		Signature: MarketplaceArtifactSignature{
			Algorithm: PluginSignatureAlgorithmEd25519,
			KeyID:     "tokenhub-release-2026",
			URL:       "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_any.zip.sig",
		},
	})

	canonical, err := CanonicalMarketplaceIndexJSON(index)
	if err != nil {
		t.Fatalf("canonical marketplace index: %v", err)
	}
	var decoded MarketplaceChannelIndex
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	if decoded.Plugins[0].ID != "com.example.trace" || decoded.Plugins[1].ID != "tokenhub.openai-codex" {
		t.Fatalf("plugins are not sorted by id: %+v", decoded.Plugins)
	}
	categories := decoded.Plugins[1].Listing.Categories
	if len(categories) != 2 || categories[0] != "provider" || categories[1] != "subscription" {
		t.Fatalf("categories are not sorted: %+v", categories)
	}
	features := decoded.Plugins[1].Releases[0].Compatibility.RequiredFeatures
	if len(features) != 2 || features[0] != PluginFeatureMarketplaceDistribution || features[1] != PluginFeatureStdioJSONV1 {
		t.Fatalf("features are not sorted: %+v", features)
	}
	artifacts := decoded.Plugins[1].Releases[0].Artifacts
	if len(artifacts) != 2 || artifacts[0].Target != "any" || artifacts[1].Target != "linux-amd64" {
		t.Fatalf("artifacts are not sorted by target: %+v", artifacts)
	}
}

func TestDecodeMarketplaceIndexRejectsOversizedOrDeepIndexes(t *testing.T) {
	oversized := strings.Repeat(" ", MaxMarketplaceIndexBytes+1)
	if _, err := DecodeMarketplaceIndex([]byte(oversized)); err == nil {
		t.Fatal("oversized marketplace index decoded successfully")
	}

	deep := strings.Repeat("[", MaxMarketplaceJSONDepth+1) + strings.Repeat("]", MaxMarketplaceJSONDepth+1)
	if _, err := DecodeMarketplaceIndex([]byte(deep)); err == nil {
		t.Fatal("deep marketplace index decoded successfully")
	}
}

func TestDecodeMarketplaceIndexRejectsSecretMaterial(t *testing.T) {
	data := readMarketplaceFixture(t, "official-valid")
	data = []byte(strings.Replace(string(data), `"plugins": [`, `"private_key": "-----BEGIN PRIVATE KEY-----", "plugins": [`, 1))
	if _, err := DecodeMarketplaceIndex(data); err == nil {
		t.Fatal("marketplace index with secret material decoded successfully")
	}
}

func TestDecodeMarketplaceIndexRejectsTrailingJSON(t *testing.T) {
	data := append(readMarketplaceFixture(t, "official-valid"), []byte(`{}`)...)
	if _, err := DecodeMarketplaceIndex(data); err == nil {
		t.Fatal("marketplace index with trailing JSON decoded successfully")
	}
}

func readMarketplaceFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/marketplace/" + name + "/index.json")
	if err != nil {
		t.Fatalf("read marketplace fixture %s: %v", name, err)
	}
	return data
}

func validMarketplaceIndexForTest() MarketplaceChannelIndex {
	return MarketplaceChannelIndex{
		SchemaVersion: MarketplaceIndexSchemaVersion,
		RepositoryID:  "tokenhub-official-marketplace",
		Channel:       MarketplaceChannelStable,
		Sequence:      1042,
		GeneratedAt:   "2026-08-29T08:00:00Z",
		ExpiresAt:     "2026-09-05T08:00:00Z",
		Revocations: &MarketplaceObjectRef{
			URL:    "https://plugins.example/revocations/2026-08-29.json",
			SHA256: strings.Repeat("a", 64),
		},
		Plugins: []MarketplaceIndexPlugin{
			validMarketplacePluginForTest("tokenhub.openai-codex", MarketplaceOriginOfficial, MarketplacePublisherOfficial),
		},
	}
}

func validMarketplacePluginForTest(id string, origin MarketplaceOrigin, verification MarketplacePublisherVerification) MarketplaceIndexPlugin {
	publisherID := "tokenhub-official"
	if origin == MarketplaceOriginThirdParty {
		publisherID = "example-corp"
	}
	return MarketplaceIndexPlugin{
		ID:     id,
		Origin: origin,
		Publisher: MarketplaceIndexPublisher{
			ID:           publisherID,
			Verification: verification,
		},
		Listing: MarketplaceIndexListing{
			Name:       "OpenAI Codex Subscription",
			Summary:    "Adds Codex subscription capabilities.",
			Categories: []string{"provider", "subscription"},
		},
		Latest: "1.0.0",
		Releases: []MarketplaceIndexRelease{{
			Version:      "1.0.0",
			Channel:      MarketplaceChannelStable,
			PublishedAt:  "2026-08-29T07:00:00Z",
			SourceCommit: "0123456789abcdef0123456789abcdef01234567",
			Compatibility: MarketplaceReleaseCompatibility{
				PluginAPI:             CurrentPluginAPI,
				ManifestSchemaVersion: PluginManifestSchemaVersion,
				MinCore:               CurrentCoreVersion,
				RequiredFeatures:      []string{PluginFeatureMarketplaceDistribution},
			},
			ManifestSHA256:    strings.Repeat("b", 64),
			PermissionsSHA256: strings.Repeat("c", 64),
			Artifacts: []MarketplaceArtifact{{
				Target: "linux-amd64",
				URL:    "https://plugins.example/" + id + "/1.0.0/" + id + "_1.0.0_linux-amd64.zip",
				Size:   1024,
				SHA256: strings.Repeat("d", 64),
				Signature: MarketplaceArtifactSignature{
					Algorithm: PluginSignatureAlgorithmEd25519,
					KeyID:     publisherID + "-2026",
					URL:       "https://plugins.example/" + id + "/1.0.0/" + id + "_1.0.0_linux-amd64.zip.sig",
				},
			}},
			ReleaseNotes: &MarketplaceObjectRef{
				URL:    "https://plugins.example/" + id + "/1.0.0/release-notes.md",
				SHA256: strings.Repeat("e", 64),
			},
			Review: MarketplaceReleaseReview{
				Status:           MarketplaceReviewApproved,
				ReviewedAt:       "2026-08-29T07:30:00Z",
				PermissionChange: "none",
			},
		}},
	}
}

func parseMarketplaceManifestForTest(t *testing.T, id string, release MarketplaceIndexRelease, artifact MarketplaceArtifact) Manifest {
	t.Helper()
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: ` + id + `
name: Marketplace Test Plugin
version: 1.0.0
tokenhub:
  plugin_api: ` + release.Compatibility.PluginAPI + `
  min_core: ` + release.Compatibility.MinCore + `
distribution:
  download_url: ` + artifact.URL + `
  checksum_sha256: ` + artifact.SHA256 + `
  signature_url: ` + artifact.Signature.URL + `
  signature_algorithm: ` + artifact.Signature.Algorithm + `
  signature_key_id: ` + artifact.Signature.KeyID + `
kinds:
  - extension
`))
	if err != nil {
		t.Fatalf("parse marketplace manifest: %v", err)
	}
	return manifest
}

func withMarketplaceReleaseVersion(release MarketplaceIndexRelease, version string) MarketplaceIndexRelease {
	release.Version = version
	return release
}

func withMarketplaceArtifactChecksum(artifact MarketplaceArtifact, checksum string) MarketplaceArtifact {
	artifact.SHA256 = checksum
	return artifact
}
