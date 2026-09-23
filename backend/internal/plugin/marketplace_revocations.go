package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	MarketplaceRevocationSchemaVersion = 1
	MaxMarketplaceRevocations          = 1000
)

type MarketplaceRevocationFeed struct {
	SchemaVersion int                     `json:"schema_version"`
	RepositoryID  string                  `json:"repository_id"`
	GeneratedAt   string                  `json:"generated_at"`
	ExpiresAt     string                  `json:"expires_at"`
	Revocations   []MarketplaceRevocation `json:"revocations"`
}

type MarketplaceRevocation struct {
	ID             string `json:"id"`
	Reason         string `json:"reason"`
	CreatedAt      string `json:"created_at"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	IndexKeyID     string `json:"index_key_id,omitempty"`
	PublisherKeyID string `json:"publisher_key_id,omitempty"`
	PublisherID    string `json:"publisher_id,omitempty"`
	PluginID       string `json:"plugin_id,omitempty"`
	Version        string `json:"version,omitempty"`
}

type MarketplaceRevocationMatch struct {
	RevocationID string
	Reason       string
	Target       string
}

func DecodeMarketplaceRevocations(data []byte) (MarketplaceRevocationFeed, error) {
	if len(data) > MaxMarketplaceIndexBytes {
		return MarketplaceRevocationFeed{}, fmt.Errorf("marketplace revocation feed cannot exceed %d bytes", MaxMarketplaceIndexBytes)
	}
	if err := validateMarketplaceJSONDepth(data); err != nil {
		return MarketplaceRevocationFeed{}, err
	}
	if err := rejectMarketplaceSecretMaterial(data); err != nil {
		return MarketplaceRevocationFeed{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var feed MarketplaceRevocationFeed
	if err := decoder.Decode(&feed); err != nil {
		return MarketplaceRevocationFeed{}, fmt.Errorf("decode marketplace revocation feed: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return MarketplaceRevocationFeed{}, fmt.Errorf("marketplace revocation feed contains trailing data")
	}
	return feed, ValidateMarketplaceRevocations(feed)
}

func ValidateMarketplaceRevocations(feed MarketplaceRevocationFeed) error {
	if feed.SchemaVersion != MarketplaceRevocationSchemaVersion {
		return fmt.Errorf("unsupported marketplace revocation schema_version %d", feed.SchemaVersion)
	}
	if !marketplaceSafeToken(feed.RepositoryID) {
		return fmt.Errorf("marketplace revocation repository_id is required and must be a safe token")
	}
	generatedAt, err := parseMarketplaceTime("generated_at", feed.GeneratedAt)
	if err != nil {
		return err
	}
	expiresAt, err := parseMarketplaceTime("expires_at", feed.ExpiresAt)
	if err != nil {
		return err
	}
	if !expiresAt.After(generatedAt) {
		return fmt.Errorf("marketplace revocation expires_at must be after generated_at")
	}
	if len(feed.Revocations) > MaxMarketplaceRevocations {
		return fmt.Errorf("marketplace revocation feed cannot contain more than %d records", MaxMarketplaceRevocations)
	}
	seen := map[string]struct{}{}
	for index, revocation := range feed.Revocations {
		if err := validateMarketplaceRevocation(revocation); err != nil {
			return fmt.Errorf("revocations[%d] %s: %w", index, strings.TrimSpace(revocation.ID), err)
		}
		id := strings.TrimSpace(revocation.ID)
		if _, ok := seen[id]; ok {
			return fmt.Errorf("marketplace revocation %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func MarketplaceRevocationMatches(index MarketplaceChannelIndex, indexKeyID string, feed MarketplaceRevocationFeed) []MarketplaceRevocationMatch {
	var matches []MarketplaceRevocationMatch
	indexKeyID = strings.TrimSpace(indexKeyID)
	for _, revocation := range feed.Revocations {
		matches = append(matches, marketplaceRevocationMatches(index, indexKeyID, revocation)...)
	}
	return matches
}

func validateMarketplaceRevocation(revocation MarketplaceRevocation) error {
	if !marketplaceSafeToken(revocation.ID) {
		return fmt.Errorf("id is required and must be a safe token")
	}
	if strings.TrimSpace(revocation.Reason) == "" {
		return fmt.Errorf("reason is required")
	}
	if _, err := parseMarketplaceTime("created_at", revocation.CreatedAt); err != nil {
		return err
	}
	targets := 0
	if strings.TrimSpace(revocation.ArtifactSHA256) != "" {
		if !marketplaceSHA256Pattern.MatchString(strings.TrimSpace(revocation.ArtifactSHA256)) {
			return fmt.Errorf("artifact_sha256 must be a lowercase SHA-256")
		}
		targets++
	}
	if strings.TrimSpace(revocation.IndexKeyID) != "" {
		if !marketplaceSafeToken(revocation.IndexKeyID) {
			return fmt.Errorf("index_key_id must be a safe token")
		}
		targets++
	}
	if strings.TrimSpace(revocation.PublisherKeyID) != "" {
		if !marketplaceSafeToken(revocation.PublisherKeyID) {
			return fmt.Errorf("publisher_key_id must be a safe token")
		}
		targets++
	}
	if strings.TrimSpace(revocation.PublisherID) != "" {
		if !marketplaceSafeToken(revocation.PublisherID) {
			return fmt.Errorf("publisher_id must be a safe token")
		}
		targets++
	}
	if strings.TrimSpace(revocation.PluginID) != "" {
		if !validMarketplacePluginID(revocation.PluginID) {
			return fmt.Errorf("plugin_id must be DNS-style")
		}
		targets++
	}
	if strings.TrimSpace(revocation.Version) != "" {
		if !validMarketplaceSemver(revocation.Version) {
			return fmt.Errorf("version must be SemVer")
		}
		if strings.TrimSpace(revocation.PluginID) == "" {
			return fmt.Errorf("plugin_id is required when version is set")
		}
		targets++
	}
	if targets == 0 {
		return fmt.Errorf("at least one revocation target is required")
	}
	return nil
}

func marketplaceRevocationMatches(index MarketplaceChannelIndex, indexKeyID string, revocation MarketplaceRevocation) []MarketplaceRevocationMatch {
	var matches []MarketplaceRevocationMatch
	id := strings.TrimSpace(revocation.ID)
	reason := strings.TrimSpace(revocation.Reason)
	if strings.TrimSpace(revocation.IndexKeyID) != "" && strings.TrimSpace(revocation.IndexKeyID) == indexKeyID {
		matches = append(matches, MarketplaceRevocationMatch{RevocationID: id, Reason: reason, Target: "index_key:" + indexKeyID})
	}
	for _, item := range index.Plugins {
		pluginID := strings.TrimSpace(item.ID)
		publisherID := strings.TrimSpace(item.Publisher.ID)
		for _, release := range item.Releases {
			version := strings.TrimSpace(release.Version)
			if revocationMatchesPluginVersion(revocation, publisherID, pluginID, version) {
				matches = append(matches, MarketplaceRevocationMatch{RevocationID: id, Reason: reason, Target: "plugin:" + pluginID + "@" + version})
			}
			for _, artifact := range release.Artifacts {
				if strings.TrimSpace(revocation.ArtifactSHA256) == strings.TrimSpace(artifact.SHA256) {
					matches = append(matches, MarketplaceRevocationMatch{RevocationID: id, Reason: reason, Target: "artifact:" + artifact.SHA256})
				}
				if strings.TrimSpace(revocation.PublisherKeyID) != "" && strings.TrimSpace(revocation.PublisherKeyID) == strings.TrimSpace(artifact.Signature.KeyID) {
					matches = append(matches, MarketplaceRevocationMatch{RevocationID: id, Reason: reason, Target: "publisher_key:" + artifact.Signature.KeyID})
				}
			}
		}
	}
	return matches
}

func revocationMatchesPluginVersion(revocation MarketplaceRevocation, publisherID string, pluginID string, version string) bool {
	if strings.TrimSpace(revocation.PublisherID) != "" && strings.TrimSpace(revocation.PublisherID) != publisherID {
		return false
	}
	if strings.TrimSpace(revocation.PluginID) != "" && strings.TrimSpace(revocation.PluginID) != pluginID {
		return false
	}
	if strings.TrimSpace(revocation.Version) != "" && strings.TrimSpace(revocation.Version) != version {
		return false
	}
	return strings.TrimSpace(revocation.PublisherID) != "" || strings.TrimSpace(revocation.PluginID) != "" || strings.TrimSpace(revocation.Version) != ""
}
