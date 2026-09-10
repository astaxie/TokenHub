package server

import "strings"

func (s *Server) providerCatalogActiveAccountResourceID(providerType string, requireDeclaredResourceType bool) string {
	providerType = strings.TrimSpace(providerType)
	if s == nil || s.store == nil || providerType == "" {
		return ""
	}
	declaredResourceTypes := s.providerCatalogDeclaredAccountResourceTypes(providerType)
	if requireDeclaredResourceType && len(declaredResourceTypes) == 0 {
		return ""
	}
	for _, resource := range s.store.ListProviderResources() {
		if resource.Status != StatusActive {
			continue
		}
		provider, ok := s.store.GetProvider(resource.ProviderID)
		if !ok || provider.Type != providerType {
			continue
		}
		resourceType := strings.ToLower(strings.TrimSpace(resource.ResourceType))
		if len(declaredResourceTypes) > 0 {
			if _, ok := declaredResourceTypes[resourceType]; !ok {
				continue
			}
		} else if !s.store.IsProviderAccountResourceType(provider.Type, resource.ResourceType) {
			continue
		}
		return resource.ID
	}
	return ""
}

func (s *Server) providerCatalogDeclaredAccountResourceTypes(providerType string) map[string]struct{} {
	if s == nil || s.adapterRegistry == nil {
		return nil
	}
	descriptor, ok := s.adapterRegistry.Describe(providerType)
	if !ok {
		return nil
	}
	declared := map[string]struct{}{}
	for _, resourceType := range descriptor.ResourceTypes {
		name := strings.ToLower(strings.TrimSpace(resourceType.Type))
		if name == "" || name == ProviderResourceAPIKey {
			continue
		}
		declared[name] = struct{}{}
	}
	if len(declared) == 0 {
		return nil
	}
	return declared
}
