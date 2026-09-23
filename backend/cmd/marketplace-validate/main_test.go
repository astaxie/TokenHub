package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tokenhub/backend/internal/plugin"
)

func TestRunValidatesMarketplaceIndexes(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{
		filepath.Join("..", "..", "internal", "plugin", "testdata", "marketplace", "official-valid", "index.json"),
		filepath.Join("..", "..", "internal", "plugin", "testdata", "marketplace", "third-party-valid", "index.json"),
	}, &stdout)
	if err != nil {
		t.Fatalf("validate marketplace indexes: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "official-valid") || !strings.Contains(output, "third-party-valid") {
		t.Fatalf("stdout = %q", output)
	}
}

func TestRunRejectsInvalidMarketplaceIndex(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{
		filepath.Join("..", "..", "internal", "plugin", "testdata", "marketplace", "incompatible-api", "index.json"),
	}, &stdout)
	if err == nil {
		t.Fatal("invalid marketplace index validated successfully")
	}
	if !strings.Contains(err.Error(), "unsupported tokenhub.plugin_api") {
		t.Fatalf("error = %q", err.Error())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty output for failed validation", stdout.String())
	}
}

func TestRunRequiresAtLeastOnePath(t *testing.T) {
	var stdout bytes.Buffer
	err := run(nil, &stdout)
	if err == nil {
		t.Fatal("run succeeded without input paths")
	}
	if !strings.Contains(err.Error(), "usage: marketplace-validate") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRunReportsReadFailures(t *testing.T) {
	var stdout bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.json")
	err := run([]string{missing}, &stdout)
	if err == nil {
		t.Fatal("missing marketplace index validated successfully")
	}
	if !strings.Contains(err.Error(), missing) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRunOfflineVerifiesSignedMarketplace(t *testing.T) {
	fixture := writeSignedMarketplaceForTest(t, nil)
	var stdout bytes.Buffer
	err := run(fixture.args(), &stdout)
	if err != nil {
		t.Fatalf("verify signed marketplace: %v", err)
	}
	if !strings.Contains(stdout.String(), "verified signed marketplace index (1 plugins, 1 artifacts)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunOfflineRejectsVerificationFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*signedMarketplaceFixture)
		want   string
	}{
		{
			name: "wrong key",
			mutate: func(fixture *signedMarketplaceFixture) {
				fixture.indexKeyArg = "missing=" + fixture.indexKeyArg[strings.Index(fixture.indexKeyArg, "=")+1:]
			},
			want: "trusted key",
		},
		{
			name: "expired",
			mutate: func(fixture *signedMarketplaceFixture) {
				fixture.now = "2026-09-06T08:00:00Z"
			},
			want: "marketplace index expired",
		},
		{
			name: "revoked artifact",
			mutate: func(fixture *signedMarketplaceFixture) {
				revocation := plugin.MarketplaceRevocation{
					ID:             "artifact-revoked",
					Reason:         "artifact compromised",
					CreatedAt:      "2026-08-30T08:01:00Z",
					ArtifactSHA256: fixture.artifactSHA256,
				}
				fixture.addRevocation(t, revocation)
			},
			want: "marketplace revocation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := writeSignedMarketplaceForTest(t, nil)
			tc.mutate(&fixture)
			var stdout bytes.Buffer
			err := run(fixture.args(), &stdout)
			if err == nil {
				t.Fatal("offline verification succeeded")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty output for failed verification", stdout.String())
			}
		})
	}
}

func TestRunOfflineRequiresExplicitInputs(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"--offline", "--index", "index.json"}, &stdout)
	if err == nil {
		t.Fatal("offline verification succeeded with missing inputs")
	}
	if !strings.Contains(err.Error(), "--index-signature is required") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestRunOfflineRequiresExplicitClock(t *testing.T) {
	fixture := writeSignedMarketplaceForTest(t, nil)
	args := fixture.args()
	args = args[:len(args)-2]
	var stdout bytes.Buffer
	err := run(args, &stdout)
	if err == nil {
		t.Fatal("offline verification succeeded without explicit clock")
	}
	if !strings.Contains(err.Error(), "--now is required") {
		t.Fatalf("error = %q", err.Error())
	}
}

