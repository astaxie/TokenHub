package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

type providerAllowanceSetting struct {
	SharedAccountID string
	ResourceID      string `gorm:"primaryKey"`
	Revision        string
	SourceEncrypted string
	TokenEncrypted  string
	PolicyJSON      string
	WalletSnapshot  string
	UpdatedAt       time.Time
}

type providerAllowanceConfig struct {
	ResourceID string             `json:"resource_id"`
	Revision   string             `json:"revision"`
	Source     quotasource.Config `json:"source"`
	Policy     allowance.Policy   `json:"policy"`
	HasToken   bool               `json:"has_token"`
	Token      string             `json:"token,omitempty"`
	ClearToken bool               `json:"clear_token,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at,omitzero"`
}

type providerAllowancePoint struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	ResourceID string    `json:"resource_id"`
	FetchedAt  time.Time `json:"fetched_at"`
	Digest     string    `json:"-"`
	Snapshot   string    `json:"-"`
}

type providerAllowanceEvent struct {
	ID         string    `json:"id" gorm:"primaryKey"`
	ResourceID string    `json:"resource_id"`
	Kind       string    `json:"kind"`
	Cycle      string    `json:"cycle"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Details    string    `json:"details"`
}

func (s *GormStore) providerAllowanceConfig(ctx context.Context, id string) (providerAllowanceConfig, error) {
	return s.readProviderAllowanceConfig(s.db.WithContext(ctx), id)
}

func (s *GormStore) readProviderAllowanceConfig(db *gorm.DB, id string) (providerAllowanceConfig, error) {
	out := providerAllowanceConfig{ResourceID: id}
	var row providerAllowanceSetting
	if err := db.First(&row, "resource_id = ?", id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	} else if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(s.decryptSecret(row.SourceEncrypted)), &out.Source) != nil || json.Unmarshal([]byte(row.PolicyJSON), &out.Policy) != nil {
		return out, NewHTTPError(500, "allowance_config_unreadable", "Allowance configuration cannot be read")
	}
	out.Revision = row.Revision
	out.Token = s.decryptSecret(row.TokenEncrypted)
	if row.TokenEncrypted != "" && out.Token == "" {
		return out, NewHTTPError(500, "allowance_config_unreadable", "Allowance credentials cannot be read")
	}
	out.HasToken = out.Token != ""
	out.UpdatedAt = row.UpdatedAt
	return out, nil
}

func publicProviderAllowanceConfig(c providerAllowanceConfig) providerAllowanceConfig {
	c.Token = ""
	c.ClearToken = false
	if c.Source.Headers != nil {
		headers := map[string]string{}
		for k := range c.Source.Headers {
			headers[k] = "[redacted]"
		}
		c.Source.Headers = headers
	}
	return c
}

func (s *GormStore) saveProviderAllowanceConfig(ctx context.Context, id string, req providerAllowanceConfig) (providerAllowanceConfig, error) {
	var saved providerAllowanceConfig
	err := s.withClusterLease(ctx, providerResourceMutationLeaseName(id), func(ctx context.Context) error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var resource ProviderResource
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&resource, "id = ?", id).Error; err != nil {
				return notFound(err, "provider_resource_not_found", "Provider resource not found")
			}
			current, err := s.readProviderAllowanceConfig(tx, id)
			if err != nil {
				return err
			}
			if req.Revision != current.Revision {
				return NewHTTPError(http.StatusConflict, "allowance_config_changed", "Allowance configuration changed; reload before saving")
			}
			if req.Token == "" && !req.ClearToken {
				req.Token = current.Token
			}
			if req.ClearToken {
				req.Token = ""
			}
			for key, value := range req.Source.Headers {
				if value == "[redacted]" {
					value = current.Source.Headers[key]
					if value == "" {
						delete(req.Source.Headers, key)
					} else {
						req.Source.Headers[key] = value
					}
				}
			}
			if err := req.Source.Validate(); err != nil {
				return NewHTTPError(400, "invalid_allowance_source", err.Error())
			}
			if req.Source.Auth == "" {
				switch req.Source.Preset {
				case "volcengine-coding", "volcengine-agent":
					req.Source.Auth = "volcengine"
				case "tencent":
					req.Source.Auth = "tencent"
				case "alibaba":
					req.Source.Auth = "alibaba"
				}
			}
			if (req.Source.Auth == "volcengine" || req.Source.Auth == "tencent" || req.Source.Auth == "alibaba") && (req.Source.AccessKeyID == "" || req.Token == "") {
				return NewHTTPError(400, "allowance_access_key_required", "Cloud quota requires an access key ID and a separate quota secret")
			}
			for _, channelID := range req.Policy.Alerts.ChannelIDs {
				var channel AdminResource
				if err := tx.Where("id = ? AND kind = ? AND status = ?", channelID, "notification-channels", StatusActive).First(&channel).Error; err != nil {
					return NewHTTPError(400, "invalid_allowance_notification_channel", "Quota alert notification channel is unavailable")
				}
			}
			normalizedCaps := map[string]allowance.Cap{}
			for key, cap := range req.Policy.Windows {
				key = strings.ToLower(strings.TrimSpace(key))
				if _, exists := normalizedCaps[key]; exists {
					return NewHTTPError(400, "invalid_allowance_policy", "Duplicate window cap")
				}
				normalizedCaps[key] = cap
			}
			if req.Policy.Windows != nil {
				req.Policy.Windows = normalizedCaps
			}
			if err := req.Policy.Validate(); err != nil {
				return NewHTTPError(400, "invalid_allowance_policy", err.Error())
			}
			if req.Source.SharedAccountID != "" {
				var target ProviderResource
				if err := tx.First(&target, "id = ? AND provider_id = ?", req.Source.SharedAccountID, resource.ProviderID).Error; err != nil || target.ID == id {
					return NewHTTPError(400, "invalid_shared_allowance_account", "Shared wallet must reference another account of the same provider")
				}
				targetCfg, err := s.readProviderAllowanceConfig(tx, target.ID)
				if err != nil {
					return err
				}
				if targetCfg.Source.SharedAccountID != "" {
					return NewHTTPError(400, "invalid_shared_allowance_account", "A shared wallet must reference its original account")
				}
				newRevision := allowanceAccountRevision(target)
				if req.Source.SharedAccountRevision != "" && req.Source.SharedAccountRevision != newRevision {
					return NewHTTPError(409, "shared_wallet_account_changed", "Shared wallet account changed; select the account again")
				}
				req.Source.SharedAccountRevision = newRevision
			}
			data, err := json.Marshal(req.Source)
			if err != nil {
				return err
			}
			source, err := s.encryptSecretStrict(string(data))
			if err != nil {
				return err
			}
			token := ""
			if req.Token != "" {
				token, err = s.encryptSecretStrict(req.Token)
				if err != nil {
					return err
				}
			}
			policy, err := json.Marshal(req.Policy)
			if err != nil {
				return err
			}
			row := providerAllowanceSetting{SharedAccountID: req.Source.SharedAccountID, ResourceID: id, Revision: NewID("alrev"), SourceEncrypted: source, TokenEncrypted: token, PolicyJSON: string(policy), UpdatedAt: time.Now().UTC()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"revision", "source_encrypted", "token_encrypted", "policy_json", "updated_at", "shared_account_id"})}).Create(&row).Error; err != nil {
				return err
			}
			oldSource, _ := json.Marshal(current.Source)
			sourceChanged := string(oldSource) != string(data) || current.Token != req.Token
			if sourceChanged {
				// Changing collection rules invalidates the cached reading. It cannot be
				// interpreted using the new rules until a successful refresh arrives.
				if err := tx.Model(&ProviderResourceObservation{}).Where("resource_id = ?", id).Updates(map[string]any{"quota_snapshot": "", "quota_fetched_at": nil}).Error; err != nil {
					return err
				}
			}
			req.ResourceID = id
			req.Revision = row.Revision
			req.HasToken = req.Token != ""
			req.UpdatedAt = row.UpdatedAt
			saved = publicProviderAllowanceConfig(req)
			return nil
		})
	})
	return saved, err
}

func recordProviderAllowancePoint(db *gorm.DB, id string, snapshot string, now time.Time) error {
	a := allowance.Read([]byte(snapshot), now)
	if a.Version != allowance.Version {
		return nil
	}
	// Observation timestamps do not turn identical readings into history points.
	comparable := a
	comparable.FetchedAt = time.Time{}
	comparable.CheckedAt = time.Time{}
	if comparable.Resets != nil {
		copy := *comparable.Resets
		copy.CheckedAt = time.Time{}
		comparable.Resets = &copy
	}
	data, err := json.Marshal(comparable)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	var last providerAllowancePoint
	err = db.Where("resource_id = ?", id).Order("fetched_at desc").First(&last).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if last.Digest == digest || !last.FetchedAt.IsZero() && !now.After(last.FetchedAt) {
		return nil
	}
	normalized, err := json.Marshal(a)
	if err != nil {
		return err
	}
	point := providerAllowancePoint{ID: NewID("alp"), ResourceID: id, FetchedAt: now.UTC(), Digest: digest, Snapshot: string(normalized)}
	return db.Create(&point).Error
}

func (s *GormStore) providerAllowanceHistory(ctx context.Context, id string, since time.Time, limit int) ([]providerAllowancePoint, error) {
	if limit <= 0 || limit > 6000 {
		limit = 6000
	}
	minimum := time.Now().UTC().Add(-45 * 24 * time.Hour)
	if since.Before(minimum) {
		since = minimum
	}
	var points []providerAllowancePoint
	err := s.db.WithContext(ctx).Where("resource_id = ? AND fetched_at >= ?", id, since.UTC()).Order("fetched_at desc").Limit(limit).Find(&points).Error
	return points, err
}

func (s *GormStore) pruneProviderAllowanceHistory(ctx context.Context) error {
	if err := s.db.WithContext(ctx).Where("observed_at < ?", time.Now().UTC().Add(-45*24*time.Hour)).Delete(&providerAllowanceWalletCounter{}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Where("fetched_at < ?", time.Now().UTC().Add(-45*24*time.Hour)).Delete(&providerAllowancePoint{}).Error
}

func allowanceSettingRevision(tx *gorm.DB, id string) (string, error) {
	var row providerAllowanceSetting
	if err := tx.First(&row, "resource_id = ?", strings.TrimSpace(id)).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	} else {
		return row.Revision, err
	}
}

func clearProviderAllowanceResources(tx *gorm.DB, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, model := range []any{&providerAllowanceSetting{}, &providerAllowancePoint{}, &providerAllowanceEvent{}, &providerAllowanceSession{}, &providerAllowanceWalletCounter{}} {
		if err := tx.Where("resource_id IN ?", ids).Delete(model).Error; err != nil {
			return err
		}
	}
	return nil
}
