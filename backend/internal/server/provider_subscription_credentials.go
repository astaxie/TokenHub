package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ReplaceProviderSession persists rotations only while the account still has the
// authentication that started the operation. A concurrent administrator edit
// must never be overwritten by a late upstream response.
func (s *GormStore) ReplaceProviderSession(ctx context.Context, resourceID, providerType, previous, next string) error {
	if err := validateProviderSessionJSON(next); err != nil {
		return err
	}
	return s.withClusterLease(ctx, providerResourceMutationLeaseName(resourceID), func(leaseCtx context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		var resource ProviderResource
		if err := s.db.WithContext(leaseCtx).First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		var provider Provider
		if err := s.db.WithContext(leaseCtx).First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
			return err
		}
		credentials := s.providerResourceCredentialsForRuntimeForProvider(provider.Type, resource)
		if provider.Type != providerType || !s.IsProviderAccountResourceType(provider.Type, resource.ResourceType) || strings.TrimSpace(credentials.SessionJSON) != strings.TrimSpace(previous) {
			return NewHTTPError(409, "provider_credentials_changed", "Account credentials changed during the request; retry with the current account")
		}
		credentials.SessionJSON = next
		resource.CredentialBlob = s.encryptProviderResourceCredentialBlob(credentials)
		resource.UpdatedAt = time.Now().UTC()
		return updateExistingProviderResourceColumns(s.db.WithContext(leaseCtx), &resource, "credential_blob", "updated_at")
	})
}

func validateProviderSessionJSON(value string) error {
	var object map[string]json.RawMessage
	if len(value) > 1<<20 || json.Unmarshal([]byte(value), &object) != nil || len(object) == 0 {
		return NewHTTPError(400, "provider_session_invalid", "Provider session must be a nonempty JSON object under 1 MiB")
	}
	return nil
}

func validateProviderResourceSession(credentials *ProviderResourceCredentials) error {
	if credentials != nil && credentials.Replace && strings.TrimSpace(credentials.AccessToken) == "" && strings.TrimSpace(credentials.SessionJSON) == "" {
		return NewHTTPError(400, "provider_credentials_required", "Replacing account credentials requires a new access token or session JSON")
	}
	if credentials == nil || strings.TrimSpace(credentials.SessionJSON) == "" {
		return nil
	}
	return validateProviderSessionJSON(credentials.SessionJSON)
}
