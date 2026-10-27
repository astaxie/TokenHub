package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) captureChatGPTSubscriptionCallback(ctx context.Context, state, redirectURI string, callback chatGPTSubscriptionCallback) error {
	invalid := NewHTTPError(http.StatusBadRequest, "invalid_oauth_state", "Subscription OAuth session is invalid or expired")
	var initial providerAccountOAuthSessionRecord
	if err := s.db.WithContext(ctx).Select("id").First(&initial, "state_hash = ? AND expires_at > ?", HashSecret(state), time.Now().UTC()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalid
		}
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Use the same lock as session consumption. The final conditional update
		// cannot recreate a session that cancellation or exchange already removed.
		if err := s.lockScopeForUpdate(tx, "provider_account_oauth", initial.ID); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var record providerAccountOAuthSessionRecord
		if err := query.First(&record, "id = ? AND state_hash = ? AND expires_at > ?", initial.ID, HashSecret(state), time.Now().UTC()).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return invalid
			}
			return err
		}
		if record.ClientID != "tokenhub:subscription:"+ProviderChatGPTSubscription || record.RedirectURI != redirectURI {
			return invalid
		}
		session, err := s.providerAccountOAuthSessionFromRecord(record, state)
		if err != nil {
			return err
		}
		var secret subscriptionOAuthSecret
		if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || !secret.AutomaticCallback || secret.ActorID == "" || !secret.ExpiresAt.After(time.Now()) {
			return invalid
		}
		if secret.Callback != nil {
			return NewHTTPError(http.StatusConflict, "subscription_oauth_callback_already_received", "Subscription OAuth callback was already received")
		}
		secret.Callback = &callback
		encoded, err := json.Marshal(secret)
		if err != nil {
			return err
		}
		protected, err := s.encryptSecretStrict(string(encoded))
		if err != nil {
			return err
		}
		updated := tx.Model(&providerAccountOAuthSessionRecord{}).Where("id = ? AND state_hash = ? AND code_verifier_encrypted = ? AND expires_at > ?", record.ID, record.StateHash, record.CodeVerifierEncrypted, time.Now().UTC()).Update("code_verifier_encrypted", protected)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return invalid
		}
		return nil
	})
}
