package plugin

import (
	"sort"
	"strings"
)

type Kind string

const (
	KindProvider  Kind = "provider"
	KindAdminUI   Kind = "admin_ui"
	KindSIM       Kind = "sim"
	KindExtension Kind = "extension"
)

type Placement string

const (
	PlacementPresentation     Placement = "presentation"
	PlacementGatewayChain     Placement = "gateway_chain"
	PlacementBackground       Placement = "background"
	PlacementManagementAction Placement = "management_action"
)

type Source string

const (
	SourceBuiltIn     Source = "built_in"
	SourceMarketplace Source = "marketplace"
	SourceLocalFile   Source = "local_file"
)

type Status string

const (
	StatusEnabled           Status = "enabled"
	StatusDisabled          Status = "disabled"
	StatusPendingRestart    Status = "pending_restart"
	StatusFailedValidation  Status = "failed_validation"
	StatusRollbackAvailable Status = "rollback_available"
	StatusMandatory         Status = "mandatory"
)

type CapabilityDescriptor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Subject string `json:"subject,omitempty"`
	Value   string `json:"value,omitempty"`
}

type Distribution struct {
	MarketplaceURL     string `json:"marketplace_url,omitempty"`
	RepositoryURL      string `json:"repository_url,omitempty"`
	DownloadURL        string `json:"download_url,omitempty"`
	ChecksumSHA256     string `json:"checksum_sha256,omitempty"`
	SignatureURL       string `json:"signature_url,omitempty"`
	SignatureAlgorithm string `json:"signature_algorithm,omitempty"`
	SignatureKeyID     string `json:"signature_key_id,omitempty"`
	HomepageURL        string `json:"homepage_url,omitempty"`
	License            string `json:"license,omitempty"`
}

type MarketplaceCompatibilityVerdict string

const (
	MarketplaceCompatibilityCompatible   MarketplaceCompatibilityVerdict = "compatible"
	MarketplaceCompatibilityNeedsReview  MarketplaceCompatibilityVerdict = "needs_review"
	MarketplaceCompatibilityIncompatible MarketplaceCompatibilityVerdict = "incompatible"
	MarketplaceCompatibilityUnknown      MarketplaceCompatibilityVerdict = "unknown"
)

type MarketplaceMetadata struct {
	Summary       string                             `json:"summary,omitempty" yaml:"summary"`
	Categories    []string                           `json:"categories,omitempty" yaml:"categories"`
	Screenshots   []MarketplaceScreenshot            `json:"screenshots,omitempty" yaml:"screenshots"`
	Localizations map[string]MarketplaceLocalization `json:"localizations,omitempty" yaml:"localizations"`
	Compatibility *MarketplaceCompatibility          `json:"compatibility,omitempty" yaml:"compatibility"`
	Publisher     *MarketplacePublisher              `json:"publisher,omitempty" yaml:"publisher"`
	Advisories    []MarketplaceAdvisory              `json:"advisories,omitempty" yaml:"advisories"`
	ReleaseNotes  []MarketplaceReleaseNote           `json:"release_notes,omitempty" yaml:"release_notes"`
}

type MarketplaceScreenshot struct {
	URL          string `json:"url,omitempty" yaml:"url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty" yaml:"thumbnail_url"`
	Alt          string `json:"alt,omitempty" yaml:"alt"`
	Caption      string `json:"caption,omitempty" yaml:"caption"`
	Locale       string `json:"locale,omitempty" yaml:"locale"`
	Width        int    `json:"width,omitempty" yaml:"width"`
	Height       int    `json:"height,omitempty" yaml:"height"`
}

type MarketplaceLocalization struct {
	Name         string `json:"name,omitempty" yaml:"name"`
	Summary      string `json:"summary,omitempty" yaml:"summary"`
	Description  string `json:"description,omitempty" yaml:"description"`
	ReleaseNotes string `json:"release_notes,omitempty" yaml:"release_notes"`
}

type MarketplaceCompatibility struct {
	Verdict MarketplaceCompatibilityVerdict `json:"verdict,omitempty" yaml:"verdict"`
	Badges  []MarketplaceCompatibilityBadge `json:"badges,omitempty" yaml:"badges"`
}

