package plugin

import (
	"strings"
	"testing"
)

func TestVerifyMarketplaceArtifactSignature(t *testing.T) {
	keyID, publicKey, privateKey := marketplaceSigningKeyForTest(t, "tokenhub-artifact-2026")
	wrongKeyID, wrongPublicKey, _ := marketplaceSigningKeyForTest(t, "wrong-artifact-2026")
	artifact := []byte("test-only plugin archive bytes")
	signature := encodeMarketplaceSignatureEnvelopeForTest(t, marketplaceSignatureEnvelopeForTest(t, artifact, MarketplaceArtifactMediaType, keyID, privateKey))

	verification, err := VerifyMarketplaceArtifactSignature(MarketplaceArtifactVerificationInput{
		Artifact:    artifact,
		Signature:   signature,
		KeyID:       keyID,
		TrustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
	})
	if err != nil {
		t.Fatalf("verify marketplace artifact signature: %v", err)
	}
	if verification.SubjectMedia != MarketplaceArtifactMediaType || verification.SubjectSHA256 != MarketplaceSHA256Hex(artifact) || verification.KeyID != keyID {
		t.Fatalf("verification = %+v", verification)
	}

	for _, tc := range []struct {
		name        string
		artifact    []byte
		signature   []byte
		keyID       string
		trustedKeys []MarketplaceTrustedKey
		want        string
	}{
		{
			name:        "tampered artifact",
			artifact:    []byte("tampered plugin archive bytes"),
			signature:   signature,
			keyID:       keyID,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "subject digest mismatch",
		},
		{
			name:        "missing expected key id",
			artifact:    artifact,
			signature:   signature,
			keyID:       "",
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "key_id is required",
		},
		{
			name:        "missing trusted key",
			artifact:    artifact,
			signature:   signature,
			keyID:       keyID,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: wrongKeyID, PublicKey: wrongPublicKey}},
			want:        "trusted key",
		},
		{
			name:        "wrong trusted key material",
			artifact:    artifact,
			signature:   signature,
			keyID:       keyID,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: wrongPublicKey}},
			want:        "verification failed",
		},
		{
			name:        "mismatched expected key id",
			artifact:    artifact,
			signature:   signature,
			keyID:       wrongKeyID,
			trustedKeys: []MarketplaceTrustedKey{{KeyID: keyID, PublicKey: publicKey}},
			want:        "does not match expected",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyMarketplaceArtifactSignature(MarketplaceArtifactVerificationInput{
				Artifact:    tc.artifact,
				Signature:   tc.signature,
				KeyID:       tc.keyID,
				TrustedKeys: tc.trustedKeys,
			})
			if err == nil {
				t.Fatal("invalid marketplace artifact signature verified")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}
