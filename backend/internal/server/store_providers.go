package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) AddProvider(provider Provider) Provider {
	s.mu.Lock()
	defer s.mu.Unlock()

	if provider.ID == "" {
		provider.ID = NewID("prv")
	}
	if provider.Status == "" {
		provider.Status = StatusActive
	}
	if !provider.Healthy {
		provider.Healthy = true
	}
	if provider.CreatedAt.IsZero() {
		provider.CreatedAt = time.Now().UTC()
	}
	if provider.Type == ProviderOpenAICodex {
		provider.APIKey = ""
		if codexProviderBaseURLNeedsNormalization(provider.BaseURL) {
			provider.BaseURL = openAICodexBaseURL
		}
	}
	if headers, sensitive, err := s.protectProviderHeaders(provider.Headers, provider.SensitiveHeaders, nil, nil); err == nil {
		provider.Headers = headers
		provider.SensitiveHeaders = sensitive
	} else {
		log.Printf("[tokenhub] refusing invalid headers for trusted provider create id=%s: %v", provider.ID, err)
		provider.Headers = nil
		provider.SensitiveHeaders = nil
	}
	provider.APIKey = s.encryptSecret(provider.APIKey)
	_ = s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&provider).Error
	provider.APIKey = ""
	provider.Headers = maskedProviderHeaders(s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders), provider.SensitiveHeaders)
	return provider
}

func (s *GormStore) ListProviders() []Provider {
	var items []Provider
	_ = s.db.Order("priority asc").Find(&items).Error
	for i := range items {
		items[i].APIKey = ""
		items[i].Headers, items[i].HeaderValidationErrors = s.revealProviderHeaderConfig(items[i].Headers, items[i].SensitiveHeaders)
		items[i].HeaderValidationErrors = providerHeaderValidationErrorsForType(items[i].Type, items[i].Headers)
		items[i].Headers = maskedProviderHeaders(items[i].Headers, items[i].SensitiveHeaders)
	}
	return items
}

func (s *GormStore) LoadProviderCatalogSnapshot(includeModels bool) ([]ProviderCatalogEntry, string, time.Time, bool, error) {
	var snapshot ProviderCatalogSnapshot
	query := s.db
	if !includeModels {
		query = query.Select("id", "source", "summary_json", "fetched_at")
	}
	if err := query.First(&snapshot, "id = ?", providerCatalogSnapshotID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", time.Time{}, false, nil
		}
		return nil, "", time.Time{}, false, err
	}
	payload := snapshot.SummaryJSON
	if includeModels || strings.TrimSpace(payload) == "" {
		payload = snapshot.CatalogJSON
	}
	var entries []ProviderCatalogEntry
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		return nil, "", time.Time{}, false, fmt.Errorf("decode provider catalog snapshot: %w", err)
	}
	if !includeModels && strings.TrimSpace(snapshot.SummaryJSON) == "" {
		entries = cloneCatalogEntries(entries, false)
	}
	return entries, snapshot.Source, snapshot.FetchedAt, true, nil
}

func (s *GormStore) SaveProviderCatalogSnapshot(entries []ProviderCatalogEntry, source string, fetchedAt time.Time) error {
	if len(entries) == 0 {
		return fmt.Errorf("provider catalog snapshot cannot be empty")
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}
	catalogJSON, err := json.Marshal(cloneCatalogEntries(entries, true))
	if err != nil {
		return fmt.Errorf("encode provider catalog snapshot: %w", err)
	}
	summaryJSON, err := json.Marshal(cloneCatalogEntries(entries, false))
	if err != nil {
		return fmt.Errorf("encode provider catalog summaries: %w", err)
	}
	snapshot := ProviderCatalogSnapshot{
		ID:          providerCatalogSnapshotID,
		Source:      firstNonEmpty(strings.TrimSpace(source), "builtin"),
		SummaryJSON: string(summaryJSON),
		CatalogJSON: string(catalogJSON),
		FetchedAt:   fetchedAt.UTC(),
	}
	return s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&snapshot).Error
}

func (s *GormStore) GetProvider(id string) (Provider, bool) {
	var provider Provider
	if err := s.db.First(&provider, "id = ?", id).Error; err != nil {
		return Provider{}, false
	}
	provider.APIKey = s.decryptSecret(provider.APIKey)
	provider.Headers, provider.HeaderValidationErrors = s.revealProviderHeaderConfig(provider.Headers, provider.SensitiveHeaders)
	provider.HeaderValidationErrors = providerHeaderValidationErrorsForType(provider.Type, provider.Headers)
	return provider, true
}

