package plugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	MarketplaceSignatureEnvelopeSchemaVersion = 1
	MaxMarketplaceSignatureEnvelopeBytes      = 16 << 10

	MarketplaceIndexMediaType       = "application/vnd.tokenhub.marketplace.index.v1+json"
	MarketplaceArtifactMediaType    = "application/vnd.tokenhub.plugin.artifact.v1+zip"
	MarketplaceRevocationMediaType  = "application/vnd.tokenhub.marketplace.revocations.v1+json"
	marketplaceIndexSignatureDomain = "tokenhub-marketplace-index-v1"
	marketplaceArtifactDomain       = "tokenhub-plugin-artifact-v1"
	marketplaceRevocationDomain     = "tokenhub-marketplace-revocations-v1"
)

type MarketplaceSignatureEnvelope struct {
	SchemaVersion   int    `json:"schema_version"`
	SubjectMedia    string `json:"subject_media_type"`
	SubjectSHA256   string `json:"subject_sha256"`
	Algorithm       string `json:"algorithm"`
	KeyID           string `json:"key_id"`
	SignatureBase64 string `json:"signature"`
}

type MarketplaceTrustedKey struct {
	KeyID     string
	PublicKey ed25519.PublicKey
}

type MarketplaceSignatureVerification struct {
	SubjectMedia  string
	SubjectSHA256 string
	Algorithm     string
	KeyID         string
}

func MarketplaceSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func NewMarketplaceSignatureEnvelope(subject []byte, subjectMedia string, keyID string, privateKey ed25519.PrivateKey) (MarketplaceSignatureEnvelope, error) {
	subjectMedia = strings.TrimSpace(subjectMedia)
	keyID = strings.TrimSpace(keyID)
	if _, err := marketplaceSignatureDomain(subjectMedia); err != nil {
		return MarketplaceSignatureEnvelope{}, err
	}
	if !marketplaceSafeToken(keyID) {
		return MarketplaceSignatureEnvelope{}, fmt.Errorf("marketplace signature key_id is required and must be a safe token")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return MarketplaceSignatureEnvelope{}, fmt.Errorf("marketplace signature private key must be Ed25519")
	}
	digest := MarketplaceSHA256Hex(subject)
	signature := ed25519.Sign(privateKey, marketplaceSignatureInput(subjectMedia, digest))
	return MarketplaceSignatureEnvelope{
		SchemaVersion:   MarketplaceSignatureEnvelopeSchemaVersion,
		SubjectMedia:    subjectMedia,
		SubjectSHA256:   digest,
		Algorithm:       PluginSignatureAlgorithmEd25519,
		KeyID:           keyID,
		SignatureBase64: base64.StdEncoding.EncodeToString(signature),
	}, nil
}

