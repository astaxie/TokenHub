package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/quotasource"
)

func automaticAllowanceConfig(c providerAllowanceConfig, r ProviderResource, p Provider) providerAllowanceConfig {
	if !c.Source.Enabled && c.Source.URL == "" && c.Source.Preset == "" && r.ResourceType == ProviderResourceAPIKey && p.Type != ProviderMock && (r.APIKey != "" || p.APIKey != "") {
		base := firstNonEmpty(r.BaseURL, p.BaseURL)
		if _, known := quotasource.AutomaticSource(base, c.Token); !known && (c.Source.NoBalanceProbe || quotasource.RelayOrigin(base) == "") {
			return c
		}
		c.Source.Enabled = true
		c.Source.Auth = "magpie"
	}
	return c
}

type allowanceProbe struct {
	Source  quotasource.Config `json:"source"`
	Missing bool               `json:"missing"`
}

// Probe outcomes use Magpie's 24-hour hit, six-hour miss and five-minute
// transport-failure lifetimes. The shared encrypted session table survives restarts.
func (s *Server) queryAutomaticAllowance(ctx context.Context, cfg providerAllowanceConfig, resource ProviderResource, provider Provider, preview bool) (quotasource.Result, error) {
	base := firstNonEmpty(resource.BaseURL, provider.BaseURL)
	key := firstNonEmpty(resource.APIKey, provider.APIKey)
	if source, ok := quotasource.AutomaticSource(base, cfg.Token); ok {
		source.Headers = cfg.Source.Headers
		cfg.Source = source
		return s.queryConfiguredAllowance(ctx, cfg, resource, provider, preview)
	}
	origin := quotasource.RelayOrigin(base)
	if cfg.Source.NoBalanceProbe || origin == "" || key == "" {
		return quotasource.Result{}, errors.New("no balance query configured")
	}
	store := s.store.(*GormStore)
	revision := fmt.Sprintf("probe_%x", sha256.Sum256([]byte(base+"\x00"+key+"\x00"+cfg.Revision)))
	var saved providerAllowanceSession
	if !preview && store.db.WithContext(ctx).Where("resource_id = ? AND revision = ? AND expires_at > ?", resource.ID, revision, time.Now().UTC()).First(&saved).Error == nil {
		var found allowanceProbe
		if json.Unmarshal([]byte(store.decryptSecret(saved.TokenEncrypted)), &found) == nil {
			if found.Missing {
				return quotasource.Result{}, errors.New("no balance query found at this address")
			}
			cfg.Source = found.Source
			return s.queryConfiguredAllowance(ctx, cfg, resource, provider, false)
		}
	}
	remember := func(source quotasource.Config, missing bool, ttl time.Duration) {
		if preview {
			return
		}
		body, _ := json.Marshal(allowanceProbe{Source: source, Missing: missing})
		encrypted, err := store.encryptSecretStrict(string(body))
		if err != nil {
			return
		}
		row := providerAllowanceSession{ResourceID: resource.ID, Revision: revision, TokenEncrypted: encrypted, ExpiresAt: time.Now().UTC().Add(ttl)}
		_ = store.withClusterLease(ctx, providerResourceMutationLeaseName(resource.ID), func(ctx context.Context) error {
			return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				var current ProviderResource
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", resource.ID).Error; err != nil {
					return err
				}
				if !sameProviderAccountSnapshot(current, resource) {
					return nil
				}
				actual, err := allowanceSettingRevision(tx, resource.ID)
				if err != nil {
					return err
				}
				if actual != cfg.Revision {
					return nil
				}
				return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"revision", "token_encrypted", "expires_at"})}).Create(&row).Error
			})
		})
	}
	allDown := true
	for _, path := range []string{"/v1/usage", "/api/usage/token"} {
		source := quotasource.Config{Enabled: true, Auth: "magpie", URL: origin + path, Expression: quotasource.DefaultBalanceExpression(origin + path), Headers: cfg.Source.Headers}
		if path == "/v1/usage" {
			source.Preset = "sub2api"
		}
		cfg.Source = source
		result, err := s.queryConfiguredAllowance(ctx, cfg, resource, provider, preview)
		if err == nil {
			remember(source, false, 24*time.Hour)
			return result, nil
		}
		if !strings.Contains(err.Error(), "request failed") {
			allDown = false
		}
	}
	ttl := 6 * time.Hour
	if allDown {
		ttl = 5 * time.Minute
	}
	remember(quotasource.Config{}, true, ttl)
	return quotasource.Result{}, errors.New("no balance query found at this address")
}
