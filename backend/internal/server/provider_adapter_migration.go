package server

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) NormalizeProviderAdapterTypes(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var providers []Provider
		if err := tx.Where("type = ?", ProviderOpenAI).Find(&providers).Error; err != nil {
			return err
		}
		for _, provider := range providers {
			var resources []ProviderResource
			if err := tx.Where("provider_id = ?", provider.ID).Find(&resources).Error; err != nil {
				return err
			}
			subscriptions := make([]ProviderResource, 0, len(resources))
			hasDirectResource := false
			for _, resource := range resources {
				if isOpenAIAccountResource(resource.ResourceType) {
					subscriptions = append(subscriptions, resource)
				} else {
					hasDirectResource = true
				}
			}
			if len(subscriptions) == 0 {
				continue
			}
			if !hasDirectResource && strings.TrimSpace(provider.APIKey) == "" {
				updates := map[string]any{"type": ProviderOpenAICodex}
				if codexProviderBaseURLNeedsNormalization(provider.BaseURL) {
					updates["base_url"] = openAICodexBaseURL
				}
				if err := tx.Model(&Provider{}).Where("id = ?", provider.ID).Updates(updates).Error; err != nil {
					return err
				}
				continue
			}
			if err := splitMixedOpenAIProvider(tx, provider, subscriptions); err != nil {
				return err
			}
		}
		return nil
	})
}

func splitMixedOpenAIProvider(tx *gorm.DB, provider Provider, subscriptions []ProviderResource) error {
	codexProvider := Provider{
		ID:        NewID("prv"),
		Name:      strings.TrimSpace(provider.Name) + " Codex",
		Type:      ProviderOpenAICodex,
		BaseURL:   openAICodexBaseURL,
		Status:    provider.Status,
		Healthy:   provider.Healthy,
		Priority:  provider.Priority,
		Options:   cloneStringMap(provider.Options),
		CreatedAt: provider.CreatedAt,
	}
	if err := tx.Create(&codexProvider).Error; err != nil {
		return err
	}
	resourceIDs := make([]string, 0, len(subscriptions))
	for _, resource := range subscriptions {
		resourceIDs = append(resourceIDs, resource.ID)
	}
	if err := tx.Model(&ProviderResource{}).Where("id IN ?", resourceIDs).Update("provider_id", codexProvider.ID).Error; err != nil {
		return err
	}
	if err := tx.Model(&ModelRoute{}).Where("provider_id = ? AND provider_resource_id IN ?", provider.ID, resourceIDs).
		Update("provider_id", codexProvider.ID).Error; err != nil {
		return err
	}

	var genericRoutes []ModelRoute
	if err := tx.Where("provider_id = ? AND (provider_resource_id = '' OR provider_resource_id IS NULL)", provider.ID).Find(&genericRoutes).Error; err != nil {
		return err
	}
	for _, route := range genericRoutes {
		var model Model
		if err := tx.Where("name = ?", route.ModelName).First(&model).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			return err
		}
		if !strings.EqualFold(model.Category, "codex") && !strings.EqualFold(model.Family, "codex") {
			continue
		}
		route.ID = NewID("rte")
		route.ProviderID = codexProvider.ID
		route.CreatedAt = route.CreatedAt.UTC()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&route).Error; err != nil {
			return err
		}
	}
	return nil
}

func ensureProviderResourceAdapterCompatibility(tx *gorm.DB, provider *Provider, resourceType string) error {
	if provider == nil {
		return NewHTTPError(404, "provider_not_found", "Provider not found")
	}
	if isOpenAIAccountResource(resourceType) {
		switch provider.Type {
		case ProviderOpenAICodex:
			return nil
		case ProviderOpenAI:
			var directResources int64
			if err := tx.Model(&ProviderResource{}).
				Where("provider_id = ? AND resource_type NOT IN ?", provider.ID, []string{ProviderResourceOpenAISubscription, "openai_oauth", "oauth"}).
				Count(&directResources).Error; err != nil {
				return err
			}
			if directResources > 0 || strings.TrimSpace(provider.APIKey) != "" {
				return NewHTTPError(409, "provider_adapter_resource_conflict", "OpenAI API credentials and Codex Subscription accounts must use separate Providers")
			}
			updates := map[string]any{"type": ProviderOpenAICodex}
			if codexProviderBaseURLNeedsNormalization(provider.BaseURL) {
				updates["base_url"] = openAICodexBaseURL
				provider.BaseURL = openAICodexBaseURL
			}
			if err := tx.Model(&Provider{}).Where("id = ?", provider.ID).Updates(updates).Error; err != nil {
				return err
			}
			provider.Type = ProviderOpenAICodex
			return nil
		default:
			// Custom adapters remain responsible for their own resource schema.
			return nil
		}
	}
	if provider.Type == ProviderOpenAICodex {
		return NewHTTPError(409, "provider_adapter_resource_conflict", "Codex Subscription Providers only accept subscription account resources")
	}
	return nil
}

func codexProviderBaseURLNeedsNormalization(baseURL string) bool {
	baseURL = strings.TrimSpace(strings.ToLower(baseURL))
	return baseURL == "" || strings.Contains(baseURL, "api.openai.com")
}

func validateProviderAdapterResources(tx *gorm.DB, providerID string, adapterType string) error {
	var resources []ProviderResource
	if err := tx.Where("provider_id = ?", providerID).Find(&resources).Error; err != nil {
		return err
	}
	for _, resource := range resources {
		switch adapterType {
		case ProviderOpenAICodex:
			if !isOpenAIAccountResource(resource.ResourceType) {
				return NewHTTPError(409, "provider_adapter_resource_conflict", "Codex Subscription Providers only accept subscription account resources")
			}
		case ProviderOpenAI:
			if isOpenAIAccountResource(resource.ResourceType) {
				return NewHTTPError(409, "provider_adapter_resource_conflict", "Codex Subscription accounts must use a Codex Provider")
			}
		}
	}
	return nil
}
