package server

import (
	"strings"
	"time"
)

func (s *GormStore) codexImageAllowedByPolicyLocked(project Project, key APIKey, policy *ScopedRoutingPolicy) bool {
	return s.providerImageCapabilityAllowedByPolicyLocked(project, key, policy, codexImageCapabilityRouteProfile())
}

func (s *GormStore) providerImageCapabilityAllowedByPolicyLocked(project Project, key APIKey, policy *ScopedRoutingPolicy, profile providerImageCapabilityRouteProfile) bool {
	profile.withDefaults()
	var routes []ModelRoute
	if err := s.db.Where("model_name = ? AND provider_model = ? AND status = ?", profile.PublicModel, profile.UpstreamModel, StatusActive).Find(&routes).Error; err != nil {
		return false
	}
	for _, configuredRoute := range routes {
		var provider Provider
		providerQuery := s.db.Where("id = ? AND status = ? AND healthy = ?", configuredRoute.ProviderID, StatusActive, true)
		if profile.ProviderType != "" {
			providerQuery = providerQuery.Where("type = ?", profile.ProviderType)
		}
		if err := providerQuery.First(&provider).Error; err != nil {
			continue
		}
		var resources []ProviderResource
		query := s.db.Where("provider_id = ? AND status = ? AND healthy = ?", provider.ID, StatusActive, true)
		if profile.ResourceType != "" {
			query = query.Where("resource_type = ?", profile.ResourceType)
		}
		if configuredRoute.ProviderResourceID != "" {
			query = query.Where("id = ?", configuredRoute.ProviderResourceID)
		} else if strings.TrimSpace(configuredRoute.ResourceGroup) != "" {
			query = query.Where("\"group\" = ?", strings.TrimSpace(configuredRoute.ResourceGroup))
		}
		if err := query.Find(&resources).Error; err != nil {
			continue
		}
		for index := range resources {
			resource := &resources[index]
			if !s.providerImageCapabilityResourceAvailable(*resource, profile) {
				continue
			}
			resourceRoute := configuredRoute
			resourceRoute.ProviderResourceID = resource.ID
			route := RouteSelection{
				Provider: provider, Resource: resource, ProviderModel: profile.UpstreamModel,
				Route: resourceRoute,
			}
			call := CallContext{Project: project, Key: key, Model: Model{Name: profile.PublicModel}}
			if len(routingPolicyCandidateReasons(call, route, policy)) == 0 {
				return true
			}
		}
	}
	return false
}

func (s *GormStore) codexImageResourceAvailable(resource ProviderResource) bool {
	return s.providerImageCapabilityResourceAvailable(resource, codexImageCapabilityRouteProfile())
}

func (s *GormStore) providerImageCapabilityResourceAvailable(resource ProviderResource, profile providerImageCapabilityRouteProfile) bool {
	profile.withDefaults()
	capability := strings.TrimSpace(resource.Options[profile.CapabilityOption])
	switch {
	case profile.capabilityIsSupported(capability):
		return true
	case profile.capabilityIsUnsupported(capability):
		checkedAt, err := time.Parse(time.RFC3339Nano, resource.Options[profile.CapabilityCheckedAtOption])
		return err == nil && s.imageCapabilityRetry > 0 && !time.Now().Before(checkedAt.Add(s.imageCapabilityRetry))
	default:
		return false
	}
}

func providerImageCapabilityModelNameSet(profiles []providerImageCapabilityRouteProfile) map[string]bool {
	models := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		if profile.PublicModel != "" {
			models[profile.PublicModel] = true
		}
	}
	return models
}