func EncodeMarketplaceSignatureEnvelope(envelope MarketplaceSignatureEnvelope) ([]byte, error) {
	if err := validateMarketplaceSignatureEnvelope(envelope); err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

func DecodeMarketplaceSignatureEnvelope(data []byte) (MarketplaceSignatureEnvelope, error) {
	if len(data) == 0 {
		return MarketplaceSignatureEnvelope{}, fmt.Errorf("marketplace signature envelope is required")
	}
	if len(data) > MaxMarketplaceSignatureEnvelopeBytes {
		return MarketplaceSignatureEnvelope{}, fmt.Errorf("marketplace signature envelope cannot exceed %d bytes", MaxMarketplaceSignatureEnvelopeBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope MarketplaceSignatureEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return MarketplaceSignatureEnvelope{}, fmt.Errorf("decode marketplace signature envelope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return MarketplaceSignatureEnvelope{}, fmt.Errorf("marketplace signature envelope contains trailing data")
	}
	return envelope, validateMarketplaceSignatureEnvelope(envelope)
}

func VerifyMarketplaceSignature(subject []byte, subjectMedia string, envelopeBytes []byte, keys []MarketplaceTrustedKey) (MarketplaceSignatureVerification, error) {
	envelope, err := DecodeMarketplaceSignatureEnvelope(envelopeBytes)
	if err != nil {
		return MarketplaceSignatureVerification{}, err
	}
	return VerifyMarketplaceSignatureEnvelope(subject, subjectMedia, envelope, keys)
}

func VerifyMarketplaceSignatureEnvelope(subject []byte, subjectMedia string, envelope MarketplaceSignatureEnvelope, keys []MarketplaceTrustedKey) (MarketplaceSignatureVerification, error) {
	subjectMedia = strings.TrimSpace(subjectMedia)
	if subjectMedia != strings.TrimSpace(envelope.SubjectMedia) {
		return MarketplaceSignatureVerification{}, fmt.Errorf("marketplace signature subject media type %q does not match expected %q", envelope.SubjectMedia, subjectMedia)
	}
	if _, err := marketplaceSignatureDomain(subjectMedia); err != nil {
		return MarketplaceSignatureVerification{}, err
	}
	if err := validateMarketplaceSignatureEnvelope(envelope); err != nil {
		return MarketplaceSignatureVerification{}, err
	}
	digest := MarketplaceSHA256Hex(subject)
	if subtle.ConstantTimeCompare([]byte(digest), []byte(strings.TrimSpace(envelope.SubjectSHA256))) != 1 {
		return MarketplaceSignatureVerification{}, fmt.Errorf("marketplace signature subject digest mismatch")
	}
	key, err := marketplaceTrustedKey(keys, envelope.KeyID)
	if err != nil {
		return MarketplaceSignatureVerification{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envelope.SignatureBase64))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return MarketplaceSignatureVerification{}, fmt.Errorf("marketplace signature is not valid base64 Ed25519 data")
	}
	if !ed25519.Verify(key, marketplaceSignatureInput(subjectMedia, digest), signature) {
		return MarketplaceSignatureVerification{}, fmt.Errorf("marketplace signature verification failed")
	}
	return MarketplaceSignatureVerification{
		SubjectMedia:  subjectMedia,
		SubjectSHA256: digest,
		Algorithm:     PluginSignatureAlgorithmEd25519,
		KeyID:         strings.TrimSpace(envelope.KeyID),
	}, nil
}

func validateMarketplaceSignatureEnvelope(envelope MarketplaceSignatureEnvelope) error {
	if envelope.SchemaVersion != MarketplaceSignatureEnvelopeSchemaVersion {
		return fmt.Errorf("unsupported marketplace signature schema_version %d", envelope.SchemaVersion)
	}
	if _, err := marketplaceSignatureDomain(envelope.SubjectMedia); err != nil {
		return err
	}
	if !marketplaceSHA256Pattern.MatchString(strings.TrimSpace(envelope.SubjectSHA256)) {
		return fmt.Errorf("marketplace signature subject_sha256 must be a lowercase SHA-256")
	}
	if strings.TrimSpace(envelope.Algorithm) != PluginSignatureAlgorithmEd25519 {
		return fmt.Errorf("marketplace signature algorithm must be ed25519")
	}
	if !marketplaceSafeToken(envelope.KeyID) {
		return fmt.Errorf("marketplace signature key_id is required and must be a safe token")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envelope.SignatureBase64))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("marketplace signature is not valid base64 Ed25519 data")
	}
	return nil
}

func marketplaceTrustedKey(keys []MarketplaceTrustedKey, keyID string) (ed25519.PublicKey, error) {
	keyID = strings.TrimSpace(keyID)
	var found ed25519.PublicKey
	for _, key := range keys {
		if strings.TrimSpace(key.KeyID) != keyID {
			continue
		}
		if len(key.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("marketplace signature trusted key %q is not a valid Ed25519 public key", keyID)
		}
		if found != nil {
			return nil, fmt.Errorf("marketplace signature trusted key %q is duplicated", keyID)
		}
		found = key.PublicKey
	}
	if found == nil {
		return nil, fmt.Errorf("marketplace signature trusted key %q is not available", keyID)
	}
	return found, nil
}

func marketplaceSignatureInput(subjectMedia string, digest string) []byte {
	domain, _ := marketplaceSignatureDomain(subjectMedia)
	return []byte("TokenHub Marketplace Signature v1\n" + domain + "\n" + strings.TrimSpace(subjectMedia) + "\n" + strings.TrimSpace(digest) + "\n")
}

func marketplaceSignatureDomain(subjectMedia string) (string, error) {
	switch strings.TrimSpace(subjectMedia) {
	case MarketplaceIndexMediaType:
		return marketplaceIndexSignatureDomain, nil
	case MarketplaceArtifactMediaType:
		return marketplaceArtifactDomain, nil
	case MarketplaceRevocationMediaType:
		return marketplaceRevocationDomain, nil
	default:
		return "", fmt.Errorf("unsupported marketplace signature subject media type %q", subjectMedia)
	}
}
