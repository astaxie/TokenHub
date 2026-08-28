package plugin

import "testing"

func TestValidateInstallTrustAllowsLocalDevelopmentWithoutChecksum(t *testing.T) {
	decision, err := ValidateInstallTrust([]byte("archive"), InstallOptions{})
	if err != nil {
		t.Fatalf("validate local trust: %v", err)
	}
	if decision.Policy != TrustPolicyLocalDevelopment || decision.Verdict != TrustVerdictUnverified {
		t.Fatalf("trust decision = %+v", decision)
	}
}

func TestValidateInstallTrustRequiresChecksumForVerifiedArtifacts(t *testing.T) {
	for _, policy := range []PluginTrustPolicy{TrustPolicyVerifiedArtifact, TrustPolicyMarketplace, TrustPolicyOfflineMirror} {
		t.Run(string(policy), func(t *testing.T) {
			decision, err := ValidateInstallTrust([]byte("archive"), InstallOptions{TrustPolicy: policy})
			if err == nil {
				t.Fatal("trust validation succeeded without checksum")
			}
			if decision.Verdict != TrustVerdictRejected || decision.Reason != PluginErrorTrustChecksumRequired {
				t.Fatalf("trust decision = %+v", decision)
			}
			if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorTrustChecksumRequired {
				t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorTrustChecksumRequired, err)
			}
		})
	}
}

func TestValidateInstallTrustRejectsUnsupportedPolicy(t *testing.T) {
	decision, err := ValidateInstallTrust([]byte("archive"), InstallOptions{TrustPolicy: PluginTrustPolicy("mirror-ish")})
	if err == nil {
		t.Fatal("trust validation succeeded with unsupported policy")
	}
	if decision.Verdict != TrustVerdictRejected || decision.Reason != PluginErrorTrustPolicyUnsupported {
		t.Fatalf("trust decision = %+v", decision)
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorTrustPolicyUnsupported {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorTrustPolicyUnsupported, err)
	}
}

func TestValidateInstallTrustRequiresVerifiedSignatureForSignedMarketplace(t *testing.T) {
	archive := []byte("archive")
	checksum := sha256Hex(archive)
	decision, err := ValidateInstallTrust(archive, InstallOptions{
		ChecksumSHA256: checksum,
		TrustPolicy:    TrustPolicySignedMarketplace,
		SignatureURL:   "https://plugins.example/plugin.zip.sig",
		SignatureKeyID: "tokenhub-root-2026",
	})
	if err == nil {
		t.Fatal("signed marketplace trust validation succeeded without a verified signature")
	}
	if decision.Verdict != TrustVerdictRejected || decision.Reason != PluginErrorTrustSignatureUnverified {
		t.Fatalf("trust decision = %+v", decision)
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorTrustSignatureUnverified {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorTrustSignatureUnverified, err)
	}

	decision, err = ValidateInstallTrust(archive, InstallOptions{
		ChecksumSHA256:    checksum,
		TrustPolicy:       TrustPolicySignedMarketplace,
		SignatureURL:      "https://plugins.example/plugin.zip.sig",
		SignatureKeyID:    "tokenhub-root-2026",
		SignatureVerified: true,
	})
	if err != nil {
		t.Fatalf("validate signed marketplace trust: %v", err)
	}
	if decision.Verdict != TrustVerdictTrusted || !decision.SignatureRequired || !decision.ChecksumRequired {
		t.Fatalf("trust decision = %+v", decision)
	}
}

func TestParseManifestBuildsSignatureDistributionMetadata(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
schema_version: 1
id: tokenhub.signed
name: Signed Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
distribution:
  download_url: https://plugins.example/signed.zip
  checksum_sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  signature_url: https://plugins.example/signed.zip.sig
  signature_algorithm: ed25519
  signature_key_id: tokenhub-root-2026
kinds:
  - extension
`))
	if err != nil {
		t.Fatalf("parse signed manifest: %v", err)
	}
	distribution := manifest.Descriptor().Distribution
	if distribution == nil {
		t.Fatal("descriptor distribution is nil")
	}
	if distribution.SignatureURL != "https://plugins.example/signed.zip.sig" ||
		distribution.SignatureAlgorithm != PluginSignatureAlgorithmEd25519 ||
		distribution.SignatureKeyID != "tokenhub-root-2026" {
		t.Fatalf("descriptor distribution = %+v", distribution)
	}
}

func TestParseManifestRejectsInvalidSignatureDistributionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "unsupported algorithm",
			body: `
schema_version: 1
id: tokenhub.bad-signature
name: Bad Signature
version: 1.0.0
tokenhub:
  plugin_api: v1
distribution:
  signature_url: https://plugins.example/plugin.zip.sig
  signature_algorithm: rsa
kinds:
  - extension
`,
		},
		{
			name: "key without signature url",
			body: `
schema_version: 1
id: tokenhub.bad-key
name: Bad Key
version: 1.0.0
tokenhub:
  plugin_api: v1
distribution:
  signature_key_id: tokenhub-root-2026
kinds:
  - extension
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(tc.body)); err == nil {
				t.Fatal("manifest with invalid signature metadata parsed successfully")
			}
		})
	}
}