func (s *GormStore) UpdateProvider(id string, patch Provider) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", id).Error; err != nil {
		return Provider{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if patch.Name != "" {
		provider.Name = patch.Name
	}
	if patch.Type != "" {
		if err := validateProviderAdapterResources(s.db, id, patch.Type); err != nil {
			return Provider{}, err
		}
		provider.Type = patch.Type
	}
	provider.BaseURL = patch.BaseURL
	if patch.ClearAPIKey {
		provider.APIKey = ""
	} else if patch.APIKey != "" {
		if firstNonEmpty(patch.Type, provider.Type) == ProviderOpenAICodex {
			return Provider{}, NewHTTPError(409, "provider_adapter_credential_conflict", "Codex Subscription credentials must be stored on account resources")
		}
		provider.APIKey = s.encryptSecret(patch.APIKey)
	}
	if patch.Status != "" {
		provider.Status = patch.Status
	}
	provider.Healthy = patch.Healthy
	if patch.Priority != 0 {
		provider.Priority = patch.Priority
	}
	if patch.Headers != nil {
		headers, sensitive, err := s.protectProviderHeaders(patch.Headers, patch.SensitiveHeaders, provider.Headers, provider.SensitiveHeaders)
		if err != nil {
			return Provider{}, err
		}
		provider.Headers = headers
		provider.SensitiveHeaders = sensitive
	}
	if patch.Options != nil {
		provider.Options = patch.Options
	}
	var resources []ProviderResource
	if err := s.db.Where("provider_id = ?", provider.ID).Find(&resources).Error; err != nil {
		return Provider{}, err
	}
	providerHeaders := s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders)
	if err := validateEffectiveProviderHeaders(provider.Type, providerHeaders, nil); err != nil {
		return Provider{}, err
	}
	for _, resource := range resources {
		resourceHeaders := s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders)
		if err := validateEffectiveProviderHeaders(provider.Type, providerHeaders, resourceHeaders); err != nil {
			return Provider{}, err
		}
	}
	if err := s.db.Save(&provider).Error; err != nil {
		return Provider{}, err
	}
	provider.APIKey = ""
	provider.Headers = maskedProviderHeaders(s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders), provider.SensitiveHeaders)
	return provider, nil
}

func (s *GormStore) DeleteProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var provider Provider
		if err := tx.First(&provider, "id = ?", id).Error; err != nil {
			return notFound(err, "provider_not_found", "Provider not found")
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ModelRoute{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ProviderModel{}).Error; err != nil {
			return err
		}
		var resourceIDs []string
		if err := tx.Model(&ProviderResource{}).Where("provider_id = ?", id).Pluck("id", &resourceIDs).Error; err != nil {
			return err
		}
		if len(resourceIDs) > 0 {
			if err := tx.Where("scope_type = ? AND scope_id IN ?", "provider_resource", resourceIDs).Delete(&InFlightLease{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id IN ?", resourceIDs).Delete(&ProviderResourceBucket{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id IN ?", resourceIDs).Delete(&ProviderResourceObservation{}).Error; err != nil {
				return err
			}
			if err := tx.Where("resource_id IN ?", resourceIDs).Delete(&ProviderObservation{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("provider_id = ?", id).Delete(&AdapterSessionBinding{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ProviderObservation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("provider_id = ?", id).Delete(&ProviderResource{}).Error; err != nil {
			return err
		}
		return tx.Delete(&provider).Error
	})
}

func (s *GormStore) SetProviderHealth(providerID string, healthy bool) (Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var provider Provider
	if err := s.db.First(&provider, "id = ?", providerID).Error; err != nil {
		return Provider{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if err := s.db.Model(&Provider{}).Where("id = ?", providerID).Update("healthy", healthy).Error; err != nil {
		return Provider{}, err
	}
	provider.Healthy = healthy
	provider.APIKey = ""
	s.maskProviderHeaderConfig(&provider)
	return provider, nil
}

func (s *GormStore) AddProviderResource(resource ProviderResource) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateClaudeCodeAttributionOptions(resource.Options); err != nil {
		return ProviderResource{}, err
	}
	var provider Provider
	if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if err := ensureProviderResourceAdapterCompatibility(s.db, &provider, resource.ResourceType); err != nil {
		return ProviderResource{}, err
	}
	resource.Name = strings.TrimSpace(resource.Name)
	// routeSelection lets a non-empty resource BaseURL override the provider's
	// validated one, so resource-level URLs must pass the same SSRF guard at
	// persistence time (operator allowlist and explicit loopback opt-in included).
	if err := ValidateProviderUpstreamBaseURL(resource.BaseURL); err != nil {
		return ProviderResource{}, err
	}
	now := time.Now().UTC()
	if resource.ID == "" {
		resource.ID = NewID("rsrc")
	}
	if err := s.ensureProviderResourceNameUnique(resource.ID, resource.Name); err != nil {
		return ProviderResource{}, err
	}
	if resource.Status == "" {
		resource.Status = StatusActive
	}
	if resource.ResourceType == "" {
		resource.ResourceType = ProviderResourceAPIKey
	}
	if !resource.Healthy {
		resource.Healthy = true
	}
	if resource.Weight <= 0 {
		resource.Weight = 100
	}
	if resource.CreatedAt.IsZero() {
		resource.CreatedAt = now
	}
	resource.UpdatedAt = now
	headers, sensitive, err := s.protectProviderHeaders(resource.Headers, resource.SensitiveHeaders, nil, nil)
	if err != nil {
		return ProviderResource{}, err
	}
	resource.Headers = headers
	resource.SensitiveHeaders = sensitive
	if err := validateMergedProviderHeaderLimits(
		s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders),
		s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders),
	); err != nil {
		return ProviderResource{}, err
	}
	s.prepareProviderResourceForCreate(&resource)
	resource.APIKey = s.encryptSecret(resource.APIKey)
	if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&resource).Error; err != nil {
		return ProviderResource{}, err
	}
	resource.Headers = maskedProviderHeaders(s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders), resource.SensitiveHeaders)
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) ListProviderResources() []ProviderResource {
	var items []ProviderResource
	_ = s.db.Order("provider_id asc, priority asc, weight desc, created_at asc").Find(&items).Error
	var providers []Provider
	_ = s.db.Find(&providers).Error
	providersByID := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		providersByID[provider.ID] = provider
	}
	var observations []ProviderResourceObservation
	_ = s.db.Find(&observations).Error
	observationByResource := make(map[string]ProviderResourceObservation, len(observations))
	for _, observation := range observations {
		observationByResource[observation.ResourceID] = observation
	}
	for i := range items {
		if observation, ok := observationByResource[items[i].ID]; ok {
			copy := observation
			items[i].Observation = &copy
		}
		redactProviderResourceSecrets(&items[i])
		items[i].Headers, items[i].HeaderValidationErrors = s.revealProviderHeaderConfig(items[i].Headers, items[i].SensitiveHeaders)
		if provider, ok := providersByID[items[i].ProviderID]; ok {
			if err := validateEffectiveProviderHeaders(provider.Type, s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders), items[i].Headers); err != nil {
				items[i].HeaderValidationErrors = []string{AsHTTPError(err).Code}
			}
		}
		items[i].Headers = maskedProviderHeaders(items[i].Headers, items[i].SensitiveHeaders)
	}
	return items
}

