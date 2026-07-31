package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type openAIIDTokenClaims struct {
	Email      string            `json:"email"`
	OpenAIAuth *openAIAuthClaims `json:"https://api.openai.com/auth,omitempty"`
}

type openAIAuthClaims struct {
	ChatGPTAccountID string                    `json:"chatgpt_account_id"`
	ChatGPTUserID    string                    `json:"chatgpt_user_id"`
	ChatGPTPlanType  string                    `json:"chatgpt_plan_type"`
	UserID           string                    `json:"user_id"`
	Organizations    []openAIOrganizationClaim `json:"organizations"`
}

type openAIOrganizationClaim struct {
	ID        string `json:"id"`
	IsDefault bool   `json:"is_default"`
}

func (s *GormStore) prepareProviderResourceForCreate(resource *ProviderResource) error {
	if resource == nil || !isOpenAIAccountResource(resource.ResourceType) {
		return nil
	}
	if strings.TrimSpace(resource.BaseURL) == "" {
		resource.BaseURL = openAICodexBaseURL
	}
	if resource.Options == nil {
		resource.Options = map[string]string{}
	}
	return s.mergeOpenAIAccountCredentials(resource, nil)
}

func (s *GormStore) prepareProviderResourceForUpdate(resource *ProviderResource, patch ProviderResource) error {
	if resource == nil || !isOpenAIAccountResource(resource.ResourceType) {
		return nil
	}
	if strings.TrimSpace(resource.BaseURL) == "" {
		resource.BaseURL = openAICodexBaseURL
	}
	if resource.Options == nil {
		resource.Options = map[string]string{}
	}
	return s.mergeOpenAIAccountCredentials(resource, &patch)
}

func (s *GormStore) mergeOpenAIAccountCredentials(resource *ProviderResource, patch *ProviderResource) error {
	creds := ProviderResourceCredentials{}
	if resource.Credentials != nil {
		creds = *resource.Credentials
	}
	if patch != nil && patch.Credentials != nil {
		creds = *patch.Credentials
	}
	if strings.TrimSpace(creds.AccessToken) == "" && resource.APIKey != "" && !strings.HasPrefix(resource.APIKey, "enc:v1:") {
		creds.AccessToken = resource.APIKey
	}
	if strings.TrimSpace(creds.AuthType) == "" {
		creds.AuthType = firstNonEmpty(resource.Options["auth_type"], "oauth")
	}
	if claims := decodeOpenAIIDTokenClaims(creds.IDToken); claims != nil {
		creds.Email = firstNonEmpty(creds.Email, claims.Email)
		if claims.OpenAIAuth != nil {
			creds.AccountID = firstNonEmpty(creds.AccountID, claims.OpenAIAuth.ChatGPTAccountID)
			creds.UserID = firstNonEmpty(creds.UserID, claims.OpenAIAuth.UserID, claims.OpenAIAuth.ChatGPTUserID)
			creds.PlanType = firstNonEmpty(creds.PlanType, claims.OpenAIAuth.ChatGPTPlanType)
			creds.OrganizationID = firstNonEmpty(creds.OrganizationID, defaultOpenAIOrganizationID(claims.OpenAIAuth.Organizations))
		}
	}
	if strings.TrimSpace(creds.AccessToken) != "" {
		resource.APIKey = creds.AccessToken
	}
	if hasOpenAIAccountSecret(creds) {
		encrypted, err := s.encryptOpenAIAccountCredentialBlob(creds)
		if err != nil {
			return fmt.Errorf("encrypt credential blob: %w", err)
		}
		resource.CredentialBlob = encrypted
	} else if patch == nil && resource.CredentialBlob == "" {
		resource.CredentialBlob = ""
	}
	applyOpenAIAccountOptions(resource.Options, creds)
	resource.Credentials = nil
	return nil
}

func hasOpenAIAccountSecret(creds ProviderResourceCredentials) bool {
	return strings.TrimSpace(creds.RefreshToken) != "" ||
		strings.TrimSpace(creds.IDToken) != "" ||
		strings.TrimSpace(creds.ClientID) != "" ||
		strings.TrimSpace(creds.Scopes) != "" ||
		strings.TrimSpace(creds.TokenType) != "" ||
		strings.TrimSpace(creds.ExpiresAt) != ""
}

func (s *GormStore) encryptOpenAIAccountCredentialBlob(creds ProviderResourceCredentials) (string, error) {
	secret := map[string]string{}
	addNonEmpty(secret, "auth_type", creds.AuthType)
	addNonEmpty(secret, "refresh_token", creds.RefreshToken)
	addNonEmpty(secret, "id_token", creds.IDToken)
	addNonEmpty(secret, "client_id", creds.ClientID)
	addNonEmpty(secret, "scopes", creds.Scopes)
	addNonEmpty(secret, "token_type", creds.TokenType)
	addNonEmpty(secret, "expires_at", creds.ExpiresAt)
	addNonEmpty(secret, "account_id", creds.AccountID)
	addNonEmpty(secret, "user_id", creds.UserID)
	addNonEmpty(secret, "email", creds.Email)
	addNonEmpty(secret, "organization_id", creds.OrganizationID)
	addNonEmpty(secret, "plan_type", creds.PlanType)
	data, err := json.Marshal(secret)
	if err != nil {
		return "", fmt.Errorf("marshal credential blob: %w", err)
	}
	return s.encryptSecret(string(data))
}

