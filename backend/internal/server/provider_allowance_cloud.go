package server

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/quotasource"
)

type providerAllowanceSession struct {
	ResourceID     string `gorm:"primaryKey"`
	Revision       string
	TokenEncrypted string
	ExpiresAt      time.Time
}

func (s *Server) fetchConfiguredAllowanceBody(ctx context.Context, c quotasource.Config, secret, id, revision string) ([]byte, error) {
	if c.Auth != "alibaba" {
		return s.fetchAllowanceBody(ctx, c, secret)
	}
	store, ok := s.store.(*GormStore)
	if !ok {
		return nil, errors.New("quota credential store is unavailable")
	}
	for attempt := 0; attempt < 2; attempt++ {
		var cached providerAllowanceSession
		token := ""
		if attempt == 0 && store.db.WithContext(ctx).First(&cached, "resource_id = ? AND revision = ? AND expires_at > ?", id, revision, time.Now().UTC().Add(time.Minute)).Error == nil {
			token = store.decryptSecret(cached.TokenEncrypted)
		}
		if token == "" {
			mint, err := s.fetchAllowanceBody(ctx, c, secret)
			if err != nil {
				return nil, err
			}
			value, err := quotasource.ReadValue(mint, "cliAccessToken")
			if err != nil {
				return nil, errors.New("the Alibaba quota token was not returned")
			}
			token, _ = value.(string)
			if token == "" {
				return nil, errors.New("the Alibaba quota token is empty")
			}
			encrypted, err := store.encryptSecretStrict(token)
			if err != nil {
				return nil, err
			}
			session := providerAllowanceSession{ResourceID: id, Revision: revision, TokenEncrypted: encrypted, ExpiresAt: time.Now().UTC().Add(20 * time.Minute)}
			if err := store.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_id"}}, DoUpdates: clause.AssignmentColumns([]string{"revision", "token_encrypted", "expires_at"})}).Create(&session).Error; err != nil {
				return nil, err
			}
		}
		req, err := quotasource.AlibabaUsageRequest(ctx, c.Region, token)
		if err != nil {
			return nil, err
		}
		body, err := s.doAllowanceRequest(req)
		if err == nil {
			if value, _ := quotasource.ReadValue(body, "data.success"); value != false {
				return body, nil
			}
		}
		_ = store.db.WithContext(ctx).Where("resource_id = ? AND revision = ?", id, revision).Delete(&providerAllowanceSession{}).Error
	}
	return nil, errors.New("the Alibaba quota gateway refused the query")
}