func (s *GormStore) GetProviderResource(id string) (ProviderResource, bool) {
	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, false
	}
	resource.APIKey = s.decryptSecret(resource.APIKey)
	resource.Headers, resource.HeaderValidationErrors = s.revealProviderHeaderConfig(resource.Headers, resource.SensitiveHeaders)
	var provider Provider
	if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err == nil {
		if validationErr := validateEffectiveProviderHeaders(provider.Type, s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders), resource.Headers); validationErr != nil {
			resource.HeaderValidationErrors = []string{AsHTTPError(validationErr).Code}
		}
	}
	return resource, true
}

func providerResourceMutationLeaseName(resourceID string) string {
	return "provider-resource-mutation:" + strings.TrimSpace(resourceID)
}

func updateExistingProviderResourceColumns(db *gorm.DB, resource *ProviderResource, columns ...string) error {
	result := db.Model(&ProviderResource{}).
		Where("id = ?", resource.ID).
		Select(columns).
		Updates(resource)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	return nil
}

func (s *GormStore) UpdateProviderResource(id string, patch ProviderResource) (ProviderResource, error) {
	var updated ProviderResource
	err := s.withClusterLease(context.Background(), providerResourceMutationLeaseName(id), func(leaseCtx context.Context) error {
		var updateErr error
		updated, updateErr = s.updateProviderResource(leaseCtx, id, patch)
		return updateErr
	})
	return updated, err
}

