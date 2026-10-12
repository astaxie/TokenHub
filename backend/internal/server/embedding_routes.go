package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

func embeddingRouteSpace(route RouteSelection) string {
	var spaces map[string]string
	if json.Unmarshal([]byte(route.Provider.Options["embedding_spaces"]), &spaces) != nil {
		return ""
	}
	return strings.TrimSpace(spaces[route.ProviderModel])
}
func embeddingSpaceKey(route RouteSelection) string {
	if space := embeddingRouteSpace(route); space != "" {
		return "verified:" + space
	}
	identity, _ := json.Marshal([]string{route.Provider.ID, route.ProviderModel, route.Provider.Type,
		strings.TrimRight(strings.TrimSpace(route.Provider.BaseURL), "/"), providerEmbeddingProtocol(route.Provider), providerEmbeddingPath(route.Provider)})
	sum := sha256.Sum256(identity)
	return "provider-deployment:v2:" + hex.EncodeToString(sum[:])
}
func embeddingSpaceConflict() error {
	return NewHTTPError(409, "embedding_space_conflict", "All routes for an embedding model must share a verified vector space; configure matching space IDs or remove incompatible routes")
}

// The contract includes configured sources even when unhealthy or cooling down.
// A request ID, failover or recovery must never select a different vector space.
func (s *GormStore) EmbeddingSpaceContract(ctx context.Context, modelName string, candidate *ModelRoute) (string, error) {
	var contract string
	err := s.withReadSnapshot(func(db *gorm.DB) error {
		db = db.WithContext(ctx)
		var routes []ModelRoute
		if err := db.Where("model_name = ?", modelName).Find(&routes).Error; err != nil {
			return err
		}
		if candidate != nil {
			replaced := false
			for i := range routes {
				if candidate.ID != "" && routes[i].ID == candidate.ID {
					routes[i] = *candidate
					replaced = true
				}
			}
			if !replaced {
				routes = append(routes, *candidate)
			}
		}
		ids := []string{}
		for _, route := range routes {
			if route.Status == "" || route.Status == StatusActive {
				ids = append(ids, route.ProviderID)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		ids = uniqueStrings(ids)
		byID, err := loadRouteCandidateProviders(db, ids)
		if err != nil {
			return err
		}
		byProvider, err := loadRouteCandidateResourcesByProvider(db, ids)
		if err != nil {
			return err
		}
		byResource := map[string]ProviderResource{}
		for _, resources := range byProvider {
			for _, resource := range resources {
				byResource[resource.ID] = resource
			}
		}
		include := func(provider Provider, resource *ProviderResource, route ModelRoute) error {
			key := embeddingSpaceKey(RouteSelection{Provider: effectiveProviderResourceConfig(provider, resource), ProviderModel: route.ProviderModel})
			if contract != "" && contract != key {
				return embeddingSpaceConflict()
			}
			contract = key
			return nil
		}
		for _, route := range routes {
			if route.Status != "" && route.Status != StatusActive {
				continue
			}
			provider, ok := byID[route.ProviderID]
			if !ok {
				return embeddingSpaceConflict()
			}
			if route.ProviderResourceID != "" {
				resource, ok := byResource[route.ProviderResourceID]
				if !ok || resource.ProviderID != provider.ID {
					return embeddingSpaceConflict()
				}
				if err := include(provider, &resource, route); err != nil {
					return err
				}
				continue
			}
			group := strings.TrimSpace(route.ResourceGroup)
			for _, resource := range byProvider[provider.ID] {
				if resource.Status != StatusActive || (group != "" && resource.Group != group) {
					continue
				}
				if err := include(provider, &resource, route); err != nil {
					return err
				}
			}
			// Optional resources can fall back to provider credentials when all are down.
			if !s.routeCandidateResourcesRequireSelection(provider, byProvider[provider.ID]) {
				if err := include(provider, nil, route); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return contract, err
}

func (s *Server) embeddingSpaceContract(ctx context.Context, modelName string, candidate *ModelRoute) (string, error) {
	loader, ok := s.store.(interface {
		EmbeddingSpaceContract(context.Context, string, *ModelRoute) (string, error)
	})
	if !ok {
		return "", NewHTTPError(503, "embedding_space_unavailable", "Store cannot verify the embedding space contract")
	}
	return loader.EmbeddingSpaceContract(ctx, modelName, candidate)
}
func compatibleEmbeddingRoutes(routes []RouteSelection, contract string) []RouteSelection {
	result := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if contract != "" && embeddingSpaceKey(route) == contract {
			result = append(result, route)
		}
	}
	return result
}

// Validate all proposed routes together before the model and routes are written.
func (s *Server) validateInitialEmbeddingRoutes(ctx context.Context, model Model, routes []ModelRoute) error {
	if model.Modality != "embedding" {
		return nil
	}
	contract := ""
	for _, route := range routes {
		if route.Status != "" && route.Status != StatusActive {
			continue
		}
		space, err := s.embeddingSpaceContract(ctx, model.Name, &route)
		if err != nil {
			return err
		}
		if contract != "" && space != contract {
			return embeddingSpaceConflict()
		}
		contract = space
	}
	return nil
}
