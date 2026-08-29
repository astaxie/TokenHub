package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
)

func DecodeMarketplaceIndex(data []byte) (MarketplaceChannelIndex, error) {
	if len(data) > MaxMarketplaceIndexBytes {
		return MarketplaceChannelIndex{}, fmt.Errorf("marketplace index cannot exceed %d bytes", MaxMarketplaceIndexBytes)
	}
	if err := validateMarketplaceJSONDepth(data); err != nil {
		return MarketplaceChannelIndex{}, err
	}
	if err := rejectMarketplaceSecretMaterial(data); err != nil {
		return MarketplaceChannelIndex{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var index MarketplaceChannelIndex
	if err := decoder.Decode(&index); err != nil {
		return MarketplaceChannelIndex{}, fmt.Errorf("decode marketplace index: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return MarketplaceChannelIndex{}, fmt.Errorf("marketplace index contains trailing data")
	}
	return index, ValidateMarketplaceIndex(index)
}

func ValidateMarketplaceIndex(index MarketplaceChannelIndex) error {
	if index.SchemaVersion != MarketplaceIndexSchemaVersion {
		return fmt.Errorf("unsupported marketplace index schema_version %d", index.SchemaVersion)
	}
	if !marketplaceSafeToken(index.RepositoryID) {
		return fmt.Errorf("marketplace repository_id is required and must be a safe token")
	}
	if !validMarketplaceChannel(index.Channel) {
		return fmt.Errorf("unsupported marketplace channel %q", index.Channel)
	}
	if index.Sequence <= 0 {
		return fmt.Errorf("marketplace index sequence must be positive")
	}
	generatedAt, err := parseMarketplaceTime("generated_at", index.GeneratedAt)
	if err != nil {
		return err
	}
	expiresAt, err := parseMarketplaceTime("expires_at", index.ExpiresAt)
	if err != nil {
		return err
	}
	if !expiresAt.After(generatedAt) {
		return fmt.Errorf("marketplace index expires_at must be after generated_at")
	}
	if index.Revocations != nil {
		if err := validateMarketplaceObjectRef("revocations", *index.Revocations); err != nil {
			return err
		}
	}
	if len(index.Plugins) == 0 {
		return fmt.Errorf("marketplace index requires at least one plugin")
	}
	if len(index.Plugins) > MaxMarketplacePlugins {
		return fmt.Errorf("marketplace index cannot contain more than %d plugins", MaxMarketplacePlugins)
	}
	seenPlugins := map[string]struct{}{}
	for pluginIndex, item := range index.Plugins {
		if err := validateMarketplacePlugin(index.Channel, item); err != nil {
			return fmt.Errorf("plugins[%d] %s: %w", pluginIndex, strings.TrimSpace(item.ID), err)
		}
		id := strings.TrimSpace(item.ID)
		if _, ok := seenPlugins[id]; ok {
			return fmt.Errorf("plugin %q is duplicated", id)
		}
		seenPlugins[id] = struct{}{}
	}
	return nil
}

func CanonicalMarketplaceIndexJSON(index MarketplaceChannelIndex) ([]byte, error) {
	normalized := normalizeMarketplaceIndex(index)
	if err := ValidateMarketplaceIndex(normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func validateMarketplacePlugin(channel MarketplaceChannel, item MarketplaceIndexPlugin) error {
	if !validMarketplacePluginID(item.ID) {
		return fmt.Errorf("plugin id is required and must be DNS-style")
	}
	if !validMarketplaceOrigin(item.Origin) {
		return fmt.Errorf("unsupported origin %q", item.Origin)
	}
	if item.Origin == MarketplaceOriginOfficial && !strings.HasPrefix(item.ID, "tokenhub.") {
		return fmt.Errorf("official plugins must use the tokenhub namespace")
	}
	if item.Origin == MarketplaceOriginThirdParty && strings.HasPrefix(item.ID, "tokenhub.") {
		return fmt.Errorf("third-party plugins cannot use the reserved tokenhub namespace")
	}
	if err := validateMarketplacePublisher(item.Origin, item.Publisher); err != nil {
		return err
	}
	if strings.TrimSpace(item.Listing.Name) == "" {
		return fmt.Errorf("listing name is required")
	}
	if strings.TrimSpace(item.Listing.Summary) == "" {
		return fmt.Errorf("listing summary is required")
	}
	if len(item.Releases) == 0 {
		return fmt.Errorf("at least one release is required")
	}
	if len(item.Releases) > MaxMarketplaceReleases {
		return fmt.Errorf("plugin cannot contain more than %d releases", MaxMarketplaceReleases)
	}
	latest := strings.TrimSpace(item.Latest)
	if latest == "" {
		return fmt.Errorf("latest release is required")
	}
	seenReleases := map[string]struct{}{}
	latestFound := false
	for releaseIndex, release := range item.Releases {
		if err := validateMarketplaceRelease(channel, item.ID, release); err != nil {
			return fmt.Errorf("releases[%d] %s: %w", releaseIndex, strings.TrimSpace(release.Version), err)
		}
		version := strings.TrimSpace(release.Version)
		if _, ok := seenReleases[version]; ok {
			return fmt.Errorf("release %q is duplicated", version)
		}
		seenReleases[version] = struct{}{}
		if version == latest {
			latestFound = true
		}
	}
	if !latestFound {
		return fmt.Errorf("latest release %q is not present in releases", latest)
	}
	return nil
}

func validateMarketplacePublisher(origin MarketplaceOrigin, publisher MarketplaceIndexPublisher) error {
	if !marketplaceSafeToken(publisher.ID) {
		return fmt.Errorf("publisher id is required and must be a safe token")
	}
	switch publisher.Verification {
	case MarketplacePublisherOfficial:
		if origin != MarketplaceOriginOfficial {
			return fmt.Errorf("publisher verification official requires official origin")
		}
	case MarketplacePublisherVerified, MarketplacePublisherUnverified:
		if origin != MarketplaceOriginThirdParty {
			return fmt.Errorf("third-party publisher verification cannot be used for official plugins")
		}
	default:
		return fmt.Errorf("unsupported publisher verification %q", publisher.Verification)
	}
	return nil
}

func validateMarketplaceRelease(indexChannel MarketplaceChannel, pluginID string, release MarketplaceIndexRelease) error {
	if !validMarketplaceSemver(release.Version) {
		return fmt.Errorf("version must be SemVer")
	}
	if !validMarketplaceChannel(release.Channel) {
		return fmt.Errorf("unsupported release channel %q", release.Channel)
	}
	if release.Channel != indexChannel {
		return fmt.Errorf("release channel %q must match index channel %q", release.Channel, indexChannel)
	}
	if release.Channel == MarketplaceChannelStable && strings.Contains(strings.TrimSpace(release.Version), "-") {
		return fmt.Errorf("stable releases cannot use prerelease versions")
	}
	if _, err := parseMarketplaceTime("published_at", release.PublishedAt); err != nil {
		return err
	}
	if !marketplaceCommitPattern.MatchString(strings.TrimSpace(release.SourceCommit)) {
		return fmt.Errorf("source_commit must be a full lowercase git sha")
	}
	if err := validateMarketplaceReleaseCompatibility(release.Compatibility); err != nil {
		return err
	}
	if !marketplaceSHA256Pattern.MatchString(strings.TrimSpace(release.ManifestSHA256)) {
		return fmt.Errorf("manifest_sha256 must be a lowercase SHA-256")
	}
	if !marketplaceSHA256Pattern.MatchString(strings.TrimSpace(release.PermissionsSHA256)) {
		return fmt.Errorf("permissions_sha256 must be a lowercase SHA-256")
	}
	if len(release.Artifacts) == 0 {
		return fmt.Errorf("at least one artifact is required")
	}
	if len(release.Artifacts) > MaxMarketplaceArtifacts {
		return fmt.Errorf("release cannot contain more than %d artifacts", MaxMarketplaceArtifacts)
	}
	seenTargets := map[string]struct{}{}
	for artifactIndex, artifact := range release.Artifacts {
		if err := validateMarketplaceArtifact(pluginID, release.Version, artifact); err != nil {
			return fmt.Errorf("artifacts[%d] %s: %w", artifactIndex, strings.TrimSpace(artifact.Target), err)
		}
		target := strings.TrimSpace(artifact.Target)
		if _, ok := seenTargets[target]; ok {
			return fmt.Errorf("artifact target %q is duplicated", target)
		}
		seenTargets[target] = struct{}{}
	}
	if release.ReleaseNotes != nil {
		if err := validateMarketplaceObjectRef("release_notes", *release.ReleaseNotes); err != nil {
			return err
		}
	}
	return validateMarketplaceReview(release.Review)
}

func ValidateMarketplaceReleaseManifest(pluginID string, release MarketplaceIndexRelease, manifest Manifest) error {
	if strings.TrimSpace(manifest.ID) != strings.TrimSpace(pluginID) {
		return fmt.Errorf("marketplace release plugin id %q does not match manifest id %q", pluginID, manifest.ID)
	}
	if strings.TrimSpace(manifest.Version) != strings.TrimSpace(release.Version) {
		return fmt.Errorf("marketplace release version %q does not match manifest version %q", release.Version, manifest.Version)
	}
	if manifest.SchemaVersion != release.Compatibility.ManifestSchemaVersion {
		return fmt.Errorf("marketplace release manifest schema %d does not match manifest schema %d", release.Compatibility.ManifestSchemaVersion, manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.TokenHub.PluginAPI) != strings.TrimSpace(release.Compatibility.PluginAPI) ||
		strings.TrimSpace(manifest.TokenHub.MinCore) != strings.TrimSpace(release.Compatibility.MinCore) ||
		strings.TrimSpace(manifest.TokenHub.MaxCore) != strings.TrimSpace(release.Compatibility.MaxCore) {
		return fmt.Errorf("marketplace release compatibility does not match manifest tokenhub compatibility")
	}
	return validateMarketplaceDistributionClaim(release, manifest.Distribution)
}

func validateMarketplaceReleaseCompatibility(compat MarketplaceReleaseCompatibility) error {
	if compat.ManifestSchemaVersion != PluginManifestSchemaVersion {
		return pluginContractErrorf(PluginErrorManifestSchemaUnsupported, "unsupported manifest_schema_version %d", compat.ManifestSchemaVersion)
	}
	if err := ValidateManifestCompatibility(ManifestCompatibility{
		PluginAPI: compat.PluginAPI,
		MinCore:   compat.MinCore,
		MaxCore:   compat.MaxCore,
	}); err != nil {
		return err
	}
	supportedFeatures := map[string]struct{}{}
	for _, compatibility := range SupportedPluginAPICompatibility() {
		for _, feature := range compatibility.FeatureFlags {
			supportedFeatures[feature] = struct{}{}
		}
	}
	seenFeatures := map[string]struct{}{}
	for _, feature := range compat.RequiredFeatures {
		feature = strings.TrimSpace(feature)
		if !marketplaceSafeToken(feature) {
			return fmt.Errorf("required feature is not a safe token")
		}
		if _, ok := supportedFeatures[feature]; !ok {
			return fmt.Errorf("unsupported required feature %q", feature)
		}
		if _, ok := seenFeatures[feature]; ok {
			return fmt.Errorf("required feature %q is duplicated", feature)
		}
		seenFeatures[feature] = struct{}{}
	}
	return nil
}

func validateMarketplaceDistributionClaim(release MarketplaceIndexRelease, distribution ManifestDistribution) error {
	downloadURL := strings.TrimSpace(distribution.DownloadURL)
	checksum := strings.TrimSpace(distribution.ChecksumSHA256)
	signatureURL := strings.TrimSpace(distribution.SignatureURL)
	signatureAlgorithm := strings.TrimSpace(distribution.SignatureAlgorithm)
	signatureKeyID := strings.TrimSpace(distribution.SignatureKeyID)
	if downloadURL == "" && checksum == "" && signatureURL == "" && signatureAlgorithm == "" && signatureKeyID == "" {
		return nil
	}
	for _, artifact := range release.Artifacts {
		if downloadURL != "" && downloadURL != strings.TrimSpace(artifact.URL) {
			continue
		}
		if checksum != "" && checksum != strings.TrimSpace(artifact.SHA256) {
			continue
		}
		if signatureURL != "" && signatureURL != strings.TrimSpace(artifact.Signature.URL) {
			continue
		}
		if signatureAlgorithm != "" && signatureAlgorithm != strings.TrimSpace(artifact.Signature.Algorithm) {
			continue
		}
		if signatureKeyID != "" && signatureKeyID != strings.TrimSpace(artifact.Signature.KeyID) {
			continue
		}
		return nil
	}
	return fmt.Errorf("package distribution claim conflicts with marketplace release artifacts")
}

func validateMarketplaceArtifact(pluginID string, version string, artifact MarketplaceArtifact) error {
	if !validMarketplaceTarget(artifact.Target) {
		return fmt.Errorf("unsupported target %q", artifact.Target)
	}
	if err := validateMarketplaceImmutableHTTPSURL("artifact url", artifact.URL); err != nil {
		return err
	}
	if !strings.Contains(artifact.URL, pluginID) || !strings.Contains(artifact.URL, version) {
		return fmt.Errorf("artifact url must include plugin id and version")
	}
	if artifact.Size <= 0 || artifact.Size > MaxMarketplaceArtifactBytes {
		return fmt.Errorf("artifact size must be between 1 and %d bytes", MaxMarketplaceArtifactBytes)
	}
	if !marketplaceSHA256Pattern.MatchString(strings.TrimSpace(artifact.SHA256)) {
		return fmt.Errorf("artifact sha256 must be a lowercase SHA-256")
	}
	if strings.TrimSpace(artifact.Signature.Algorithm) != PluginSignatureAlgorithmEd25519 {
		return fmt.Errorf("artifact signature algorithm must be ed25519")
	}
	if !marketplaceSafeToken(artifact.Signature.KeyID) {
		return fmt.Errorf("artifact signature key_id is required and must be a safe token")
	}
	if err := validateMarketplaceImmutableHTTPSURL("artifact signature url", artifact.Signature.URL); err != nil {
		return err
	}
	if artifact.SBOM != nil {
		if err := validateMarketplaceAttachment("sbom", *artifact.SBOM); err != nil {
			return err
		}
	}
	if artifact.Provenance != nil {
		if err := validateMarketplaceAttachment("provenance", *artifact.Provenance); err != nil {
			return err
		}
	}
	return nil
}

func validateMarketplaceAttachment(field string, attachment MarketplaceAttachment) error {
	if strings.TrimSpace(attachment.Format) == "" {
		return fmt.Errorf("%s format is required", field)
	}
	return validateMarketplaceObjectRef(field, MarketplaceObjectRef{URL: attachment.URL, SHA256: attachment.SHA256})
}

func validateMarketplaceObjectRef(field string, ref MarketplaceObjectRef) error {
	if err := validateMarketplaceImmutableHTTPSURL(field+" url", ref.URL); err != nil {
		return err
	}
	if !marketplaceSHA256Pattern.MatchString(strings.TrimSpace(ref.SHA256)) {
		return fmt.Errorf("%s sha256 must be a lowercase SHA-256", field)
	}
	return nil
}

func validateMarketplaceReview(review MarketplaceReleaseReview) error {
	if !validMarketplaceReviewStatus(review.Status) {
		return fmt.Errorf("unsupported review status %q", review.Status)
	}
	if _, err := parseMarketplaceTime("reviewed_at", review.ReviewedAt); err != nil {
		return err
	}
	change := strings.TrimSpace(review.PermissionChange)
	if change == "" {
		return fmt.Errorf("permission_change is required")
	}
	switch change {
	case "none", "reduced", "metadata_only":
		return nil
	case "increased", "sensitive":
		if review.Status != MarketplaceReviewRestricted {
			return fmt.Errorf("permission increase requires restricted review status")
		}
		return nil
	default:
		return fmt.Errorf("unsupported permission_change %q", review.PermissionChange)
	}
}

func validateMarketplaceImmutableHTTPSURL(field string, value string) error {
	trimmed := strings.TrimSpace(value)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("%s must be an HTTPS URL", field)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must not contain query or fragment parameters", field)
	}
	lowerHost := strings.ToLower(parsed.Host)
	if lowerHost == "bit.ly" || lowerHost == "tinyurl.com" || strings.HasSuffix(lowerHost, ".bit.ly") {
		return fmt.Errorf("%s must not use a URL shortener", field)
	}
	lowerPath := strings.ToLower(parsed.EscapedPath())
	for _, mutableSegment := range []string{"/latest", "/main/", "/master/", "/head/", "/snapshot/"} {
		if strings.Contains(lowerPath, mutableSegment) {
			return fmt.Errorf("%s must be immutable HTTPS URL", field)
		}
	}
	return nil
}

func validateMarketplaceJSONDepth(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				return nil
			}
			return fmt.Errorf("decode marketplace index: %w", err)
		}
		switch delimiter := token.(type) {
		case json.Delim:
			switch delimiter {
			case '{', '[':
				depth++
				if depth > MaxMarketplaceJSONDepth {
					return fmt.Errorf("marketplace index JSON depth cannot exceed %d", MaxMarketplaceJSONDepth)
				}
			case '}', ']':
				depth--
			}
		}
	}
}

func rejectMarketplaceSecretMaterial(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return walkMarketplaceSecretMaterial(value, "")
}

func walkMarketplaceSecretMaterial(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lowerKey := strings.ToLower(key)
			for _, forbidden := range []string{"private_key", "access_token", "secret", "password", "credential"} {
				if strings.Contains(lowerKey, forbidden) {
					return fmt.Errorf("marketplace index must not include secret field %q", key)
				}
			}
			if err := walkMarketplaceSecretMaterial(child, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := walkMarketplaceSecretMaterial(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		lowerValue := strings.ToLower(typed)
		if strings.Contains(lowerValue, "-----begin private key-----") || strings.Contains(lowerValue, "tokenhub_secret_") {
			return fmt.Errorf("marketplace index must not include secret material")
		}
	}
	return nil
}

func parseMarketplaceTime(field string, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339 UTC time", field)
	}
	if parsed.Location() != time.UTC {
		return time.Time{}, fmt.Errorf("%s must use UTC", field)
	}
	return parsed, nil
}

func normalizeMarketplaceIndex(index MarketplaceChannelIndex) MarketplaceChannelIndex {
	index.RepositoryID = strings.TrimSpace(index.RepositoryID)
	index.Channel = MarketplaceChannel(strings.TrimSpace(string(index.Channel)))
	index.GeneratedAt = strings.TrimSpace(index.GeneratedAt)
	index.ExpiresAt = strings.TrimSpace(index.ExpiresAt)
	if index.Revocations != nil {
		ref := normalizeMarketplaceObjectRef(*index.Revocations)
		index.Revocations = &ref
	}
	for pluginIndex := range index.Plugins {
		index.Plugins[pluginIndex] = normalizeMarketplacePlugin(index.Plugins[pluginIndex])
	}
	sort.Slice(index.Plugins, func(i, j int) bool {
		return index.Plugins[i].ID < index.Plugins[j].ID
	})
	return index
}

func normalizeMarketplacePlugin(item MarketplaceIndexPlugin) MarketplaceIndexPlugin {
	item.ID = strings.TrimSpace(item.ID)
	item.Origin = MarketplaceOrigin(strings.TrimSpace(string(item.Origin)))
	item.Publisher.ID = strings.TrimSpace(item.Publisher.ID)
	item.Publisher.Verification = MarketplacePublisherVerification(strings.TrimSpace(string(item.Publisher.Verification)))
	item.Listing.Name = strings.TrimSpace(item.Listing.Name)
	item.Listing.Summary = strings.TrimSpace(item.Listing.Summary)
	item.Listing.Categories = normalizeStrings(item.Listing.Categories)
	sort.Strings(item.Listing.Categories)
	item.Latest = strings.TrimSpace(item.Latest)
	for releaseIndex := range item.Releases {
		item.Releases[releaseIndex] = normalizeMarketplaceRelease(item.Releases[releaseIndex])
	}
	sort.Slice(item.Releases, func(i, j int) bool {
		return item.Releases[i].Version < item.Releases[j].Version
	})
	return item
}

func normalizeMarketplaceRelease(release MarketplaceIndexRelease) MarketplaceIndexRelease {
	release.Version = strings.TrimSpace(release.Version)
	release.Channel = MarketplaceChannel(strings.TrimSpace(string(release.Channel)))
	release.PublishedAt = strings.TrimSpace(release.PublishedAt)
	release.SourceCommit = strings.TrimSpace(release.SourceCommit)
	release.Compatibility.PluginAPI = strings.TrimSpace(release.Compatibility.PluginAPI)
	release.Compatibility.MinCore = strings.TrimSpace(release.Compatibility.MinCore)
	release.Compatibility.MaxCore = strings.TrimSpace(release.Compatibility.MaxCore)
	release.Compatibility.RequiredFeatures = normalizeStrings(release.Compatibility.RequiredFeatures)
	sort.Strings(release.Compatibility.RequiredFeatures)
	release.ManifestSHA256 = strings.TrimSpace(release.ManifestSHA256)
	release.PermissionsSHA256 = strings.TrimSpace(release.PermissionsSHA256)
	for artifactIndex := range release.Artifacts {
		release.Artifacts[artifactIndex] = normalizeMarketplaceArtifact(release.Artifacts[artifactIndex])
	}
	sort.Slice(release.Artifacts, func(i, j int) bool {
		return release.Artifacts[i].Target < release.Artifacts[j].Target
	})
	if release.ReleaseNotes != nil {
		ref := normalizeMarketplaceObjectRef(*release.ReleaseNotes)
		release.ReleaseNotes = &ref
	}
	release.Review.Status = MarketplaceReviewStatus(strings.TrimSpace(string(release.Review.Status)))
	release.Review.ReviewedAt = strings.TrimSpace(release.Review.ReviewedAt)
	release.Review.PermissionChange = strings.TrimSpace(release.Review.PermissionChange)
	return release
}

func normalizeMarketplaceArtifact(artifact MarketplaceArtifact) MarketplaceArtifact {
	artifact.Target = strings.TrimSpace(artifact.Target)
	artifact.URL = strings.TrimSpace(artifact.URL)
	artifact.SHA256 = strings.TrimSpace(artifact.SHA256)
	artifact.Signature.Algorithm = strings.TrimSpace(artifact.Signature.Algorithm)
	artifact.Signature.KeyID = strings.TrimSpace(artifact.Signature.KeyID)
	artifact.Signature.URL = strings.TrimSpace(artifact.Signature.URL)
	if artifact.SBOM != nil {
		attachment := normalizeMarketplaceAttachment(*artifact.SBOM)
		artifact.SBOM = &attachment
	}
	if artifact.Provenance != nil {
		attachment := normalizeMarketplaceAttachment(*artifact.Provenance)
		artifact.Provenance = &attachment
	}
	return artifact
}

func normalizeMarketplaceAttachment(attachment MarketplaceAttachment) MarketplaceAttachment {
	attachment.Format = strings.TrimSpace(attachment.Format)
	attachment.URL = strings.TrimSpace(attachment.URL)
	attachment.SHA256 = strings.TrimSpace(attachment.SHA256)
	return attachment
}

func normalizeMarketplaceObjectRef(ref MarketplaceObjectRef) MarketplaceObjectRef {
	ref.URL = strings.TrimSpace(ref.URL)
	ref.SHA256 = strings.TrimSpace(ref.SHA256)
	return ref
}

func validMarketplacePluginID(value string) bool {
	value = strings.TrimSpace(value)
	if !marketplaceSafeToken(value) || !strings.Contains(value, ".") {
		return false
	}
	return !strings.Contains(value, "..")
}

func marketplaceSafeToken(value string) bool {
	return marketplaceTokenPattern.MatchString(strings.TrimSpace(value))
}

func validMarketplaceSemver(value string) bool {
	return marketplaceSemverPattern.MatchString(strings.TrimSpace(value))
}

func validMarketplaceChannel(channel MarketplaceChannel) bool {
	switch channel {
	case MarketplaceChannelStable, MarketplaceChannelBeta, MarketplaceChannelNightly:
		return true
	default:
		return false
	}
}

func validMarketplaceOrigin(origin MarketplaceOrigin) bool {
	return origin == MarketplaceOriginOfficial || origin == MarketplaceOriginThirdParty
}

func validMarketplaceReviewStatus(status MarketplaceReviewStatus) bool {
	switch status {
	case MarketplaceReviewApproved, MarketplaceReviewRestricted, MarketplaceReviewDeprecated, MarketplaceReviewRevoked:
		return true
	default:
		return false
	}
}

func validMarketplaceTarget(target string) bool {
	switch strings.TrimSpace(target) {
	case "any", "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64":
		return true
	default:
		return false
	}
}