func (s *GormStore) updateProviderResource(ctx context.Context, id string, patch ProviderResource) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.db.WithContext(ctx)

	if err := validateClaudeCodeAttributionOptions(patch.Options); err != nil {
		return ProviderResource{}, err
	}
	var resource ProviderResource
	if err := db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	before := resource
	beforeCredentials := s.providerResourceCredentialsForRuntime(before)
	beforeImageCapability := strings.TrimSpace(before.Options[codexImageCapabilityOption])
	if patch.ProviderID != "" && patch.ProviderID != resource.ProviderID {
		if err := db.First(&Provider{}, "id = ?", patch.ProviderID).Error; err != nil {
			return ProviderResource{}, notFound(err, "provider_not_found", "Provider not found")
		}
		resource.ProviderID = patch.ProviderID
	}
	if patch.Name != "" {
		nextName := strings.TrimSpace(patch.Name)
		if err := s.ensureProviderResourceNameUnique(resource.ID, nextName); err != nil {
			return ProviderResource{}, err
		}
		resource.Name = nextName
	}
	if patch.Group != "" {
		resource.Group = patch.Group
	}
	if patch.ResourceType != "" {
		resource.ResourceType = patch.ResourceType
	}
	resource.BaseURL = patch.BaseURL
	// Same SSRF persistence guard as AddProviderResource: an empty value
	// clears the override (the provider URL applies again), a non-empty one
	// must be a routable upstream.
	if err := ValidateProviderUpstreamBaseURL(resource.BaseURL); err != nil {
		return ProviderResource{}, err
	}
	shouldEncryptAPIKey := false
	if patch.APIKey != "" {
		resource.APIKey = patch.APIKey
		shouldEncryptAPIKey = true
	}
	resource.Region = patch.Region
	resource.Environment = patch.Environment
	if patch.Status != "" {
		resource.Status = patch.Status
	}
	resource.Healthy = patch.Healthy
	if patch.Priority != 0 {
		resource.Priority = patch.Priority
	}
	if patch.Weight != 0 {
		resource.Weight = patch.Weight
	}
	resource.RateLimitRPM = patch.RateLimitRPM
	resource.TokenLimitTPM = patch.TokenLimitTPM
	resource.MaxConcurrency = patch.MaxConcurrency
	if patch.Headers != nil {
		headers, sensitive, err := s.protectProviderHeaders(patch.Headers, patch.SensitiveHeaders, resource.Headers, resource.SensitiveHeaders)
		if err != nil {
			return ProviderResource{}, err
		}
		resource.Headers = headers
		resource.SensitiveHeaders = sensitive
	}
	if patch.Options != nil {
		if isOpenAIAccountResource(resource.ResourceType) {
			resource.Options = preserveOpenAIAccountProtectedOptions(resource.Options, patch)
		} else {
			resource.Options = patch.Options
		}
	}
	var provider Provider
	if err := db.First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_not_found", "Provider not found")
	}
	if err := ensureProviderResourceAdapterCompatibility(db, &provider, resource.ResourceType); err != nil {
		return ProviderResource{}, err
	}
	if err := validateEffectiveProviderHeaders(
		provider.Type,
		s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders),
		s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders),
	); err != nil {
		return ProviderResource{}, err
	}
	resource.UpdatedAt = time.Now().UTC()
	s.prepareProviderResourceForUpdate(&resource, patch)
	imageBindingChanged := isOpenAIAccountResource(before.ResourceType) && openAIAccountImageBindingChanged(
		before,
		beforeCredentials,
		resource,
		s.providerResourceCredentialsForRuntime(resource),
	)
	if imageBindingChanged {
		delete(resource.Options, codexImageCapabilityOption)
		delete(resource.Options, codexImageCapabilityCheckedAtOption)
		delete(resource.Options, codexImageRouteBackfillOption)
	}
	if patch.Credentials != nil && strings.TrimSpace(patch.Credentials.AccessToken) != "" {
		shouldEncryptAPIKey = true
	}
	if isOpenAIAccountResource(resource.ResourceType) && strings.TrimSpace(resource.APIKey) != "" && !strings.HasPrefix(resource.APIKey, "enc:v1:") {
		shouldEncryptAPIKey = true
	}
	if shouldEncryptAPIKey {
		resource.APIKey = s.encryptSecret(resource.APIKey)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := updateExistingProviderResourceColumns(tx, &resource,
			"provider_id", "name", "group", "resource_type", "base_url", "api_key", "region", "environment",
			"status", "healthy", "priority", "weight", "rate_limit_rpm", "token_limit_tpm", "max_concurrency",
			"headers", "sensitive_headers", "options", "credential_blob", "updated_at",
		); err != nil {
			return err
		}
		if !imageBindingChanged || beforeImageCapability != codexImageCapabilitySupported {
			return nil
		}
		var resources []ProviderResource
		if err := tx.Where("provider_id = ?", before.ProviderID).Find(&resources).Error; err != nil {
			return err
		}
		var routes []ModelRoute
		if err := tx.
			Where("provider_id = ? AND model_name = ? AND provider_model = ? AND status = ?", before.ProviderID, codexImageModelName, codexImageUpstreamModel, StatusActive).
			Find(&routes).Error; err != nil {
			return err
		}
		for _, route := range routes {
			if codexImageRouteHasSupportedResource(route, resources) {
				continue
			}
			if err := tx.Model(&ModelRoute{}).Where("id = ?", route.ID).Update("status", StatusDisabled).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return ProviderResource{}, err
	}
	resource.Headers = maskedProviderHeaders(s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders), resource.SensitiveHeaders)
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) ensureProviderResourceNameUnique(resourceID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_provider_resource", "Provider resource name is required")
	}
	var count int64
	err := s.db.Model(&ProviderResource{}).
		Where("LOWER(TRIM(name)) = ?", strings.ToLower(name)).
		Where("id <> ?", resourceID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return NewHTTPError(http.StatusConflict, "provider_resource_name_conflict", "Provider resource name already exists")
	}
	return nil
}

func (s *GormStore) UpdateProviderResourceOptions(id string, options map[string]string) (ProviderResource, error) {
	var updated ProviderResource
	err := s.withClusterLease(context.Background(), providerResourceMutationLeaseName(id), func(leaseCtx context.Context) error {
		var updateErr error
		updated, updateErr = s.updateProviderResourceOptions(leaseCtx, id, options)
		return updateErr
	})
	return updated, err
}

