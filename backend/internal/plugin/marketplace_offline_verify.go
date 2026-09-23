package plugin

import (
	"fmt"
	"strings"
	"time"
)

type MarketplaceOfflineArtifact struct {
	Data      []byte
	Signature []byte
}

type MarketplaceOfflineVerificationInput struct {
	IndexBytes               []byte
	IndexSignatureBytes      []byte
	RevocationBytes          []byte
	RevocationSignatureBytes []byte
	Artifacts                []MarketplaceOfflineArtifact
	TrustedKeys              []MarketplaceTrustedKey
	Now                      time.Time
}

type MarketplaceOfflineVerification struct {
	RepositoryID          string
	Channel               MarketplaceChannel
	Sequence              int64
	Plugins               int
	ArtifactsVerified     int
	RevocationsChecked    int
	IndexSignature        MarketplaceSignatureVerification
	RevocationSignature   *MarketplaceSignatureVerification
	RevocationFeedPresent bool
}

func VerifyMarketplaceOffline(input MarketplaceOfflineVerificationInput) (MarketplaceOfflineVerification, error) {
	index, err := DecodeMarketplaceIndex(input.IndexBytes)
	if err != nil {
		return MarketplaceOfflineVerification{}, err
	}
	canonicalIndex, err := CanonicalMarketplaceIndexJSON(index)
	if err != nil {
		return MarketplaceOfflineVerification{}, err
	}
	indexSignature, err := VerifyMarketplaceSignature(canonicalIndex, MarketplaceIndexMediaType, input.IndexSignatureBytes, input.TrustedKeys)
	if err != nil {
		return MarketplaceOfflineVerification{}, fmt.Errorf("verify marketplace index signature: %w", err)
	}
	now := input.Now
	if now.IsZero() {
		return MarketplaceOfflineVerification{}, fmt.Errorf("marketplace offline verification time is required")
	}
	now = now.UTC()
	if err := validateMarketplaceOfflineExpiry("marketplace index", index.ExpiresAt, now); err != nil {
		return MarketplaceOfflineVerification{}, err
	}
	artifactsBySHA, err := marketplaceOfflineArtifactsBySHA(input.Artifacts)
	if err != nil {
		return MarketplaceOfflineVerification{}, err
	}
	result := MarketplaceOfflineVerification{
		RepositoryID:      strings.TrimSpace(index.RepositoryID),
		Channel:           index.Channel,
		Sequence:          index.Sequence,
		Plugins:           len(index.Plugins),
		IndexSignature:    indexSignature,
		ArtifactsVerified: 0,
	}
	for _, item := range index.Plugins {
		for _, release := range item.Releases {
			for _, artifact := range release.Artifacts {
				offline, ok := artifactsBySHA[strings.TrimSpace(artifact.SHA256)]
				if !ok {
					return MarketplaceOfflineVerification{}, fmt.Errorf("marketplace artifact %s %s is missing offline bytes", strings.TrimSpace(item.ID), strings.TrimSpace(artifact.Target))
				}
				verification, err := VerifyMarketplaceSignature(offline.Data, MarketplaceArtifactMediaType, offline.Signature, input.TrustedKeys)
				if err != nil {
					return MarketplaceOfflineVerification{}, fmt.Errorf("verify marketplace artifact %s %s signature: %w", strings.TrimSpace(item.ID), strings.TrimSpace(artifact.Target), err)
				}
				if verification.KeyID != strings.TrimSpace(artifact.Signature.KeyID) {
					return MarketplaceOfflineVerification{}, fmt.Errorf("marketplace artifact %s %s signature key_id %q does not match index key_id %q", strings.TrimSpace(item.ID), strings.TrimSpace(artifact.Target), verification.KeyID, artifact.Signature.KeyID)
				}
				result.ArtifactsVerified++
			}
		}
	}
	revocationSignature, revocationsChecked, err := verifyMarketplaceOfflineRevocations(index, indexSignature.KeyID, input, now)
	if err != nil {
		return MarketplaceOfflineVerification{}, err
	}
	result.RevocationSignature = revocationSignature
	result.RevocationFeedPresent = revocationSignature != nil
	result.RevocationsChecked = revocationsChecked
	return result, nil
}

func verifyMarketplaceOfflineRevocations(index MarketplaceChannelIndex, indexKeyID string, input MarketplaceOfflineVerificationInput, now time.Time) (*MarketplaceSignatureVerification, int, error) {
	if index.Revocations == nil && len(input.RevocationBytes) == 0 && len(input.RevocationSignatureBytes) == 0 {
		return nil, 0, nil
	}
	if index.Revocations != nil && len(input.RevocationBytes) == 0 {
		return nil, 0, fmt.Errorf("marketplace revocation feed is required by index")
	}
	if len(input.RevocationBytes) == 0 || len(input.RevocationSignatureBytes) == 0 {
		return nil, 0, fmt.Errorf("marketplace revocation feed and signature must be provided together")
	}
	if index.Revocations != nil {
		digest := MarketplaceSHA256Hex(input.RevocationBytes)
		if digest != strings.TrimSpace(index.Revocations.SHA256) {
			return nil, 0, fmt.Errorf("marketplace revocation feed digest mismatch")
		}
	}
	verification, err := VerifyMarketplaceSignature(input.RevocationBytes, MarketplaceRevocationMediaType, input.RevocationSignatureBytes, input.TrustedKeys)
	if err != nil {
		return nil, 0, fmt.Errorf("verify marketplace revocation signature: %w", err)
	}
	feed, err := DecodeMarketplaceRevocations(input.RevocationBytes)
	if err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(feed.RepositoryID) != strings.TrimSpace(index.RepositoryID) {
		return nil, 0, fmt.Errorf("marketplace revocation repository_id %q does not match index repository_id %q", feed.RepositoryID, index.RepositoryID)
	}
	if err := validateMarketplaceOfflineExpiry("marketplace revocation feed", feed.ExpiresAt, now); err != nil {
		return nil, 0, err
	}
	matches := MarketplaceRevocationMatches(index, indexKeyID, feed)
	if len(matches) > 0 {
		match := matches[0]
		return nil, 0, fmt.Errorf("marketplace revocation %s matches %s: %s", match.RevocationID, match.Target, match.Reason)
	}
	return &verification, len(feed.Revocations), nil
}

func validateMarketplaceOfflineExpiry(name string, expiresAt string, now time.Time) error {
	expires, err := parseMarketplaceTime("expires_at", expiresAt)
	if err != nil {
		return err
	}
	if !expires.After(now) {
		return fmt.Errorf("%s expired at %s", name, expires.Format(time.RFC3339))
	}
	return nil
}

func marketplaceOfflineArtifactsBySHA(artifacts []MarketplaceOfflineArtifact) (map[string]MarketplaceOfflineArtifact, error) {
	bySHA := map[string]MarketplaceOfflineArtifact{}
	for index, artifact := range artifacts {
		if len(artifact.Data) == 0 {
			return nil, fmt.Errorf("marketplace offline artifact[%d] bytes are required", index)
		}
		if len(artifact.Signature) == 0 {
			return nil, fmt.Errorf("marketplace offline artifact[%d] signature is required", index)
		}
		digest := MarketplaceSHA256Hex(artifact.Data)
		if _, ok := bySHA[digest]; ok {
			return nil, fmt.Errorf("marketplace offline artifact %s is duplicated", digest)
		}
		bySHA[digest] = artifact
	}
	return bySHA, nil
}
