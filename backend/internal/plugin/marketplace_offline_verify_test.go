package plugin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMarketplaceOfflineVerifyAcceptsSignedIndexAndArtifacts(t *testing.T) {
	input := signedMarketplaceOfflineInputForTest(t, nil)
	result, err := VerifyMarketplaceOffline(input)
	if err != nil {
		t.Fatalf("verify marketplace offline: %v", err)
	}
	if result.Plugins != 1 || result.ArtifactsVerified != 1 || result.IndexSignature.KeyID != "tokenhub-index-2026" {
		t.Fatalf("result = %+v", result)
	}
}

func TestMarketplaceOfflineVerifyRejectsSignatureAndExpiryFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*MarketplaceOfflineVerificationInput)
		want   string
	}{
		{
			name: "tampered canonical index",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				var index MarketplaceChannelIndex
				if err := json.Unmarshal(input.IndexBytes, &index); err != nil {
					t.Fatalf("decode index: %v", err)
				}
				index.Sequence++
				data, err := json.Marshal(index)
				if err != nil {
					t.Fatalf("encode index: %v", err)
				}
				input.IndexBytes = data
			},
			want: "subject digest mismatch",
		},
		{
			name: "wrong index key",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				input.TrustedKeys[0].KeyID = "missing-index-key"
			},
			want: "trusted key",
		},
		{
			name: "expired index",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				input.Now = time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
			},
			want: "marketplace index expired",
		},
		{
			name: "tampered artifact",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				input.Artifacts[0].Data = []byte("tampered artifact")
			},
			want: "missing offline bytes",
		},
		{
			name: "wrong artifact signature domain",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				key := input.Artifacts[0].Data
				_, _, privateKey := marketplaceSigningKeyForTest(t, "tokenhub-artifact-2026")
				envelope := marketplaceSignatureEnvelopeForTest(t, key, MarketplaceIndexMediaType, "tokenhub-artifact-2026", privateKey)
				input.Artifacts[0].Signature = encodeMarketplaceSignatureEnvelopeForTest(t, envelope)
			},
			want: "subject media type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := signedMarketplaceOfflineInputForTest(t, nil)
			tc.mutate(&input)
			_, err := VerifyMarketplaceOffline(input)
			if err == nil {
				t.Fatal("invalid offline marketplace verification succeeded")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestMarketplaceOfflineVerifyRejectsRevokedTargets(t *testing.T) {
	for _, tc := range []struct {
		name       string
		revocation func(MarketplaceChannelIndex) MarketplaceRevocation
		want       string
	}{
		{
			name: "artifact digest",
			revocation: func(index MarketplaceChannelIndex) MarketplaceRevocation {
				return MarketplaceRevocation{ID: "artifact-revoked", Reason: "artifact compromised", CreatedAt: "2026-08-30T08:01:00Z", ArtifactSHA256: index.Plugins[0].Releases[0].Artifacts[0].SHA256}
			},
			want: "artifact:",
		},
		{
			name: "index key",
			revocation: func(index MarketplaceChannelIndex) MarketplaceRevocation {
				return MarketplaceRevocation{ID: "index-key-revoked", Reason: "index key retired", CreatedAt: "2026-08-30T08:01:00Z", IndexKeyID: "tokenhub-index-2026"}
			},
			want: "index_key:",
		},
		{
			name: "publisher key",
			revocation: func(index MarketplaceChannelIndex) MarketplaceRevocation {
				return MarketplaceRevocation{ID: "publisher-key-revoked", Reason: "publisher key retired", CreatedAt: "2026-08-30T08:01:00Z", PublisherKeyID: index.Plugins[0].Releases[0].Artifacts[0].Signature.KeyID}
			},
			want: "publisher_key:",
		},
		{
			name: "plugin version",
			revocation: func(index MarketplaceChannelIndex) MarketplaceRevocation {
				return MarketplaceRevocation{ID: "version-revoked", Reason: "version yanked", CreatedAt: "2026-08-30T08:01:00Z", PublisherID: index.Plugins[0].Publisher.ID, PluginID: index.Plugins[0].ID, Version: index.Plugins[0].Releases[0].Version}
			},
			want: "plugin:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := signedMarketplaceOfflineInputForTest(t, tc.revocation)
			_, err := VerifyMarketplaceOffline(input)
			if err == nil {
				t.Fatal("revoked offline marketplace verification succeeded")
			}
			if !strings.Contains(err.Error(), "marketplace revocation") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want revocation target %q", err.Error(), tc.want)
			}
		})
	}
}