func (s *GormStore) updateProviderResourceOptions(ctx context.Context, id string, options map[string]string) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.db.WithContext(ctx)

	var resource ProviderResource
	if err := db.First(&resource, "id = ?", id).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	if resource.Options == nil {
		resource.Options = map[string]string{}
	}
	for key, value := range options {
		resource.Options[key] = value
	}
	resource.UpdatedAt = time.Now().UTC()
	if err := updateExistingProviderResourceColumns(db, &resource, "options", "updated_at"); err != nil {
		return ProviderResource{}, err
	}
	s.maskProviderResourceHeaderConfig(&resource)
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) DeleteProviderResource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Transaction(func(tx *gorm.DB) error {
		var resource ProviderResource
		if err := tx.First(&resource, "id = ?", id).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		if err := tx.Model(&ModelRoute{}).
			Where("provider_resource_id = ?", id).
			Update("provider_resource_id", "").Error; err != nil {
			return err
		}
		if err := tx.Where("scope_type = ? AND scope_id = ?", "provider_resource", id).Delete(&InFlightLease{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&ProviderResourceBucket{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&ProviderResourceObservation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&ProviderObservation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&AdapterSessionBinding{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&resource).Error; err != nil {
			return err
		}
		if resource.ResourceType != ProviderResourceOpenAISubscription {
			return nil
		}
		var remainingAccounts int64
		if err := tx.Model(&ProviderResource{}).
			Where("provider_id = ? AND resource_type = ?", resource.ProviderID, ProviderResourceOpenAISubscription).
			Count(&remainingAccounts).Error; err != nil {
			return err
		}
		if remainingAccounts > 0 {
			return nil
		}
		return tx.Model(&ModelRoute{}).
			Where("provider_id = ? AND model_name = ? AND provider_model = ?", resource.ProviderID, codexImageModelName, codexImageUpstreamModel).
			Update("status", StatusDisabled).Error
	})
}

func (s *GormStore) SetProviderResourceHealth(resourceID string, healthy bool) (ProviderResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var resource ProviderResource
	if err := s.db.First(&resource, "id = ?", resourceID).Error; err != nil {
		return ProviderResource{}, notFound(err, "provider_resource_not_found", "Provider resource not found")
	}
	now := time.Now().UTC()
	if err := s.db.Model(&ProviderResource{}).
		Where("id = ?", resourceID).
		Updates(map[string]any{"healthy": healthy, "last_checked_at": now, "updated_at": now}).Error; err != nil {
		return ProviderResource{}, err
	}
	resource.Healthy = healthy
	resource.LastCheckedAt = &now
	resource.UpdatedAt = now
	s.maskProviderResourceHeaderConfig(&resource)
	redactProviderResourceSecrets(&resource)
	return resource, nil
}

func (s *GormStore) BulkOperateProviderResources(action string, ids []string) (ProviderResourceBulkResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	action = strings.TrimSpace(action)
	if !validProviderResourceBulkAction(action) {
		return ProviderResourceBulkResult{}, NewHTTPError(400, "invalid_provider_resource_action", "Invalid provider resource bulk action")
	}
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return ProviderResourceBulkResult{}, NewHTTPError(400, "missing_provider_resource_ids", "Provider resource ids are required")
	}
	now := time.Now().UTC()
	result := ProviderResourceBulkResult{Action: action, Resources: make([]ProviderResource, 0, len(ids))}
	for _, id := range ids {
		var resource ProviderResource
		if err := s.db.First(&resource, "id = ?", id).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, id+": "+notFound(err, "provider_resource_not_found", "Provider resource not found").Error())
			continue
		}
		updates := map[string]any{"updated_at": now}
		switch action {
		case "enable":
			updates["status"] = StatusActive
			updates["healthy"] = true
			updates["failure_count"] = 0
			updates["cooldown_until"] = nil
			updates["last_checked_at"] = now
			resource.Status = StatusActive
			resource.Healthy = true
			resource.FailureCount = 0
			resource.CooldownUntil = nil
			resource.LastCheckedAt = &now
		case "disable":
			updates["status"] = StatusDisabled
			updates["healthy"] = false
			updates["cooldown_until"] = nil
			updates["last_checked_at"] = now
			resource.Status = StatusDisabled
			resource.Healthy = false
			resource.CooldownUntil = nil
			resource.LastCheckedAt = &now
		case "test":
			healthy := resource.Status == StatusActive
			updates["healthy"] = healthy
			updates["last_checked_at"] = now
			resource.Healthy = healthy
			resource.LastCheckedAt = &now
			if healthy {
				updates["failure_count"] = 0
				updates["cooldown_until"] = nil
				resource.FailureCount = 0
				resource.CooldownUntil = nil
			}
		case "clear_error":
			updates["healthy"] = true
			updates["failure_count"] = 0
			updates["cooldown_until"] = nil
			updates["last_checked_at"] = now
			resource.Healthy = true
			resource.FailureCount = 0
			resource.CooldownUntil = nil
			resource.LastCheckedAt = &now
		case "reset_usage":
			if err := s.db.Where("resource_id = ?", resource.ID).Delete(&ProviderResourceBucket{}).Error; err != nil {
				result.Failed++
				result.Errors = append(result.Errors, id+": "+err.Error())
				continue
			}
		}
		if err := s.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Updates(updates).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, id+": "+err.Error())
			continue
		}
		resource.UpdatedAt = now
		s.maskProviderResourceHeaderConfig(&resource)
		redactProviderResourceSecrets(&resource)
		result.Success++
		result.Resources = append(result.Resources, resource)
	}
	return result, nil
}

