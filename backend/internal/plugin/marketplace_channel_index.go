package plugin

import (
	"fmt"
	"runtime"
	"strings"
)

func MarketplaceDescriptorsFromChannelIndex(index MarketplaceChannelIndex) ([]Descriptor, error) {
	index = normalizeMarketplaceIndex(index)
	if err := ValidateMarketplaceIndex(index); err != nil {
		return nil, err
	}
	descriptors := make([]Descriptor, 0, len(index.Plugins))
	for _, item := range index.Plugins {
		descriptor, err := marketplaceDescriptorFromChannelPlugin(item)
		if err != nil {
			return nil, err
		}
		descriptors = append(descriptors, NormalizeDescriptor(descriptor))
	}
	return descriptors, nil
}

func marketplaceDescriptorFromChannelPlugin(item MarketplaceIndexPlugin) (Descriptor, error) {
	release, ok := marketplacePreferredRelease(item)
	if !ok {
		return Descriptor{}, fmt.Errorf("marketplace plugin %q latest release %q is not present", item.ID, item.Latest)
	}
	artifact, hasArtifact := marketplacePreferredArtifact(release.Artifacts)
	descriptor := Descriptor{
		ID:           strings.TrimSpace(item.ID),
		Name:         strings.TrimSpace(item.Listing.Name),
		Version:      strings.TrimSpace(release.Version),
		Source:       SourceMarketplace,
		Distribution: marketplaceDistributionFromRelease(release, artifact, hasArtifact),
		Marketplace: &MarketplaceMetadata{
			Summary:       strings.TrimSpace(item.Listing.Summary),
			Categories:    normalizeStrings(item.Listing.Categories),
			Compatibility: marketplaceCompatibilityFromRelease(release, hasArtifact),
			Publisher:     marketplacePublisherFromIndex(item),
			Advisories:    marketplaceAdvisoriesFromRelease(release),
			ReleaseNotes:  marketplaceReleaseNotesFromRelease(release),
		},
	}
	return descriptor, nil
}

func marketplacePreferredRelease(item MarketplaceIndexPlugin) (MarketplaceIndexRelease, bool) {
	var selected MarketplaceIndexRelease
	selectedOK := false
	for _, release := range item.Releases {
		_, hasArtifact := marketplacePreferredArtifact(release.Artifacts)
		if !hasArtifact || release.Review.Status != MarketplaceReviewApproved {
			continue
		}
		if !selectedOK || marketplaceVersionGreater(release.Version, selected.Version) {
			selected = release
			selectedOK = true
		}
	}
	if selectedOK {
		return selected, true
	}
	return marketplaceLatestRelease(item)
}

func marketplaceLatestRelease(item MarketplaceIndexPlugin) (MarketplaceIndexRelease, bool) {
	latest := strings.TrimSpace(item.Latest)
	for _, release := range item.Releases {
		if strings.TrimSpace(release.Version) == latest {
			return release, true
		}
	}
	return MarketplaceIndexRelease{}, false
}

func marketplaceVersionGreater(left string, right string) bool {
	leftVersion, leftErr := parsePluginCoreVersion(left)
	rightVersion, rightErr := parsePluginCoreVersion(right)
	if leftErr == nil && rightErr == nil {
		return comparePluginCoreVersion(leftVersion, rightVersion) > 0
	}
	return strings.TrimSpace(left) > strings.TrimSpace(right)
}

func marketplacePreferredArtifact(artifacts []MarketplaceArtifact) (MarketplaceArtifact, bool) {
	if len(artifacts) == 0 {
		return MarketplaceArtifact{}, false
	}
	currentTarget := runtime.GOOS + "-" + runtime.GOARCH
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.Target) == "any" {
			return artifact, true
		}
	}
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.Target) == currentTarget {
			return artifact, true
		}
	}
	return artifacts[0], true
}

func marketplaceDistributionFromRelease(release MarketplaceIndexRelease, artifact MarketplaceArtifact, ok bool) *Distribution {
	if !ok {
		return nil
	}
	distribution := &Distribution{
		DownloadURL:        strings.TrimSpace(artifact.URL),
		ChecksumSHA256:     strings.TrimSpace(artifact.SHA256),
		SignatureURL:       strings.TrimSpace(artifact.Signature.URL),
		SignatureAlgorithm: strings.TrimSpace(artifact.Signature.Algorithm),
		SignatureKeyID:     strings.TrimSpace(artifact.Signature.KeyID),
	}
	return distribution
}