func TestMarketplaceOfflineVerifyRejectsInvalidRevocationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*MarketplaceOfflineVerificationInput)
		want   string
	}{
		{
			name: "digest mismatch",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				input.RevocationBytes = []byte(strings.Replace(string(input.RevocationBytes), "version yanked", "version blocked", 1))
			},
			want: "revocation feed digest mismatch",
		},
		{
			name: "wrong signature domain",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				_, _, privateKey := marketplaceSigningKeyForTest(t, "tokenhub-index-2026")
				envelope := marketplaceSignatureEnvelopeForTest(t, input.RevocationBytes, MarketplaceIndexMediaType, "tokenhub-index-2026", privateKey)
				input.RevocationSignatureBytes = encodeMarketplaceSignatureEnvelopeForTest(t, envelope)
			},
			want: "verify marketplace revocation signature",
		},
		{
			name: "expired revocation feed",
			mutate: func(input *MarketplaceOfflineVerificationInput) {
				resignExpiredRevocationFeedForTest(t, input)
			},
			want: "marketplace revocation feed expired",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := signedMarketplaceOfflineInputForTest(t, func(index MarketplaceChannelIndex) MarketplaceRevocation {
				return MarketplaceRevocation{ID: "version-revoked", Reason: "version yanked", CreatedAt: "2026-08-30T08:01:00Z", PluginID: index.Plugins[0].ID, Version: index.Plugins[0].Releases[0].Version}
			})
			tc.mutate(&input)
			_, err := VerifyMarketplaceOffline(input)
			if err == nil {
				t.Fatal("invalid revocation evidence verified")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func resignExpiredRevocationFeedForTest(t *testing.T, input *MarketplaceOfflineVerificationInput) {
	t.Helper()
	var index MarketplaceChannelIndex
	if err := json.Unmarshal(input.IndexBytes, &index); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	var feed MarketplaceRevocationFeed
	if err := json.Unmarshal(input.RevocationBytes, &feed); err != nil {
		t.Fatalf("decode revocation feed: %v", err)
	}
	feed.GeneratedAt = "2026-08-30T07:00:00Z"
	feed.ExpiresAt = "2026-08-30T07:59:59Z"
	revocationBytes, err := json.Marshal(feed)
	if err != nil {
		t.Fatalf("encode revocation feed: %v", err)
	}
	index.Revocations.SHA256 = MarketplaceSHA256Hex(revocationBytes)
	indexKeyID, indexPublicKey, indexPrivateKey := marketplaceSigningKeyForTest(t, "tokenhub-index-2026")
	canonicalIndex, err := CanonicalMarketplaceIndexJSON(index)
	if err != nil {
		t.Fatalf("canonical marketplace index: %v", err)
	}
	input.IndexBytes = canonicalIndex
	input.IndexSignatureBytes = encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, canonicalIndex, MarketplaceIndexMediaType, indexKeyID, indexPrivateKey))
	input.RevocationBytes = revocationBytes
	input.RevocationSignatureBytes = encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, revocationBytes, MarketplaceRevocationMediaType, indexKeyID, indexPrivateKey))
	input.TrustedKeys[0] = MarketplaceTrustedKey{KeyID: indexKeyID, PublicKey: indexPublicKey}
}

func signedMarketplaceOfflineInputForTest(t *testing.T, revocation func(MarketplaceChannelIndex) MarketplaceRevocation) MarketplaceOfflineVerificationInput {
	t.Helper()
	indexKeyID, indexPublicKey, indexPrivateKey := marketplaceSigningKeyForTest(t, "tokenhub-index-2026")
	artifactKeyID, artifactPublicKey, artifactPrivateKey := marketplaceSigningKeyForTest(t, "tokenhub-artifact-2026")
	artifactBytes := []byte("test-only plugin archive bytes")
	index := validMarketplaceIndexForTest()
	index.ExpiresAt = "2026-09-05T08:00:00Z"
	index.Revocations = nil
	index.Plugins[0].Releases[0].Artifacts[0].SHA256 = MarketplaceSHA256Hex(artifactBytes)
	index.Plugins[0].Releases[0].Artifacts[0].Signature.KeyID = artifactKeyID
	canonicalIndex, err := CanonicalMarketplaceIndexJSON(index)
	if err != nil {
		t.Fatalf("canonical marketplace index: %v", err)
	}
	indexSignature := encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, canonicalIndex, MarketplaceIndexMediaType, indexKeyID, indexPrivateKey))
	artifactSignature := encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, artifactBytes, MarketplaceArtifactMediaType, artifactKeyID, artifactPrivateKey))

	input := MarketplaceOfflineVerificationInput{
		IndexBytes:          canonicalIndex,
		IndexSignatureBytes: indexSignature,
		Artifacts: []MarketplaceOfflineArtifact{{
			Data:      artifactBytes,
			Signature: artifactSignature,
		}},
		TrustedKeys: []MarketplaceTrustedKey{
			{KeyID: indexKeyID, PublicKey: indexPublicKey},
			{KeyID: artifactKeyID, PublicKey: artifactPublicKey},
		},
		Now: time.Date(2026, 8, 30, 8, 0, 0, 0, time.UTC),
	}
	if revocation == nil {
		return input
	}
	feed := MarketplaceRevocationFeed{
		SchemaVersion: MarketplaceRevocationSchemaVersion,
		RepositoryID:  index.RepositoryID,
		GeneratedAt:   "2026-08-30T08:00:00Z",
		ExpiresAt:     "2026-09-06T08:00:00Z",
		Revocations:   []MarketplaceRevocation{revocation(index)},
	}
	revocationBytes, err := json.Marshal(feed)
	if err != nil {
		t.Fatalf("encode revocation feed: %v", err)
	}
	var indexWithRevocations MarketplaceChannelIndex
	if err := json.Unmarshal(canonicalIndex, &indexWithRevocations); err != nil {
		t.Fatalf("decode canonical index: %v", err)
	}
	indexWithRevocations.Revocations = &MarketplaceObjectRef{
		URL:    "https://plugins.example/revocations/2026-08-30.json",
		SHA256: MarketplaceSHA256Hex(revocationBytes),
	}
	canonicalIndex, err = CanonicalMarketplaceIndexJSON(indexWithRevocations)
	if err != nil {
		t.Fatalf("canonical index with revocations: %v", err)
	}
	input.IndexBytes = canonicalIndex
	input.IndexSignatureBytes = encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, canonicalIndex, MarketplaceIndexMediaType, indexKeyID, indexPrivateKey))
	input.RevocationBytes = revocationBytes
	input.RevocationSignatureBytes = encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, revocationBytes, MarketplaceRevocationMediaType, indexKeyID, indexPrivateKey))
	return input
}