func (s *GormStore) ImportProviderResources(resources []ProviderResource) (ProviderResourceImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(resources) == 0 {
		return ProviderResourceImportResult{}, NewHTTPError(400, "missing_provider_resources", "Provider resources are required")
	}
	if len(resources) > 200 {
		return ProviderResourceImportResult{}, NewHTTPError(400, "too_many_provider_resources", "Provider resource import is limited to 200 rows")
	}
	result := ProviderResourceImportResult{Resources: make([]ProviderResource, 0, len(resources))}
	for index, resource := range resources {
		row := strconv.Itoa(index + 1)
		resource.ProviderID = strings.TrimSpace(resource.ProviderID)
		resource.Name = strings.TrimSpace(resource.Name)
		if resource.ProviderID == "" || resource.Name == "" {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": provider_id and name are required")
			continue
		}
		var provider Provider
		if err := s.db.First(&provider, "id = ?", resource.ProviderID).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+notFound(err, "provider_not_found", "Provider not found").Error())
			continue
		}
		if err := validateProviderHeaderSupport(provider.Type, resource.Headers); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		// Same SSRF persistence guard as AddProviderResource: a rejected row
		// fails the row, not the whole import, matching the per-row contract.
		if err := ValidateProviderUpstreamBaseURL(resource.BaseURL); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		now := time.Now().UTC()
		if resource.ID == "" {
			resource.ID = NewID("rsrc")
		}
		if err := s.ensureProviderResourceNameUnique(resource.ID, resource.Name); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		if resource.Status == "" {
			resource.Status = StatusActive
		}
		if resource.ResourceType == "" {
			resource.ResourceType = ProviderResourceAPIKey
		}
		if !resource.Healthy {
			resource.Healthy = true
		}
		if resource.Weight <= 0 {
			resource.Weight = 100
		}
		if resource.CreatedAt.IsZero() {
			resource.CreatedAt = now
		}
		resource.UpdatedAt = now
		headers, sensitive, err := s.protectProviderHeaders(resource.Headers, resource.SensitiveHeaders, nil, nil)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		resource.Headers = headers
		resource.SensitiveHeaders = sensitive
		if err := validateEffectiveProviderHeaders(
			provider.Type,
			s.revealProviderHeaders(provider.Headers, provider.SensitiveHeaders),
			s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders),
		); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		s.prepareProviderResourceForCreate(&resource)
		resource.APIKey = s.encryptSecret(resource.APIKey)
		if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&resource).Error; err != nil {
			result.Failed++
			result.Errors = append(result.Errors, "row "+row+": "+err.Error())
			continue
		}
		resource.Headers = maskedProviderHeaders(s.revealProviderHeaders(resource.Headers, resource.SensitiveHeaders), resource.SensitiveHeaders)
		redactProviderResourceSecrets(&resource)
		result.Success++
		result.Resources = append(result.Resources, resource)
	}
	return result, nil
}

func (s *GormStore) lockScopeForUpdate(tx *gorm.DB, scopeType string, scopeID string) error {
	if s.dbDriver != "postgres" {
		return nil
	}
	key := "tokenhub:" + scopeType + ":" + scopeID
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error
}

func (s *GormStore) lockScopeForSharedRead(tx *gorm.DB, scopeType string, scopeID string) error {
	if s.dbDriver != "postgres" {
		return nil
	}
	key := "tokenhub:" + scopeType + ":" + scopeID
	return tx.Exec("SELECT pg_advisory_xact_lock_shared(hashtextextended(?, 0))", key).Error
}

func (s *GormStore) acquireInFlightLease(tx *gorm.DB, scopeType string, scopeID string, limit int64, leaseID string) (time.Duration, error) {
	if limit <= 0 {
		return 0, nil
	}
	if err := s.lockScopeForUpdate(tx, scopeType, scopeID); err != nil {
		return 0, err
	}
	now, err := s.databaseNow(tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Where("scope_type = ? AND scope_id = ? AND expires_at <= ?", scopeType, scopeID, now).
		Delete(&InFlightLease{}).Error; err != nil {
		return 0, err
	}
	var count int64
	if err := tx.Model(&InFlightLease{}).
		Where("scope_type = ? AND scope_id = ? AND expires_at > ?", scopeType, scopeID, now).
		Count(&count).Error; err != nil {
		return 0, err
	}
	if count >= limit {
		return 0, ErrRateLimitExceeded
	}
	ttl := effectiveLeaseTTL(s.inFlightLeaseTTL, 300*time.Second)
	expiresAt := now.Add(ttl)
	lease := InFlightLease{
		ID:        leaseID,
		ScopeType: scopeType,
		ScopeID:   scopeID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := tx.Create(&lease).Error; err != nil {
		return 0, err
	}
	var persisted InFlightLease
	if err := tx.Select("expires_at").First(&persisted, "id = ?", leaseID).Error; err != nil {
		return 0, err
	}
	return s.persistedLeaseConfirmation(tx, persisted.ExpiresAt)
}

func (s *GormStore) renewInFlightLease(ctx context.Context, leaseID string, ttl time.Duration) (time.Duration, bool, error) {
	var confirmedFor time.Duration
	var retained bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		result := tx.Model(&InFlightLease{}).Where("id = ?", leaseID).
			Updates(map[string]any{"expires_at": now.Add(ttl), "updated_at": now})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		var persisted InFlightLease
		if err := tx.Select("expires_at").First(&persisted, "id = ?", leaseID).Error; err != nil {
			return err
		}
		confirmedFor, err = s.persistedLeaseConfirmation(tx, persisted.ExpiresAt)
		if err != nil {
			return err
		}
		retained = confirmedFor > 0
		return nil
	})
	return confirmedFor, retained, err
}

func (s *GormStore) startInFlightLeaseHeartbeat(parent context.Context, leaseID string, confirmedFor time.Duration) context.Context {
	if strings.TrimSpace(leaseID) == "" {
		return parent
	}
	ttl := effectiveLeaseTTL(s.inFlightLeaseTTL, 300*time.Second)
	heartbeat := startLeaseHeartbeat(parent, ttl, confirmedFor, func(attemptCtx context.Context) (time.Duration, bool, error) {
		return s.renewInFlightLease(attemptCtx, leaseID, ttl)
	})
	if previous, loaded := s.leaseHeartbeats.LoadOrStore(leaseID, heartbeat); loaded {
		if previousHeartbeat, ok := previous.(*leaseHeartbeat); ok {
			_ = stopLeaseHeartbeat(previousHeartbeat)
		}
		s.leaseHeartbeats.Store(leaseID, heartbeat)
	}
	return heartbeat.ctx
}

