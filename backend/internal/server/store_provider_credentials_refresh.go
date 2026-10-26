package server

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"
)

type providerCredentialRefreshPersistenceKey struct{}

func updateExistingProviderResourceColumns(db *gorm.DB, resource *ProviderResource, columns ...string) error {
	query := db.Model(&ProviderResource{}).Where("id = ?", resource.ID)
	expected, guarded := db.Statement.Context.Value(providerCredentialRefreshPersistenceKey{}).(ProviderResource)
	if guarded {
		// Deletion does not take the mutation lease. Keep the original identity
		// in the final UPDATE predicate so a reused ID cannot receive this result.
		query = query.Where("id = ? AND provider_id = ? AND resource_type = ? AND created_at = ?", expected.ID, expected.ProviderID, expected.ResourceType, expected.CreatedAt)
	}
	result := query.Select(columns).Updates(resource)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		if guarded {
			var count int64
			// Ignore update-only conditions when distinguishing replacement from deletion.
			if err := db.Session(&gorm.Session{NewDB: true}).Model(&ProviderResource{}).Where("id = ?", resource.ID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return NewHTTPError(http.StatusConflict, "provider_resource_changed", "Provider resource changed during credential refresh")
			}
		}
		return NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	return nil
}

func (s *GormStore) updateProviderResource(ctx context.Context, id string, patch ProviderResource) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateProviderResourceLocked(ctx, id, patch)
}

// ApplyProviderResourceCredentialRefresh checks the account binding and persists
// the result under the same mutation lease and lock as account replacement.
func (s *GormStore) ApplyProviderResourceCredentialRefresh(ctx context.Context, expected ProviderResource, credentials *ProviderResourceCredentials, reauthorizationRequired bool) (ProviderResource, error) {
	if err := validateProviderResourceSession(credentials); err != nil {
		return ProviderResource{}, err
	}
	var updated ProviderResource
	err := s.withClusterLease(ctx, providerResourceMutationLeaseName(expected.ID), func(leaseCtx context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		leaseCtx = context.WithValue(leaseCtx, providerCredentialRefreshPersistenceKey{}, expected)
		db := s.db.WithContext(leaseCtx)
		var current ProviderResource
		if err := db.First(&current, "id = ?", expected.ID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		if current.ProviderID != expected.ProviderID || current.ResourceType != expected.ResourceType ||
			!current.CreatedAt.Equal(expected.CreatedAt) ||
			providerCredentialReplacementGeneration(current.Options) != providerCredentialReplacementGeneration(expected.Options) {
			return NewHTTPError(http.StatusConflict, "provider_resource_changed", "Provider resource changed during credential refresh")
		}
		if credentials != nil {
			var err error
			updated, err = s.updateProviderResourceLocked(leaseCtx, current.ID, providerResourceCredentialPatch(current, credentials))
			return err
		}
		if reauthorizationRequired {
			if current.Options == nil {
				current.Options = map[string]string{}
			}
			current.Options[providerResourceReauthorizationRequiredOption] = "true"
			current.UpdatedAt = time.Now().UTC()
			if err := updateExistingProviderResourceColumns(db, &current, "options", "updated_at"); err != nil {
				return err
			}
		}
		s.maskProviderResourceHeaderConfig(&current)
		redactProviderResourceSecrets(&current)
		updated = current
		return nil
	})
	return updated, err
}