type MarketplaceCompatibilityBadge struct {
	ID    string `json:"id,omitempty" yaml:"id"`
	Label string `json:"label,omitempty" yaml:"label"`
	Tone  string `json:"tone,omitempty" yaml:"tone"`
	URL   string `json:"url,omitempty" yaml:"url"`
}

type MarketplacePublisher struct {
	ID         string `json:"id,omitempty" yaml:"id"`
	Name       string `json:"name,omitempty" yaml:"name"`
	URL        string `json:"url,omitempty" yaml:"url"`
	SupportURL string `json:"support_url,omitempty" yaml:"support_url"`
	ContactURL string `json:"contact_url,omitempty" yaml:"contact_url"`
	Verified   bool   `json:"verified,omitempty" yaml:"verified"`
}

type MarketplaceAdvisory struct {
	ID          string `json:"id,omitempty" yaml:"id"`
	Severity    string `json:"severity,omitempty" yaml:"severity"`
	Title       string `json:"title,omitempty" yaml:"title"`
	URL         string `json:"url,omitempty" yaml:"url"`
	PublishedAt string `json:"published_at,omitempty" yaml:"published_at"`
	UpdatedAt   string `json:"updated_at,omitempty" yaml:"updated_at"`
}

type MarketplaceReleaseNote struct {
	Version string   `json:"version,omitempty" yaml:"version"`
	Date    string   `json:"date,omitempty" yaml:"date"`
	Title   string   `json:"title,omitempty" yaml:"title"`
	Notes   string   `json:"notes,omitempty" yaml:"notes"`
	URL     string   `json:"url,omitempty" yaml:"url"`
	Items   []string `json:"items,omitempty" yaml:"items"`
}

type Descriptor struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Source       Source                 `json:"source"`
	Status       Status                 `json:"status"`
	Distribution *Distribution          `json:"distribution,omitempty"`
	Marketplace  *MarketplaceMetadata   `json:"marketplace,omitempty"`
	Kinds        []Kind                 `json:"kinds"`
	Placements   []Placement            `json:"placements"`
	Permissions  []PermissionDescriptor `json:"permissions,omitempty"`
	Capabilities []CapabilityDescriptor `json:"capabilities"`
}

func BuiltInProvider(id string, name string, providerTypes []string, capabilities []string) Descriptor {
	return BuiltInProviderWithResourceTypes(id, name, providerTypes, nil, capabilities)
}

func BuiltInProviderWithResourceTypes(id string, name string, providerTypes []string, resourceTypes []string, capabilities []string) Descriptor {
	typedResourceTypes := make([]ManifestProviderResourceType, 0, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		typedResourceTypes = append(typedResourceTypes, ManifestProviderResourceType{Type: resourceType})
	}
	return BuiltInProviderWithResourceTypeMetadata(id, name, providerTypes, typedResourceTypes, capabilities)
}

func BuiltInProviderWithResourceTypeMetadata(id string, name string, providerTypes []string, resourceTypes []ManifestProviderResourceType, capabilities []string) Descriptor {
	descriptors := make([]CapabilityDescriptor, 0, len(providerTypes)*len(capabilities))
	for _, providerType := range providerTypes {
		for _, resourceType := range resourceTypes {
			descriptors = append(descriptors, CapabilityDescriptor{
				Kind:    "provider_resource_type",
				Name:    resourceType.Type,
				Subject: providerType,
				Value:   resourceType.CapabilityValue(),
			})
		}
		for _, capability := range capabilities {
			descriptors = append(descriptors, CapabilityDescriptor{
				Kind:    "provider",
				Name:    capability,
				Subject: providerType,
			})
		}
	}
	return Descriptor{
		ID:           id,
		Name:         name,
		Version:      "built-in",
		Source:       SourceBuiltIn,
		Status:       StatusEnabled,
		Kinds:        []Kind{KindProvider},
		Placements:   []Placement{PlacementGatewayChain, PlacementManagementAction},
		Capabilities: descriptors,
	}
}