func (s *GormStore) stopInFlightLeaseHeartbeat(leaseID string) error {
	if value, ok := s.leaseHeartbeats.LoadAndDelete(leaseID); ok {
		if heartbeat, ok := value.(*leaseHeartbeat); ok {
			return stopLeaseHeartbeat(heartbeat)
		}
	}
	return nil
}

// ReleaseProviderResourceCapacity releases concurrency bookkeeping without
// treating a local coordination failure as an upstream provider failure.
func (s *GormStore) ReleaseProviderResourceCapacity(resourceID string, leaseID string) {
	_ = s.stopInFlightLeaseHeartbeat(leaseID)
	if strings.TrimSpace(leaseID) == "" {
		return
	}
	if err := s.db.Delete(&InFlightLease{}, "id = ?", leaseID).Error; err != nil {
		log.Printf("[tokenhub] failed to release provider concurrency lease resource=%s lease=%s: %v", resourceID, leaseID, err)
	}
}

func (s *GormStore) providerResourceBucketForUpdate(tx *gorm.DB, resourceID string, bucket string) (ProviderResourceBucket, error) {
	seed := ProviderResourceBucket{ResourceID: resourceID, Bucket: bucket, UpdatedAt: time.Now().UTC()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return ProviderResourceBucket{}, err
	}
	query := tx
	if s.dbDriver == "postgres" {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var item ProviderResourceBucket
	if err := query.First(&item, "resource_id = ? AND bucket = ?", resourceID, bucket).Error; err != nil {
		return ProviderResourceBucket{}, err
	}
	return item, nil
}

func (s *GormStore) consumeProviderResourceRequestCapacity(tx *gorm.DB, resource ProviderResource, now time.Time) error {
	if resource.RateLimitRPM <= 0 && resource.TokenLimitTPM <= 0 {
		return nil
	}
	bucket, err := s.providerResourceBucketForUpdate(tx, resource.ID, minuteBucket(now))
	if err != nil {
		return err
	}
	if resource.RateLimitRPM > 0 && bucket.Requests >= resource.RateLimitRPM {
		return NewHTTPError(http.StatusTooManyRequests, "provider_resource_rpm_exceeded", "Provider resource RPM limit exceeded")
	}
	if resource.TokenLimitTPM > 0 && bucket.Tokens >= resource.TokenLimitTPM {
		return NewHTTPError(http.StatusTooManyRequests, "provider_resource_tpm_exceeded", "Provider resource TPM limit exceeded")
	}
	bucket.Requests++
	bucket.UpdatedAt = now
	return tx.Save(&bucket).Error
}

func (s *GormStore) CheckProviderResourceCapacity(ctx context.Context, resourceID string) (string, context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if resourceID == "" {
		return "", ctx, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	leaseID := NewID("lease")
	acquiredLease := false
	halfOpenClaimed := false
	var leaseConfirmedFor time.Duration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_resource", resourceID); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var resource ProviderResource
		if err := query.First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if !resource.Healthy {
			// Half-open admission. The resource is parked; the trial is claimed by
			// pushing cooldown_until into the future, which both rejects every
			// concurrent request below and pre-arms the next window if this trial
			// fails. The UPDATE is guarded by the deadline it read, so across
			// replicas exactly one caller can win.
			if resource.CooldownUntil == nil || now.Before(*resource.CooldownUntil) {
				return NewHTTPError(http.StatusTooManyRequests, "provider_resource_cooling_down", "Provider resource is cooling down")
			}
			nextDeadline := now.Add(s.cooldownWindow(resource.FailureCount))
			claim := tx.Model(&ProviderResource{}).
				Where("id = ? AND healthy = ? AND cooldown_until = ?", resourceID, false, resource.CooldownUntil).
				Updates(map[string]any{"cooldown_until": &nextDeadline, "updated_at": now})
			if claim.Error != nil {
				return claim.Error
			}
			if claim.RowsAffected == 0 {
				return NewHTTPError(http.StatusTooManyRequests, "provider_resource_cooling_down", "Provider resource is cooling down")
			}
			resource.CooldownUntil = &nextDeadline
			halfOpenClaimed = true
		} else if resource.CooldownUntil != nil && now.Before(*resource.CooldownUntil) {
			return NewHTTPError(http.StatusTooManyRequests, "provider_resource_cooling_down", "Provider resource is cooling down")
		}
		if resource.MaxConcurrency > 0 {
			confirmedFor, err := s.acquireInFlightLease(tx, "provider_resource", resource.ID, resource.MaxConcurrency, leaseID)
			if err != nil {
				if errors.Is(err, ErrRateLimitExceeded) {
					return NewHTTPError(http.StatusTooManyRequests, "provider_resource_concurrency_exceeded", "Provider resource concurrency limit exceeded")
				}
				return err
			}
			leaseConfirmedFor = confirmedFor
			acquiredLease = true
		}
		if err := s.consumeProviderResourceRequestCapacity(tx, resource, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", ctx, err
	}
	// Tag the call context before deriving the lease context, so the marker reaches
	// FinishProviderResourceAttempt on both the leased and the unleased path.
	if halfOpenClaimed {
		ctx = withHalfOpenClaim(ctx)
	}
	if acquiredLease {
		leaseCtx := s.startInFlightLeaseHeartbeat(ctx, leaseID, leaseConfirmedFor)
		return leaseID, leaseCtx, nil
	}
	return "", ctx, nil
}

// CheckProviderResourceRetryCapacity accounts for another physical upstream
// request while retaining the concurrency lease acquired for the logical call.
func (s *GormStore) CheckProviderResourceRetryCapacity(ctx context.Context, resourceID string, leaseID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if resourceID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_resource", resourceID); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var resource ProviderResource
		if err := query.First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		now, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(leaseID) != "" {
			var count int64
			if err := tx.Model(&InFlightLease{}).
				Where("id = ? AND scope_type = ? AND scope_id = ? AND expires_at > ?", leaseID, "provider_resource", resourceID, now).
				Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrCoordinationLeaseLost
			}
		}
		return s.consumeProviderResourceRequestCapacity(tx, resource, now)
	})
}