type signedMarketplaceFixture struct {
	dir                     string
	indexPath               string
	indexSignaturePath      string
	artifactPath            string
	artifactSignaturePath   string
	revocationPath          string
	revocationSignaturePath string
	indexKeyArg             string
	artifactKeyArg          string
	now                     string
	artifactSHA256          string
	indexPrivateKey         ed25519.PrivateKey
	indexKeyID              string
	index                   plugin.MarketplaceChannelIndex
}

func (fixture signedMarketplaceFixture) args() []string {
	args := []string{
		"--offline",
		"--index", fixture.indexPath,
		"--index-signature", fixture.indexSignaturePath,
		"--key", fixture.indexKeyArg,
		"--key", fixture.artifactKeyArg,
		"--artifact", fixture.artifactPath,
		"--artifact-signature", fixture.artifactSignaturePath,
		"--now", fixture.now,
	}
	if fixture.revocationPath != "" {
		args = append(args, "--revocations", fixture.revocationPath, "--revocations-signature", fixture.revocationSignaturePath)
	}
	return args
}

func (fixture *signedMarketplaceFixture) addRevocation(t *testing.T, revocation plugin.MarketplaceRevocation) {
	t.Helper()
	feed := plugin.MarketplaceRevocationFeed{
		SchemaVersion: plugin.MarketplaceRevocationSchemaVersion,
		RepositoryID:  fixture.index.RepositoryID,
		GeneratedAt:   "2026-08-30T08:00:00Z",
		ExpiresAt:     "2026-09-06T08:00:00Z",
		Revocations:   []plugin.MarketplaceRevocation{revocation},
	}
	revocationBytes := marshalJSONForTest(t, feed)
	fixture.revocationPath = filepath.Join(fixture.dir, "revocations.json")
	writeFileForTest(t, fixture.revocationPath, revocationBytes)
	revocationSignature := marketplaceSignatureForTest(t, revocationBytes, plugin.MarketplaceRevocationMediaType, fixture.indexKeyID, fixture.indexPrivateKey)
	fixture.revocationSignaturePath = filepath.Join(fixture.dir, "revocations.json.sig")
	writeFileForTest(t, fixture.revocationSignaturePath, revocationSignature)
	fixture.index.Revocations = &plugin.MarketplaceObjectRef{
		URL:    "https://plugins.example/revocations/2026-08-30.json",
		SHA256: plugin.MarketplaceSHA256Hex(revocationBytes),
	}
	fixture.rewriteIndex(t)
}

func (fixture *signedMarketplaceFixture) rewriteIndex(t *testing.T) {
	t.Helper()
	canonical, err := plugin.CanonicalMarketplaceIndexJSON(fixture.index)
	if err != nil {
		t.Fatalf("canonical marketplace index: %v", err)
	}
	writeFileForTest(t, fixture.indexPath, canonical)
	writeFileForTest(t, fixture.indexSignaturePath, marketplaceSignatureForTest(t, canonical, plugin.MarketplaceIndexMediaType, fixture.indexKeyID, fixture.indexPrivateKey))
}

func writeSignedMarketplaceForTest(t *testing.T, mutate func(*plugin.MarketplaceChannelIndex)) signedMarketplaceFixture {
	t.Helper()
	dir := t.TempDir()
	indexPublicKey, indexPrivateKey := signingKeyForTest(t)
	artifactPublicKey, artifactPrivateKey := signingKeyForTest(t)
	indexKeyID := "tokenhub-index-2026"
	artifactKeyID := "tokenhub-artifact-2026"
	artifactBytes := []byte("test-only marketplace artifact")
	artifactSHA256 := plugin.MarketplaceSHA256Hex(artifactBytes)
	index := marketplaceIndexForCLITest(artifactSHA256, artifactKeyID)
	if mutate != nil {
		mutate(&index)
	}
	fixture := signedMarketplaceFixture{
		dir:                   dir,
		indexPath:             filepath.Join(dir, "index.json"),
		indexSignaturePath:    filepath.Join(dir, "index.json.sig"),
		artifactPath:          filepath.Join(dir, "plugin.zip"),
		artifactSignaturePath: filepath.Join(dir, "plugin.zip.sig"),
		indexKeyArg:           indexKeyID + "=" + base64.StdEncoding.EncodeToString(indexPublicKey),
		artifactKeyArg:        artifactKeyID + "=" + base64.StdEncoding.EncodeToString(artifactPublicKey),
		now:                   "2026-08-30T08:00:00Z",
		artifactSHA256:        artifactSHA256,
		indexPrivateKey:       indexPrivateKey,
		indexKeyID:            indexKeyID,
		index:                 index,
	}
	writeFileForTest(t, fixture.artifactPath, artifactBytes)
	writeFileForTest(t, fixture.artifactSignaturePath, marketplaceSignatureForTest(t, artifactBytes, plugin.MarketplaceArtifactMediaType, artifactKeyID, artifactPrivateKey))
	fixture.rewriteIndex(t)
	return fixture
}