func NormalizeDescriptor(descriptor Descriptor) Descriptor {
	if descriptor.Status == "" {
		descriptor.Status = StatusEnabled
	}
	descriptor.Marketplace = descriptor.Marketplace.Normalized()
	descriptor.Kinds = normalizeKinds(descriptor.Kinds)
	descriptor.Placements = normalizePlacements(descriptor.Placements)
	descriptor.Permissions = NormalizePermissionDescriptors(descriptor.Permissions)
	descriptor.Capabilities = normalizeCapabilities(descriptor.Capabilities)
	return descriptor
}

func (metadata *MarketplaceMetadata) Normalized() *MarketplaceMetadata {
	if metadata == nil {
		return nil
	}
	normalized := &MarketplaceMetadata{
		Summary:       strings.TrimSpace(metadata.Summary),
		Categories:    normalizeStrings(metadata.Categories),
		Localizations: normalizeMarketplaceLocalizations(metadata.Localizations),
		Compatibility: metadata.Compatibility.Normalized(),
		Publisher:     metadata.Publisher.Normalized(),
	}
	for _, screenshot := range metadata.Screenshots {
		if normalizedScreenshot, ok := screenshot.Normalized(); ok {
			normalized.Screenshots = append(normalized.Screenshots, normalizedScreenshot)
		}
	}
	for _, advisory := range metadata.Advisories {
		if normalizedAdvisory, ok := advisory.Normalized(); ok {
			normalized.Advisories = append(normalized.Advisories, normalizedAdvisory)
		}
	}
	for _, note := range metadata.ReleaseNotes {
		if normalizedNote, ok := note.Normalized(); ok {
			normalized.ReleaseNotes = append(normalized.ReleaseNotes, normalizedNote)
		}
	}
	if !normalized.Configured() {
		return nil
	}
	return normalized
}

func (metadata MarketplaceMetadata) Configured() bool {
	return strings.TrimSpace(metadata.Summary) != "" ||
		len(metadata.Categories) > 0 ||
		len(metadata.Screenshots) > 0 ||
		len(metadata.Localizations) > 0 ||
		metadata.Compatibility != nil ||
		metadata.Publisher != nil ||
		len(metadata.Advisories) > 0 ||
		len(metadata.ReleaseNotes) > 0
}

func (screenshot MarketplaceScreenshot) Normalized() (MarketplaceScreenshot, bool) {
	normalized := MarketplaceScreenshot{
		URL:          strings.TrimSpace(screenshot.URL),
		ThumbnailURL: strings.TrimSpace(screenshot.ThumbnailURL),
		Alt:          strings.TrimSpace(screenshot.Alt),
		Caption:      strings.TrimSpace(screenshot.Caption),
		Locale:       strings.TrimSpace(screenshot.Locale),
		Width:        screenshot.Width,
		Height:       screenshot.Height,
	}
	return normalized, normalized.URL != "" || normalized.ThumbnailURL != "" || normalized.Alt != "" ||
		normalized.Caption != "" || normalized.Locale != "" || normalized.Width != 0 || normalized.Height != 0
}