func (s *GormStore) FinishProviderResourceAttempt(ctx context.Context, resourceID string, leaseID string, outcome AttemptOutcome, usage Usage) {
	if resourceID == "" {
		return
	}
	success := outcome.CountsAsHealthy()
	// Only the request that won the half-open trial may close the breaker.
	closesBreaker := outcome == AttemptSucceeded && hasHalfOpenClaim(ctx)
	_ = s.stopInFlightLeaseHeartbeat(leaseID)
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "provider_resource", resourceID); err != nil {
			return err
		}
		if strings.TrimSpace(leaseID) != "" {
			if err := tx.Delete(&InFlightLease{}, "id = ?", leaseID).Error; err != nil {
				return err
			}
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var resource ProviderResource
		if err := query.First(&resource, "id = ?", resourceID).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		updates := map[string]any{"updated_at": now}
		if success {
			if usage.TotalTokens == 0 {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			if usage.TotalTokens > 0 {
				bucket, err := s.providerResourceBucketForUpdate(tx, resourceID, minuteBucket(now))
				if err != nil {
					return err
				}
				bucket.Tokens += usage.TotalTokens
				bucket.UpdatedAt = now
				if err := tx.Save(&bucket).Error; err != nil {
					return err
				}
			}
			switch {
			case outcome != AttemptSucceeded:
				// A neutral outcome (client disconnect, policy refusal, unsupported
				// model) is not the resource's fault, so it must not add a failure —
				// but it is not evidence the upstream works either. Leave the failure
				// count, health and cooldown exactly as they were: clearing the count
				// here would let an alternating failure/disconnect pattern keep the
				// breaker from ever tripping, and would reset the backoff of a
				// half-open trial whose client merely hung up.
			case resource.Healthy:
				// Ordinary success on a live resource: reset the consecutive failure
				// run and drop a stale deadline left behind by an earlier trip.
				updates["failure_count"] = 0
				if resource.CooldownUntil != nil && now.After(*resource.CooldownUntil) {
					updates["cooldown_until"] = nil
				}
			case closesBreaker && resource.Status == StatusActive:
				// The half-open claimant confirmed the upstream: close the breaker.
				updates["failure_count"] = 0
				updates["healthy"] = true
				updates["cooldown_until"] = nil
				updates["last_checked_at"] = now
				if err := tx.Create(&AlertEvent{
					ID:         NewID("alt"),
					ScopeType:  "provider_resource",
					ScopeID:    resource.ID,
					Severity:   "info",
					Code:       "provider_resource_recovered",
					Message:    "Provider resource recovered after a successful half-open request",
					ResourceID: resource.ProviderID,
					CreatedAt:  now,
				}).Error; err != nil {
					return err
				}
			default:
				// The breaker is open and this success came from a request that never
				// held the trial permit — typically one already in flight when the
				// breaker tripped. It proves nothing about the state being probed, so
				// it must not touch the failure count, the deadline, or health.
			}
		} else {
			nextFailures := s.nextFailureCount(resource.FailureCount)
			updates["failure_count"] = nextFailures
			if nextFailures >= s.failureThreshold {
				cooldownUntil := now.Add(s.cooldownWindow(nextFailures))
				updates["healthy"] = false
				updates["cooldown_until"] = &cooldownUntil
				updates["last_checked_at"] = now
				if err := tx.Create(&AlertEvent{
					ID:         NewID("alt"),
					ScopeType:  "provider_resource",
					ScopeID:    resource.ID,
					Severity:   "warning",
					Code:       "provider_resource_cooling_down",
					Message:    "Provider resource entered cooldown after repeated failures",
					ResourceID: resource.ProviderID,
					CreatedAt:  now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&ProviderResource{}).Where("id = ?", resourceID).Updates(updates).Error
	})
	if err != nil {
		log.Printf("[tokenhub] failed to finish provider resource attempt resource=%s: %v", resourceID, err)
		if strings.TrimSpace(leaseID) != "" {
			if releaseErr := s.db.Delete(&InFlightLease{}, "id = ?", leaseID).Error; releaseErr != nil {
				log.Printf("[tokenhub] failed to release provider concurrency lease resource=%s lease=%s: %v", resourceID, leaseID, releaseErr)
			}
		}
	}
}
