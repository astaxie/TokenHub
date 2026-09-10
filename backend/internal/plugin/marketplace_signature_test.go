package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
)

func TestMarketplaceSignatureVerifiesDomainSeparatedSubjects(t *testing.T) {
	keyID, publicKey, privateKey := marketplaceSigningKeyForTest(t, "tokenhub-release-2026")
	subject := []byte("signed marketplace subject")

	for _, mediaType := range []string{MarketplaceIndexMediaType, MarketplaceArtifactMediaType, MarketplaceRevocationMediaType} {
		t.Run(mediaType, func(t *testing.T) {
			envelope := marketplaceSignatureEnvelopeForTest(t, subject, mediaType, keyID, privateKey)
			encoded := encodeMarketplaceSignatureEnvelopeForTest(t, envelope)
			verification, err := VerifyMarketplaceSignature(subject, mediaType, encoded, []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}})
			if err != nil {
				t.Fatalf("verify marketplace signature: %v", err)
			}
			if verification.SubjectMedia != mediaType || verification.KeyID != keyID {
				t.Fatalf("verification = %+v", verification)
			}
		})
	}
}

func TestMarketplaceSignatureRejectsInvalidSubjectsAndKeys(t *testing.T) {
	keyID, publicKey, privateKey := marketplaceSigningKeyForTest(t, "tokenhub-release-2026")
	wrongKeyID, wrongPublicKey, _ := marketplaceSigningKeyForTest(t, "wrong-release-2026")
	subject := []byte("signed marketplace subject")
	envelope := marketplaceSignatureEnvelopeForTest(t, subject, MarketplaceIndexMediaType, keyID, privateKey)

	for _, tc := range []struct {
		name        string
		subject     []byte
		mediaType   string
		envelope    MarketplaceSignatureEnvelope
		trustedKeys []MarketplaceTrustedKey
		want        string
	}{
		{
			name:        "tampered subject digest",
			subject:     []byte("tampered"),
			mediaType:   MarketplaceIndexMediaType,
			envelope:    envelope,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "subject digest mismatch",
		},
		{
			name:        "wrong domain",
			subject:     subject,
			mediaType:   MarketplaceArtifactMediaType,
			envelope:    envelope,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "subject media type",
		},
		{
			name:        "missing key",
			subject:     subject,
			mediaType:   MarketplaceIndexMediaType,
			envelope:    envelope,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: wrongKeyID, PublicKey: wrongPublicKey}},
			want:        "trusted key",
		},
		{
			name:      "duplicate key id",
			subject:   subject,
			mediaType: MarketplaceIndexMediaType,
			envelope:  envelope,
			trustedKeys: []MarketplaceTrustedKey{
				{KeyID: keyID, PublicKey: publicKey},
				{KeyID: keyID, PublicKey: publicKey},
			},
			want: "duplicated",
		},
		{
			name:        "malformed public key",
			subject:     subject,
			mediaType:   MarketplaceIndexMediaType,
			envelope:    envelope,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: []byte("short")}},
			want:        "valid Ed25519 public key",
		},
		{
			name:        "wrong key material",
			subject:     subject,
			mediaType:   MarketplaceIndexMediaType,
			envelope:    envelope,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: wrongPublicKey}},
			want:        "verification failed",
		},
		{
			name:      "wrong algorithm",
			subject:   subject,
			mediaType: MarketplaceIndexMediaType,
			envelope: func() MarketplaceSignatureEnvelope {
				copy := envelope
				copy.Algorithm = "rsa"
				return copy
			}(),
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "algorithm must be ed25519",
		},
		{
			name:      "malformed signature",
			subject:   subject,
			mediaType: MarketplaceIndexMediaType,
			envelope: func() MarketplaceSignatureEnvelope {
				copy := envelope
				copy.SignatureBase64 = "not base64"
				return copy
			}(),
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "not valid base64",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyMarketplaceSignature(tc.subject, tc.mediaType, encodeMarketplaceSignatureEnvelopeForTest(t, tc.envelope), tc.trustedKeys)
			if err == nil {
				t.Fatal("invalid marketplace signature verified")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestDecodeMarketplaceSignatureEnvelopeRejectsUnknownFields(t *testing.T) {
	data := []byte(`{"schema_version":1,"subject_media_type":"application/vnd.tokenhub.marketplace.index.v1+json","subject_sha256":"` + strings.Repeat("a", 64) + `","algorithm":"ed25519","key_id":"tokenhub-release-2026","signature":"` + strings.Repeat("A", 88) + `","private_key":"nope"}`)
	if _, err := DecodeMarketplaceSignatureEnvelope(data); err == nil {
		t.Fatal("signature envelope with unknown field decoded successfully")
	}
}

func marketplaceSigningKeyForTest(t *testing.T, keyID string) (string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return keyID, publicKey, privateKey
}

func marketplaceSignatureEnvelopeForTest(t *testing.T, subject []byte, mediaType string, keyID string, privateKey ed25519.PrivateKey) MarketplaceSignatureEnvelope {
	t.Helper()
	envelope, err := NewMarketplaceSignatureEnvelope(subject, mediaType, keyID, privateKey)
	if err != nil {
		t.Fatalf("create marketplace signature envelope: %v", err)
	}
	return envelope
}

func encodeMarketplaceSignatureEnvelopeForTest(t *testing.T, envelope MarketplaceSignatureEnvelope) []byte {
	t.Helper()
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode marketplace signature envelope: %v", err)
	}
	return data
}