func applyOpenAIAccountOptions(options map[string]string, creds ProviderResourceCredentials) {
	if options == nil {
		return
	}
	for _, key := range []string{
		"access_token",
		"refresh_token",
		"id_token",
		"api_key",
		"credential_blob",
	} {
		delete(options, key)
	}
	options["credential_source"] = ProviderResourceOpenAISubscription
	options["auth_type"] = firstNonEmpty(creds.AuthType, options["auth_type"], "oauth")
	if strings.TrimSpace(creds.RefreshToken) != "" {
		options["has_refresh_token"] = "true"
	} else if options["has_refresh_token"] == "" {
		options["has_refresh_token"] = "false"
	}
	setOptionIfValue(options, "token_expires_at", creds.ExpiresAt)
	setOptionIfValue(options, "account_id", creds.AccountID)
	setOptionIfValue(options, "account_email", creds.Email)
	setOptionIfValue(options, "user_id", creds.UserID)
	setOptionIfValue(options, "organization_id", creds.OrganizationID)
	setOptionIfValue(options, "plan_type", creds.PlanType)
}

func isOpenAIAccountResource(resourceType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(resourceType))
	return normalized == ProviderResourceOpenAISubscription ||
		normalized == "openai_oauth" ||
		normalized == "openai_account"
}

func providerResourceCredentialSummary(resource ProviderResource) map[string]string {
	if !isOpenAIAccountResource(resource.ResourceType) {
		return nil
	}
	summary := map[string]string{}
	for _, key := range []string{
		"credential_source",
		"auth_type",
		"account_email",
		"account_id",
		"user_id",
		"organization_id",
		"plan_type",
		"token_expires_at",
		"has_refresh_token",
	} {
		if value := strings.TrimSpace(resource.Options[key]); value != "" {
			summary[key] = value
		}
	}
	if len(summary) == 0 {
		return nil
	}
	return summary
}

func redactProviderResourceSecrets(resource *ProviderResource) {
	if resource == nil {
		return
	}
	resource.APIKey = ""
	resource.CredentialBlob = ""
	resource.Credentials = nil
	resource.CredentialSummary = providerResourceCredentialSummary(*resource)
}

