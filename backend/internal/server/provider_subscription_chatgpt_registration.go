package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type subscriptionOAuthHostRecord struct {
	ID              string `gorm:"primaryKey"`
	HostIDEncrypted string `gorm:"not null"`
}

func (subscriptionOAuthHostRecord) TableName() string { return "subscription_oauth_hosts" }

type subscriptionOAuthRegistrationRecord struct {
	ID              string `gorm:"primaryKey"`
	ActorID         string `gorm:"not null"`
	HostID          string `gorm:"not null"`
	SecretEncrypted string `gorm:"not null"`
}

func (subscriptionOAuthRegistrationRecord) TableName() string {
	return "subscription_oauth_registrations"
}

type chatGPTSubscriptionRegistration struct {
	ID        string `json:"-"`
	HostID    string `json:"-"`
	ClientID  string `json:"client_id,omitempty"`
	Subject   string `json:"subject,omitempty"`
	protected string
}

type chatGPTSubscriptionRegistrationStore interface {
	chatGPTSubscriptionRegistration(context.Context, string, string) (chatGPTSubscriptionRegistration, error)
	chatGPTSubscriptionRegistrationForResource(context.Context, string, string, string) (chatGPTSubscriptionRegistration, error)
	bindChatGPTSubscriptionRegistration(context.Context, string, string, string, string, string) error
}

func invalidChatGPTSubscriptionRegistration() error {
	return NewHTTPError(400, "subscription_oauth_registration_invalid", "ChatGPT API registration is unavailable for this authorization attempt")
}

func (s *GormStore) chatGPTSubscriptionRegistration(ctx context.Context, actor, id string) (chatGPTSubscriptionRegistration, error) {
	if strings.TrimSpace(actor) == "" || len(id) > 256 {
		return chatGPTSubscriptionRegistration{}, invalidChatGPTSubscriptionRegistration()
	}
	hostID, err := s.chatGPTSubscriptionHostID(ctx)
	if err != nil {
		return chatGPTSubscriptionRegistration{}, err
	}
	if id == "" {
		protected, err := s.encryptSecretStrict(`{}`)
		if err != nil {
			return chatGPTSubscriptionRegistration{}, err
		}
		record := subscriptionOAuthRegistrationRecord{ID: NewID("subscription_registration"), ActorID: actor, HostID: ProviderChatGPTSubscription, SecretEncrypted: protected}
		if err := s.db.WithContext(ctx).Create(&record).Error; err != nil {
			return chatGPTSubscriptionRegistration{}, err
		}
		return chatGPTSubscriptionRegistration{ID: record.ID, HostID: hostID}, nil
	}
	var record subscriptionOAuthRegistrationRecord
	if err := s.db.WithContext(ctx).First(&record, "id = ? AND actor_id = ? AND host_id = ?", id, actor, ProviderChatGPTSubscription).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return chatGPTSubscriptionRegistration{}, invalidChatGPTSubscriptionRegistration()
		}
		return chatGPTSubscriptionRegistration{}, err
	}
	var registration chatGPTSubscriptionRegistration
	if json.Unmarshal([]byte(s.decryptSecret(record.SecretEncrypted)), &registration) != nil {
		return registration, invalidChatGPTSubscriptionRegistration()
	}
	registration.ID, registration.HostID = record.ID, hostID
	registration.protected = record.SecretEncrypted
	return registration, nil
}

func (s *GormStore) chatGPTSubscriptionHostID(ctx context.Context) (string, error) {
	random, err := randomHex(16)
	if err != nil {
		return "", err
	}
	hostID := "urn:uuid:" + random[:8] + "-" + random[8:12] + "-4" + random[13:16] + "-a" + random[17:20] + "-" + random[20:]
	protected, err := s.encryptSecretStrict(hostID)
	if err != nil {
		return "", err
	}
	candidate := subscriptionOAuthHostRecord{ID: ProviderChatGPTSubscription, HostIDEncrypted: protected}
	// All administrators and restarts of this gateway installation reuse its host.
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
		return "", err
	}
	var stored subscriptionOAuthHostRecord
	if err := s.db.WithContext(ctx).First(&stored, "id = ?", candidate.ID).Error; err != nil {
		return "", err
	}
	hostID = s.decryptSecret(stored.HostIDEncrypted)
	if !strings.HasPrefix(hostID, "urn:uuid:") {
		return "", invalidChatGPTSubscriptionRegistration()
	}
	return hostID, nil
}

