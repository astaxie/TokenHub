package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarketplaceRevocationMatchesIndexTargets(t *testing.T) {
	index := validMarketplaceIndexForTest()
	artifact := index.Plugins[0].Releases[0].Artifacts[0]
	feed := MarketplaceRevocationFeed{
		SchemaVersion: MarketplaceRevocationSchemaVersion,
		RepositoryID:  index.RepositoryID,
		GeneratedAt:   "2026-08-30T08:00:00Z",
		ExpiresAt:     "2026-09-06T08:00:00Z",
		Revocations: []MarketplaceRevocation{
			{ID: "artifact-revoked", Reason: "artifact compromised", CreatedAt: "2026-08-30T08:01:00Z", ArtifactSHA256: artifact.SHA256},
			{ID: "index-key-revoked", Reason: "index key retired", CreatedAt: "2026-08-30T08:02:00Z", IndexKeyID: "tokenhub-index-2026"},
			{ID: "publisher-key-revoked", Reason: "publisher key retired", CreatedAt: "2026-08-30T08:03:00Z", PublisherKeyID: artifact.Signature.KeyID},
			{ID: "version-revoked", Reason: "version yanked", CreatedAt: "2026-08-30T08:04:00Z", PublisherID: index.Plugins[0].Publisher.ID, PluginID: index.Plugins[0].ID, Version: index.Plugins[0].Releases[0].Version},
		},
	}
	if err := ValidateMarketplaceRevocations(feed); err != nil {
		t.Fatalf("validate revocations: %v", err)
	}
	matches := MarketplaceRevocationMatches(index, "tokenhub-index-2026", feed)
	if len(matches) != 4 {
		t.Fatalf("matches = %+v, want 4", matches)
	}
}

func TestDecodeMarketplaceRevocationsRejectsInvalidFeeds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*MarketplaceRevocationFeed)
		want   string
	}{
		{
			name: "missing target",
			mutate: func(feed *MarketplaceRevocationFeed) {
				feed.Revocations[0].ArtifactSHA256 = ""
			},
			want: "at least one revocation target",
		},
		{
			name: "duplicate id",
			mutate: func(feed *MarketplaceRevocationFeed) {
				feed.Revocations = append(feed.Revocations, feed.Revocations[0])
			},
			want: "duplicated",
		},
		{
			name: "invalid digest",
			mutate: func(feed *MarketplaceRevocationFeed) {
				feed.Revocations[0].ArtifactSHA256 = strings.Repeat("A", 64)
			},
			want: "artifact_sha256 must be a lowercase SHA-256",
		},
		{
			name: "version without plugin id",
			mutate: func(feed *MarketplaceRevocationFeed) {
				feed.Revocations[0].ArtifactSHA256 = ""
				feed.Revocations[0].Version = "1.0.0"
			},
			want: "plugin_id is required when version is set",
		},
		{
			name: "expired before generated",
			mutate: func(feed *MarketplaceRevocationFeed) {
				feed.ExpiresAt = "2026-08-29T08:00:00Z"
			},
			want: "expires_at must be after generated_at",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feed := MarketplaceRevocationFeed{
				SchemaVersion: MarketplaceRevocationSchemaVersion,
				RepositoryID:  "tokenhub-official-marketplace",
				GeneratedAt:   "2026-08-30T08:00:00Z",
				ExpiresAt:     "2026-09-06T08:00:00Z",
				Revocations: []MarketplaceRevocation{{
					ID:             "artifact-revoked",
					Reason:         "artifact compromised",
					CreatedAt:      "2026-08-30T08:01:00Z",
					ArtifactSHA256: strings.Repeat("a", 64),
				}},
			}
			tc.mutate(&feed)
			data, err := json.Marshal(feed)
			if err != nil {
				t.Fatalf("encode feed: %v", err)
			}
			_, err = DecodeMarketplaceRevocations(data)
			if err == nil {
				t.Fatal("invalid marketplace revocation feed decoded successfully")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}
