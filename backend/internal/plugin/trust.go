package plugin

import "strings"

type PluginTrustPolicy string

const (
	TrustPolicyLocalDevelopment  PluginTrustPolicy = "local_development"
	TrustPolicyVerifiedArtifact  PluginTrustPolicy = "verified_artifact"
	TrustPolicyMarketplace       PluginTrustPolicy = "marketplace"
	TrustPolicyOfflineMirror     PluginTrustPolicy = "offline_mirror"
	TrustPolicySignedMarketplace PluginTrustPolicy = "signed_marketplace"
)

type PluginTrustVerdict string

const (
	TrustVerdictTrusted    PluginTrustVerdict = "trusted"
	TrustVerdictUnverified PluginTrustVerdict = "unverified"
	TrustVerdictRejected   PluginTrustVerdict = "rejected"
)

const PluginSignatureAlgorithmEd25519 = "ed25519"

type PluginTrustDecision struct {
	Policy            PluginTrustPolicy  `json:"policy"`
	Verdict           PluginTrustVerdict `json:"verdict"`
	Reason            PluginErrorCode    `json:"reason,omitempty"`
	ChecksumRequired  bool               `json:"checksum_required"`
	SignatureRequired bool               `json:"signature_required"`
	ChecksumSHA256    string             `json:"checksum_sha256,omitempty"`
	SignatureURL      string             `json:"signature_url,omitempty"`
	SignatureKeyID    string             `json:"signature_key_id,omitempty"`
}

func ValidateInstallTrust(archive []byte, options InstallOptions) (PluginTrustDecision, error) {
	policy := NormalizePluginTrustPolicy(options.TrustPolicy)
	decision := PluginTrustDecision{
		Policy:            policy,
		ChecksumRequired:  pluginTrustPolicyRequiresChecksum(policy),
		SignatureRequired: pluginTrustPolicyRequiresSignature(policy),
		ChecksumSHA256:    strings.ToLower(strings.TrimSpace(options.ChecksumSHA256)),
		SignatureURL:      strings.TrimSpace(options.SignatureURL),
		SignatureKeyID:    strings.TrimSpace(options.SignatureKeyID),
	}
	if !validPluginTrustPolicy(policy) {
		decision.Verdict = TrustVerdictRejected
		decision.Reason = PluginErrorTrustPolicyUnsupported
		return decision, pluginContractErrorf(PluginErrorTrustPolicyUnsupported, "unsupported plugin trust policy %q", options.TrustPolicy)
	}
	if decision.ChecksumRequired && decision.ChecksumSHA256 == "" {
		decision.Verdict = TrustVerdictRejected
		decision.Reason = PluginErrorTrustChecksumRequired
		return decision, pluginContractErrorf(PluginErrorTrustChecksumRequired, "plugin package checksum_sha256 is required by trust policy %q", policy)
	}
	if err := verifyArchiveChecksum(archive, decision.ChecksumSHA256); err != nil {
		decision.Verdict = TrustVerdictRejected
		return decision, err
	}
	if decision.SignatureRequired && decision.SignatureURL == "" {
		decision.Verdict = TrustVerdictRejected
		decision.Reason = PluginErrorTrustSignatureRequired
		return decision, pluginContractErrorf(PluginErrorTrustSignatureRequired, "plugin package signature_url is required by trust policy %q", policy)
	}
	if decision.SignatureRequired && !options.SignatureVerified {
		decision.Verdict = TrustVerdictRejected
		decision.Reason = PluginErrorTrustSignatureUnverified
		return decision, pluginContractErrorf(PluginErrorTrustSignatureUnverified, "plugin package signature is not verified by trust policy %q", policy)
	}
	if decision.ChecksumSHA256 == "" && !options.SignatureVerified {
		decision.Verdict = TrustVerdictUnverified
		return decision, nil
	}
	decision.Verdict = TrustVerdictTrusted
	return decision, nil
}

func NormalizePluginTrustPolicy(policy PluginTrustPolicy) PluginTrustPolicy {
	policy = PluginTrustPolicy(strings.TrimSpace(string(policy)))
	if policy == "" {
		return TrustPolicyLocalDevelopment
	}
	return policy
}

func validPluginTrustPolicy(policy PluginTrustPolicy) bool {
	switch policy {
	case TrustPolicyLocalDevelopment, TrustPolicyVerifiedArtifact, TrustPolicyMarketplace, TrustPolicyOfflineMirror, TrustPolicySignedMarketplace:
		return true
	default:
		return false
	}
}

func pluginTrustPolicyRequiresChecksum(policy PluginTrustPolicy) bool {
	switch NormalizePluginTrustPolicy(policy) {
	case TrustPolicyVerifiedArtifact, TrustPolicyMarketplace, TrustPolicyOfflineMirror, TrustPolicySignedMarketplace:
		return true
	default:
		return false
	}
}

func pluginTrustPolicyRequiresSignature(policy PluginTrustPolicy) bool {
	return NormalizePluginTrustPolicy(policy) == TrustPolicySignedMarketplace
}
