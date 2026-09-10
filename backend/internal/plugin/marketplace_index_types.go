package plugin

import "regexp"

const (
	MarketplaceIndexSchemaVersion = 1
	MaxMarketplaceIndexBytes      = 1 << 20
	MaxMarketplaceJSONDepth       = 64
	MaxMarketplacePlugins         = 500
	MaxMarketplaceReleases        = 100
	MaxMarketplaceArtifacts       = 32
	MaxMarketplaceArtifactBytes   = 1 << 30
)

type MarketplaceChannel string

const (
	MarketplaceChannelStable  MarketplaceChannel = "stable"
	MarketplaceChannelBeta    MarketplaceChannel = "beta"
	MarketplaceChannelNightly MarketplaceChannel = "nightly"
)

type MarketplaceOrigin string

const (
	MarketplaceOriginOfficial   MarketplaceOrigin = "official"
	MarketplaceOriginThirdParty MarketplaceOrigin = "third_party"
)

type MarketplacePublisherVerification string

const (
	MarketplacePublisherOfficial   MarketplacePublisherVerification = "official"
	MarketplacePublisherVerified   MarketplacePublisherVerification = "verified"
	MarketplacePublisherUnverified MarketplacePublisherVerification = "unverified"
)

type MarketplaceReviewStatus string

const (
	MarketplaceReviewApproved   MarketplaceReviewStatus = "approved"
	MarketplaceReviewRestricted MarketplaceReviewStatus = "restricted"
	MarketplaceReviewDeprecated MarketplaceReviewStatus = "deprecated"
	MarketplaceReviewRevoked    MarketplaceReviewStatus = "revoked"
)

type MarketplaceChannelIndex struct {
	SchemaVersion int                      `json:"schema_version"`
	RepositoryID  string                   `json:"repository_id"`
	Channel       MarketplaceChannel       `json:"channel"`
	Sequence      int64                    `json:"sequence"`
	GeneratedAt   string                   `json:"generated_at"`
	ExpiresAt     string                   `json:"expires_at"`
	Revocations   *MarketplaceObjectRef    `json:"revocations,omitempty"`
	Plugins       []MarketplaceIndexPlugin `json:"plugins"`
}

type MarketplaceObjectRef struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type MarketplaceIndexPlugin struct {
	ID        string                    `json:"id"`
	Origin    MarketplaceOrigin         `json:"origin"`
	Publisher MarketplaceIndexPublisher `json:"publisher"`
	Listing   MarketplaceIndexListing   `json:"listing"`
	Latest    string                    `json:"latest"`
	Releases  []MarketplaceIndexRelease `json:"releases"`
}

type MarketplaceIndexPublisher struct {
	ID           string                           `json:"id"`
	Verification MarketplacePublisherVerification `json:"verification"`
}

type MarketplaceIndexListing struct {
	Name       string   `json:"name"`
	Summary    string   `json:"summary"`
	Categories []string `json:"categories,omitempty"`
}

type MarketplaceIndexRelease struct {
	Version           string                          `json:"version"`
	Channel           MarketplaceChannel              `json:"channel"`
	PublishedAt       string                          `json:"published_at"`
	SourceCommit      string                          `json:"source_commit"`
	Compatibility     MarketplaceReleaseCompatibility `json:"compatibility"`
	ManifestSHA256    string                          `json:"manifest_sha256"`
	PermissionsSHA256 string                          `json:"permissions_sha256"`
	Artifacts         []MarketplaceArtifact           `json:"artifacts"`
	ReleaseNotes      *MarketplaceObjectRef           `json:"release_notes,omitempty"`
	Review            MarketplaceReleaseReview        `json:"review"`
}

type MarketplaceReleaseCompatibility struct {
	PluginAPI             string   `json:"plugin_api"`
	ManifestSchemaVersion int      `json:"manifest_schema_version"`
	MinCore               string   `json:"min_core"`
	MaxCore               string   `json:"max_core,omitempty"`
	RequiredFeatures      []string `json:"required_features,omitempty"`
}

type MarketplaceArtifact struct {
	Target     string                       `json:"target"`
	URL        string                       `json:"url"`
	Size       int64                        `json:"size"`
	SHA256     string                       `json:"sha256"`
	Signature  MarketplaceArtifactSignature `json:"signature"`
	SBOM       *MarketplaceAttachment       `json:"sbom,omitempty"`
	Provenance *MarketplaceAttachment       `json:"provenance,omitempty"`
}

type MarketplaceArtifactSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	URL       string `json:"url"`
}

type MarketplaceAttachment struct {
	Format string `json:"format"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type MarketplaceReleaseReview struct {
	Status           MarketplaceReviewStatus `json:"status"`
	ReviewedAt       string                  `json:"reviewed_at"`
	PermissionChange string                  `json:"permission_change"`
}

var (
	marketplaceTokenPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*[a-z0-9]$`)
	marketplaceSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	marketplaceSemverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	marketplaceCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
)