func (s *GormStore) bindChatGPTSubscriptionRegistration(ctx context.Context, actor, id, hostID, clientID, subject string) error {
	if id == "" || actor == "" || clientID == "" || clientID == "dynamic_agent_client" || len(clientID) > 1024 {
		return invalidChatGPTSubscriptionRegistration()
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "subscription_oauth_registration", id); err != nil {
			return err
		}
		view := *s
		view.db = tx
		registration, err := view.chatGPTSubscriptionRegistration(ctx, actor, id)
		if err != nil {
			return err
		}
		if registration.HostID != hostID {
			return invalidChatGPTSubscriptionRegistration()
		}
		if registration.ClientID != "" && registration.ClientID != clientID {
			return NewHTTPError(400, "subscription_oauth_client_mismatch", "OAuth client does not match the session")
		}
		if registration.Subject != "" && subject != "" && registration.Subject != subject {
			return NewHTTPError(400, "subscription_oauth_identity_mismatch", "ChatGPT API account does not match the selected registration")
		}
		registration.ClientID = clientID
		registration.Subject = firstNonEmpty(registration.Subject, subject)
		encoded, err := json.Marshal(registration)
		if err != nil {
			return err
		}
		protected, err := s.encryptSecretStrict(string(encoded))
		if err != nil {
			return err
		}
		updated := tx.Model(&subscriptionOAuthRegistrationRecord{}).Where("id = ? AND actor_id = ? AND secret_encrypted = ?", id, actor, registration.protected).Update("secret_encrypted", protected)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return invalidChatGPTSubscriptionRegistration()
		}
		return nil
	})
}

func (s *GormStore) chatGPTSubscriptionRegistrationForResource(ctx context.Context, actor, id, resourceID string) (chatGPTSubscriptionRegistration, error) {
	var resource ProviderResource
	if err := s.db.WithContext(ctx).First(&resource, "id = ? AND resource_type = ?", resourceID, ProviderResourceChatGPTSubscription).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return chatGPTSubscriptionRegistration{}, invalidChatGPTSubscriptionRegistration()
		}
		return chatGPTSubscriptionRegistration{}, err
	}
	var provider Provider
	if err := s.db.WithContext(ctx).First(&provider, "id = ? AND type = ?", resource.ProviderID, ProviderChatGPTSubscription).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return chatGPTSubscriptionRegistration{}, invalidChatGPTSubscriptionRegistration()
		}
		return chatGPTSubscriptionRegistration{}, err
	}
	credentials := s.providerResourceCredentialsForRuntimeForProvider(provider.Type, resource)
	credentials.UserID = chatGPTSubscriptionStoredSubject(credentials)
	if credentials.ClientID == "" || credentials.ClientID == "dynamic_agent_client" || credentials.UserID == "" {
		return chatGPTSubscriptionRegistration{}, invalidChatGPTSubscriptionRegistration()
	}
	registration, err := s.chatGPTSubscriptionRegistration(ctx, actor, id)
	if err != nil {
		return registration, err
	}
	if err := s.bindChatGPTSubscriptionRegistration(ctx, actor, registration.ID, registration.HostID, credentials.ClientID, credentials.UserID); err != nil {
		return chatGPTSubscriptionRegistration{}, err
	}
	registration.ClientID, registration.Subject = credentials.ClientID, credentials.UserID
	return registration, nil
}

func chatGPTSubscriptionCallbackClientID(bound, returned string) (string, error) {
	if strings.TrimSpace(returned) != returned {
		return "", NewHTTPError(400, "subscription_oauth_invalid", "ChatGPT API OAuth callback is incomplete")
	}
	if bound != "" && returned != "" && bound != returned {
		return "", NewHTTPError(400, "subscription_oauth_client_mismatch", "OAuth client does not match the session")
	}
	clientID := firstNonEmpty(bound, returned)
	if clientID == "" || clientID == "dynamic_agent_client" || len(clientID) > 1024 || strings.TrimSpace(clientID) != clientID {
		return "", NewHTTPError(400, "subscription_oauth_invalid", "ChatGPT API OAuth callback is incomplete")
	}
	return clientID, nil
}