func marketplaceIndexForCLITest(artifactSHA256 string, artifactKeyID string) plugin.MarketplaceChannelIndex {
	return plugin.MarketplaceChannelIndex{
		SchemaVersion: plugin.MarketplaceIndexSchemaVersion,
		RepositoryID:  "tokenhub-official-marketplace",
		Channel:       plugin.MarketplaceChannelStable,
		Sequence:      1042,
		GeneratedAt:   "2026-08-29T08:00:00Z",
		ExpiresAt:     "2026-09-05T08:00:00Z",
		Plugins: []plugin.MarketplaceIndexPlugin{{
			ID:     "tokenhub.openai-codex",
			Origin: plugin.MarketplaceOriginOfficial,
			Publisher: plugin.MarketplaceIndexPublisher{
				ID:           "tokenhub-official",
				Verification: plugin.MarketplacePublisherOfficial,
			},
			Listing: plugin.MarketplaceIndexListing{
				Name:       "OpenAI Codex Subscription",
				Summary:    "Adds Codex subscription capabilities.",
				Categories: []string{"provider", "subscription"},
			},
			Latest: "1.0.0",
			Releases: []plugin.MarketplaceIndexRelease{{
				Version:      "1.0.0",
				Channel:      plugin.MarketplaceChannelStable,
				PublishedAt:  "2026-08-29T07:00:00Z",
				SourceCommit: "0123456789abcdef0123456789abcdef01234567",
				Compatibility: plugin.MarketplaceReleaseCompatibility{
					PluginAPI:             plugin.CurrentPluginAPI,
					ManifestSchemaVersion: plugin.PluginManifestSchemaVersion,
					MinCore:               plugin.CurrentCoreVersion,
					RequiredFeatures:      []string{plugin.PluginFeatureMarketplaceDistribution},
				},
				ManifestSHA256:    strings.Repeat("b", 64),
				PermissionsSHA256: strings.Repeat("c", 64),
				Artifacts: []plugin.MarketplaceArtifact{{
					Target: "linux-amd64",
					URL:    "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_linux-amd64.zip",
					Size:   1024,
					SHA256: artifactSHA256,
					Signature: plugin.MarketplaceArtifactSignature{
						Algorithm: plugin.PluginSignatureAlgorithmEd25519,
						KeyID:     artifactKeyID,
						URL:       "https://plugins.example/tokenhub.openai-codex/1.0.0/tokenhub.openai-codex_1.0.0_linux-amd64.zip.sig",
					},
				}},
				Review: plugin.MarketplaceReleaseReview{
					Status:           plugin.MarketplaceReviewApproved,
					ReviewedAt:       "2026-08-29T07:30:00Z",
					PermissionChange: "none",
				},
			}},
		}},
	}
}

func marketplaceSignatureForTest(t *testing.T, subject []byte, mediaType string, keyID string, privateKey ed25519.PrivateKey) []byte {
	t.Helper()
	envelope, err := plugin.NewMarketplaceSignatureEnvelope(subject, mediaType, keyID, privateKey)
	if err != nil {
		t.Fatalf("create marketplace signature envelope: %v", err)
	}
	encoded, err := plugin.EncodeMarketplaceSignatureEnvelope(envelope)
	if err != nil {
		t.Fatalf("encode marketplace signature envelope: %v", err)
	}
	return encoded
}

func signingKeyForTest(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return publicKey, privateKey
}

func marshalJSONForTest(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}

func writeFileForTest(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
