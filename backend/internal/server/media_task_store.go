package server

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/dbschema"
)

// Media task bindings store routing metadata only. Prompts and generated assets
// remain upstream; the opaque provider identifier is encrypted at rest.
type mediaTaskBinding struct {
	ID                 string `gorm:"primaryKey"`
	ProjectID          string
	APIKeyID           string
	AttributedUserID   string
	Model              string
	RouteID            string
	ProviderID         string
	ProviderModel      string
	ResourceID         string
	IdentityHash       string
	UpstreamCiphertext string
	ExpiresAt          int64
}

func mediaTaskBindingMigration() dbschema.Migration {
	return dbschema.Migration{Version: 8, Name: "add-media-task-bindings", Statements: []string{
		`CREATE TABLE IF NOT EXISTS media_task_bindings (id text PRIMARY KEY, project_id text NOT NULL, api_key_id text NOT NULL, attributed_user_id text NOT NULL, model text NOT NULL, route_id text NOT NULL, provider_id text NOT NULL, provider_model text NOT NULL, resource_id text NOT NULL, identity_hash text NOT NULL, upstream_ciphertext text NOT NULL, expires_at bigint NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_media_task_bindings_expiry ON media_task_bindings (expires_at)`,
	}, StatementBudget: 10}
}

type mediaTaskStore interface {
	SaveMediaTaskBindings(context.Context, []mediaTaskBinding, []string) error
	LoadMediaTaskBinding(context.Context, string) (mediaTaskBinding, string, bool, error)
}

func (s *GormStore) SaveMediaTaskBindings(ctx context.Context, bindings []mediaTaskBinding, ids []string) error {
	if len(bindings) == 0 || len(bindings) > maxMediaTaskBindingsPerSubmission || len(bindings) != len(ids) {
		return errors.New("invalid media task binding batch")
	}
	for i := range bindings {
		ciphertext, err := s.encryptResponseJobPayload([]byte(ids[i]))
		if err != nil {
			return err
		}
		bindings[i].UpstreamCiphertext = ciphertext
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM media_task_bindings WHERE id IN (SELECT id FROM media_task_bindings WHERE expires_at <= ? LIMIT ?)", time.Now().Unix(), maxMediaTaskBindingsPerSubmission).Error; err != nil {
			return err
		}
		return tx.Create(&bindings).Error
	})
}

func (s *GormStore) LoadMediaTaskBinding(ctx context.Context, id string) (mediaTaskBinding, string, bool, error) {
	var binding mediaTaskBinding
	err := s.db.WithContext(ctx).Where("id = ? AND expires_at > ?", id, time.Now().Unix()).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return binding, "", false, nil
	}
	if err != nil {
		return binding, "", false, err
	}
	upstream, err := s.decryptResponseJobPayload(binding.UpstreamCiphertext)
	return binding, string(upstream), err == nil, err
}