func normalizeMarketplaceLocalizations(items map[string]MarketplaceLocalization) map[string]MarketplaceLocalization {
	if len(items) == 0 {
		return nil
	}
	normalized := make(map[string]MarketplaceLocalization, len(items))
	for key, value := range items {
		key = strings.TrimSpace(key)
		value = value.Normalized()
		if key == "" || !value.Configured() {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (localization MarketplaceLocalization) Normalized() MarketplaceLocalization {
	return MarketplaceLocalization{
		Name:         strings.TrimSpace(localization.Name),
		Summary:      strings.TrimSpace(localization.Summary),
		Description:  strings.TrimSpace(localization.Description),
		ReleaseNotes: strings.TrimSpace(localization.ReleaseNotes),
	}
}

func (localization MarketplaceLocalization) Configured() bool {
	return localization.Name != "" || localization.Summary != "" || localization.Description != "" || localization.ReleaseNotes != ""
}

func (compatibility *MarketplaceCompatibility) Normalized() *MarketplaceCompatibility {
	if compatibility == nil {
		return nil
	}
	normalized := &MarketplaceCompatibility{
		Verdict: MarketplaceCompatibilityVerdict(strings.TrimSpace(string(compatibility.Verdict))),
	}
	for _, badge := range compatibility.Badges {
		if normalizedBadge, ok := badge.Normalized(); ok {
			normalized.Badges = append(normalized.Badges, normalizedBadge)
		}
	}
	if normalized.Verdict == "" && len(normalized.Badges) == 0 {
		return nil
	}
	return normalized
}

func (badge MarketplaceCompatibilityBadge) Normalized() (MarketplaceCompatibilityBadge, bool) {
	normalized := MarketplaceCompatibilityBadge{
		ID:    strings.TrimSpace(badge.ID),
		Label: strings.TrimSpace(badge.Label),
		Tone:  strings.TrimSpace(badge.Tone),
		URL:   strings.TrimSpace(badge.URL),
	}
	return normalized, normalized.ID != "" || normalized.Label != "" || normalized.Tone != "" || normalized.URL != ""
}

func (publisher *MarketplacePublisher) Normalized() *MarketplacePublisher {
	if publisher == nil {
		return nil
	}
	normalized := &MarketplacePublisher{
		ID:         strings.TrimSpace(publisher.ID),
		Name:       strings.TrimSpace(publisher.Name),
		URL:        strings.TrimSpace(publisher.URL),
		SupportURL: strings.TrimSpace(publisher.SupportURL),
		ContactURL: strings.TrimSpace(publisher.ContactURL),
		Verified:   publisher.Verified,
	}
	if normalized.ID == "" && normalized.Name == "" && normalized.URL == "" &&
		normalized.SupportURL == "" && normalized.ContactURL == "" && !normalized.Verified {
		return nil
	}
	return normalized
}

func (advisory MarketplaceAdvisory) Normalized() (MarketplaceAdvisory, bool) {
	normalized := MarketplaceAdvisory{
		ID:          strings.TrimSpace(advisory.ID),
		Severity:    strings.TrimSpace(advisory.Severity),
		Title:       strings.TrimSpace(advisory.Title),
		URL:         strings.TrimSpace(advisory.URL),
		PublishedAt: strings.TrimSpace(advisory.PublishedAt),
		UpdatedAt:   strings.TrimSpace(advisory.UpdatedAt),
	}
	return normalized, normalized.ID != "" || normalized.Severity != "" || normalized.Title != "" ||
		normalized.URL != "" || normalized.PublishedAt != "" || normalized.UpdatedAt != ""
}

func (note MarketplaceReleaseNote) Normalized() (MarketplaceReleaseNote, bool) {
	normalized := MarketplaceReleaseNote{
		Version: strings.TrimSpace(note.Version),
		Date:    strings.TrimSpace(note.Date),
		Title:   strings.TrimSpace(note.Title),
		Notes:   strings.TrimSpace(note.Notes),
		URL:     strings.TrimSpace(note.URL),
		Items:   normalizeStrings(note.Items),
	}
	return normalized, normalized.Version != "" || normalized.Date != "" || normalized.Title != "" ||
		normalized.Notes != "" || normalized.URL != "" || len(normalized.Items) > 0
}

func normalizeKinds(items []Kind) []Kind {
	seen := map[Kind]struct{}{}
	normalized := make([]Kind, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func normalizePlacements(items []Placement) []Placement {
	seen := map[Placement]struct{}{}
	normalized := make([]Placement, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized
}

func normalizeCapabilities(items []CapabilityDescriptor) []CapabilityDescriptor {
	seen := map[CapabilityDescriptor]struct{}{}
	normalized := make([]CapabilityDescriptor, 0, len(items))
	for _, item := range items {
		if item.Kind == "" || item.Name == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Kind != normalized[j].Kind {
			return normalized[i].Kind < normalized[j].Kind
		}
		if normalized[i].Subject != normalized[j].Subject {
			return normalized[i].Subject < normalized[j].Subject
		}
		return normalized[i].Name < normalized[j].Name
	})
	return normalized
}
