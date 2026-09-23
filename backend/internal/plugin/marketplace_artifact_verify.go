package plugin

import (
	"fmt"
	"strings"
)

type MarketplaceArtifactVerificationInput struct {
	Artifact    []byte
	Signature   []byte
	KeyID       string
	TrustedKeys []MarketplaceTrustedKey
}

func VerifyMarketplaceArtifactSignature(input MarketplaceArtifactVerificationInput) (MarketplaceSignatureVerification, error) {
	keyID := strings.TrimSpace(input.KeyID)
	if !marketplaceSafeToken(keyID) {
		return MarketplaceSignatureVerification{}, fmt.Errorf("marketplace artifact signature key_id is required and must be a safe token")
	}
	verification, err := VerifyMarketplaceSignature(input.Artifact, MarketplaceArtifactMediaType, input.Signature, input.TrustedKeys)
	if err != nil {
		return MarketplaceSignatureVerification{}, err
	}
	if verification.KeyID != keyID {
		return MarketplaceSignatureVerification{}, fmt.Errorf("marketplace artifact signature key_id %q does not match expected %q", verification.KeyID, keyID)
	}
	return verification, nil
}
