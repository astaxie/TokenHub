package server

import (
	"context"
	"gorm.io/gorm"
)

// LoadProviderTestRoute uses the same decryption and resource override path as
// inference routing. Administrative list DTOs must never be used for execution.
func (s *GormStore) LoadProviderTestRoute(ctx context.Context, providerID, resourceID, model string) (RouteSelection, error) {
	var selection RouteSelection
	err := s.withReadSnapshot(func(db *gorm.DB) error {
		db = db.WithContext(ctx)
		var provider Provider
		if err := db.First(&provider, "id = ?", providerID).Error; err != nil {
			return notFound(err, "provider_not_found", "Provider not found")
		}
		route := ModelRoute{ProviderID: providerID, ProviderResourceID: resourceID, ProviderModel: model}
		if resourceID == "" {
			selection = s.routeSelection(provider, nil, route)
			return nil
		}
		var resource ProviderResource
		if err := db.First(&resource, "id = ?", resourceID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}
		if resource.ProviderID != providerID {
			return NewHTTPError(400, "route_resource_mismatch", "Resource must belong to Provider")
		}
		selection = s.routeSelection(provider, &resource, route)
		return nil
	})
	return selection, err
}