func marketplaceCompatibilityFromRelease(release MarketplaceIndexRelease, hasArtifact bool) *MarketplaceCompatibility {
	verdict := MarketplaceCompatibilityCompatible
	if !hasArtifact {
		verdict = MarketplaceCompatibilityIncompatible
	} else if err := ValidateManifestCompatibility(ManifestCompatibility{
		PluginAPI: strings.TrimSpace(release.Compatibility.PluginAPI),
		MinCore:   strings.TrimSpace(release.Compatibility.MinCore),
		MaxCore:   strings.TrimSpace(release.Compatibility.MaxCore),
	}); err != nil {
		verdict = MarketplaceCompatibilityIncompatible
	} else {
		switch release.Review.Status {
		case MarketplaceReviewRestricted, MarketplaceReviewDeprecated:
			verdict = MarketplaceCompatibilityNeedsReview
		case MarketplaceReviewRevoked:
			verdict = MarketplaceCompatibilityIncompatible
		}
	}
	return (&MarketplaceCompatibility{
		Verdict: verdict,
		Badges:  marketplaceCompatibilityBadges(release, hasArtifact),
	}).Normalized()
}

func marketplaceCompatibilityBadges(release MarketplaceIndexRelease, hasArtifact bool) []MarketplaceCompatibilityBadge {
	badges := []MarketplaceCompatibilityBadge{{
		ID:    "plugin_api",
		Label: "Plugin API " + strings.TrimSpace(release.Compatibility.PluginAPI),
		Tone:  "info",
	}, {
		ID:    "core",
		Label: "Core " + strings.TrimSpace(release.Compatibility.MinCore),
		Tone:  "success",
	}, {
		ID:    "review",
		Label: "Review " + strings.TrimSpace(string(release.Review.Status)),
		Tone:  marketplaceReviewTone(release.Review.Status),
	}}
	if hasArtifact {
		artifact, _ := marketplacePreferredArtifact(release.Artifacts)
		badges = append(badges, MarketplaceCompatibilityBadge{
			ID:    "target",
			Label: "Target " + strings.TrimSpace(artifact.Target),
			Tone:  "info",
		})
	}
	return badges
}

func marketplaceReviewTone(status MarketplaceReviewStatus) string {
	switch status {
	case MarketplaceReviewApproved:
		return "success"
	case MarketplaceReviewRestricted:
		return "warning"
	case MarketplaceReviewDeprecated:
		return "neutral"
	case MarketplaceReviewRevoked:
		return "danger"
	default:
		return "neutral"
	}
}

func marketplacePublisherFromIndex(item MarketplaceIndexPlugin) *MarketplacePublisher {
	publisher := &MarketplacePublisher{
		ID:       strings.TrimSpace(item.Publisher.ID),
		Name:     strings.TrimSpace(item.Publisher.ID),
		Verified: item.Publisher.Verification == MarketplacePublisherOfficial || item.Publisher.Verification == MarketplacePublisherVerified,
	}
	return publisher.Normalized()
}

func marketplaceAdvisoriesFromRelease(release MarketplaceIndexRelease) []MarketplaceAdvisory {
	switch release.Review.Status {
	case MarketplaceReviewRestricted, MarketplaceReviewDeprecated, MarketplaceReviewRevoked:
		return []MarketplaceAdvisory{{
			ID:          "marketplace-review-" + strings.TrimSpace(release.Version),
			Severity:    marketplaceReviewSeverity(release.Review.Status),
			Title:       "Marketplace review " + strings.TrimSpace(string(release.Review.Status)),
			PublishedAt: strings.TrimSpace(release.Review.ReviewedAt),
		}}
	default:
		return nil
	}
}

func marketplaceReviewSeverity(status MarketplaceReviewStatus) string {
	switch status {
	case MarketplaceReviewRevoked:
		return "critical"
	case MarketplaceReviewRestricted:
		return "high"
	case MarketplaceReviewDeprecated:
		return "medium"
	default:
		return "low"
	}
}

func marketplaceReleaseNotesFromRelease(release MarketplaceIndexRelease) []MarketplaceReleaseNote {
	if release.ReleaseNotes == nil {
		return nil
	}
	publishedAt := strings.TrimSpace(release.PublishedAt)
	if len(publishedAt) >= len("2006-01-02") {
		publishedAt = publishedAt[:len("2006-01-02")]
	}
	return []MarketplaceReleaseNote{{
		Version: strings.TrimSpace(release.Version),
		Date:    publishedAt,
		Title:   "Release " + strings.TrimSpace(release.Version),
		URL:     strings.TrimSpace(release.ReleaseNotes.URL),
		Items:   []string{"sha256:" + strings.TrimSpace(release.ReleaseNotes.SHA256)},
	}}
}