func (s *GormStore) RefreshProviderResourceCredentials(ctx context.Context, resourceID string, force bool) (ProviderResourceCredentials, error) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		return ProviderResourceCredentials{}, nil
	}

	var result ProviderResourceCredentials
	err := s.withClusterLease(ctx, "credential-refresh:"+resourceID, func(leaseCtx context.Context) error {
		s.mu.Lock()
		var resource ProviderResource
		if err := s.db.WithContext(leaseCtx).First(&resource, "id = ?", resourceID).Error; err != nil {
			s.mu.Unlock()
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		creds, err := s.providerResourceCredentialsForRuntime(resource)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("read credentials for runtime: %w", err)
		}
		if !isOpenAIAccountResource(resource.ResourceType) {
			result = creds
			s.mu.Unlock()
			return nil
		}
		needsRefresh, expired := providerResourceCredentialsNeedRefresh(creds, openAIAccountOAuthRefreshLead)
		if !force && !needsRefresh {
			result = creds
			s.mu.Unlock()
			return nil
		}
		if strings.TrimSpace(creds.RefreshToken) == "" {
			result = creds
			s.mu.Unlock()
			if expired {
				return NewHTTPError(503, "provider_resource_token_expired", "Provider resource access token expired and no refresh token is available")
			}
			return nil
		}
		s.mu.Unlock()

		refreshed, err := refreshOpenAIAccountOAuthCredentials(leaseCtx, creds)
		if err != nil {
			return err
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		var current ProviderResource
		if err := s.db.WithContext(leaseCtx).First(&current, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		if !isOpenAIAccountResource(current.ResourceType) {
			result = refreshed
			return nil
		}
		if current.Options == nil {
			current.Options = map[string]string{}
		}
		current.Credentials = &refreshed
		if err := s.mergeOpenAIAccountCredentials(&current, &ProviderResource{Credentials: &refreshed}); err != nil {
			return err
		}
		if strings.TrimSpace(current.APIKey) != "" {
			encrypted, err := s.encryptSecret(current.APIKey)
			if err != nil {
				return fmt.Errorf("encrypt refreshed API key: %w", err)
			}
			current.APIKey = encrypted
		}
		current.UpdatedAt = time.Now().UTC()
		if err := s.db.WithContext(leaseCtx).Save(&current).Error; err != nil {
			return err
		}
		var credsErr error
		result, credsErr = s.providerResourceCredentialsForRuntime(current)
		return credsErr
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *GormStore) providerResourceCredentialsForRuntime(resource ProviderResource) (ProviderResourceCredentials, error) {
	creds := ProviderResourceCredentials{}
	if strings.TrimSpace(resource.APIKey) != "" {
		decrypted, err := s.decryptSecret(resource.APIKey)
		if err != nil {
			return ProviderResourceCredentials{}, fmt.Errorf("decrypt API key: %w", err)
		}
		creds.AccessToken = decrypted
	}
	if strings.TrimSpace(resource.CredentialBlob) != "" {
		secret, err := s.decryptSecret(resource.CredentialBlob)
		if err != nil {
			return ProviderResourceCredentials{}, fmt.Errorf("decrypt credential blob: %w", err)
		}
		if strings.TrimSpace(secret) != "" {
			var blob ProviderResourceCredentials
			if err := json.Unmarshal([]byte(secret), &blob); err == nil {
				mergeProviderResourceCredentials(&creds, blob)
			}
		}
	}
	if resource.Options != nil {
		creds.AuthType = firstNonEmpty(creds.AuthType, resource.Options["auth_type"], "oauth")
		creds.ExpiresAt = firstNonEmpty(creds.ExpiresAt, resource.Options["token_expires_at"])
		creds.AccountID = firstNonEmpty(creds.AccountID, resource.Options["account_id"])
		creds.UserID = firstNonEmpty(creds.UserID, resource.Options["user_id"])
		creds.Email = firstNonEmpty(creds.Email, resource.Options["account_email"])
		creds.OrganizationID = firstNonEmpty(creds.OrganizationID, resource.Options["organization_id"])
		creds.PlanType = firstNonEmpty(creds.PlanType, resource.Options["plan_type"])
		creds.Scopes = firstNonEmpty(creds.Scopes, resource.Options["scopes"])
	}
	if strings.TrimSpace(creds.AuthType) == "" {
		creds.AuthType = "oauth"
	}
	return creds, nil
}

func mergeProviderResourceCredentials(target *ProviderResourceCredentials, source ProviderResourceCredentials) {
	target.AuthType = firstNonEmpty(source.AuthType, target.AuthType)
	target.AccessToken = firstNonEmpty(source.AccessToken, target.AccessToken)
	target.RefreshToken = firstNonEmpty(source.RefreshToken, target.RefreshToken)
	target.IDToken = firstNonEmpty(source.IDToken, target.IDToken)
	target.ClientID = firstNonEmpty(source.ClientID, target.ClientID)
	target.Scopes = firstNonEmpty(source.Scopes, target.Scopes)
	target.TokenType = firstNonEmpty(source.TokenType, target.TokenType)
	target.ExpiresAt = firstNonEmpty(source.ExpiresAt, target.ExpiresAt)
	target.AccountID = firstNonEmpty(source.AccountID, target.AccountID)
	target.UserID = firstNonEmpty(source.UserID, target.UserID)
	target.Email = firstNonEmpty(source.Email, target.Email)
	target.OrganizationID = firstNonEmpty(source.OrganizationID, target.OrganizationID)
	target.PlanType = firstNonEmpty(source.PlanType, target.PlanType)
}

func providerResourceCredentialsNeedRefresh(creds ProviderResourceCredentials, refreshLead time.Duration) (bool, bool) {
	expiresAt, ok := parseCredentialExpiry(creds.ExpiresAt)
	if !ok {
		return false, false
	}
	now := time.Now().UTC()
	if !expiresAt.After(now) {
		return true, true
	}
	return time.Until(expiresAt) < refreshLead, false
}

func parseCredentialExpiry(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func setOptionIfValue(options map[string]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		options[key] = value
	}
}

func addNonEmpty(values map[string]string, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		values[key] = value
	}
}

func decodeOpenAIIDTokenClaims(idToken string) *openAIIDTokenClaims {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) != 3 {
		return nil
	}
	payload := parts[1]
	if padding := len(payload) % 4; padding != 0 {
		payload += strings.Repeat("=", 4-padding)
	}
	data, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(payload)
	}
	if err != nil {
		return nil
	}
	var claims openAIIDTokenClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}
	return &claims
}

func defaultOpenAIOrganizationID(organizations []openAIOrganizationClaim) string {
	if len(organizations) == 0 {
		return ""
	}
	for _, organization := range organizations {
		if organization.IsDefault && strings.TrimSpace(organization.ID) != "" {
			return strings.TrimSpace(organization.ID)
		}
	}
	return strings.TrimSpace(organizations[0].ID)
}
