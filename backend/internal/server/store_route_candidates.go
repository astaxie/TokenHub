package server

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const routeCandidateLookupBatchSize = 500

func (s *GormStore) SelectRouteCandidates(modelName string) ([]RouteSelection, error) {
	var selections []RouteSelection
	now := time.Now().UTC()
	runRead := func(load func(*gorm.DB, string, time.Time) ([]RouteSelection, error)) error {
		return s.withReadSnapshot(func(db *gorm.DB) error {
			var err error
			selections, err = load(db, modelName, now)
			return err
		})
	}
	err := runRead(s.loadRouteCandidates)
	var batchErr *routeCandidateBatchLookupError
	if errors.As(err, &batchErr) {
		err = runRead(s.loadRouteCandidatesIndividually)
	}
	if err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return nil, ErrProviderMissing
	}
	return selections, nil
}

type routeCandidateBatchLookupError struct {
	target string
	err    error
}

func (e *routeCandidateBatchLookupError) Error() string {
	return fmt.Sprintf("batch load route candidate %s: %v", e.target, e.err)
}

func (e *routeCandidateBatchLookupError) Unwrap() error {
	return e.err
}

func (s *GormStore) loadRouteCandidates(db *gorm.DB, modelName string, now time.Time) ([]RouteSelection, error) {
	var routes []ModelRoute
	if err := db.Where("model_name = ? AND status = ?", modelName, StatusActive).
		Order("priority asc, weight desc, created_at asc").
		Find(&routes).Error; err != nil {
		return nil, err
	}

	providerIDs, explicitResourceIDs, implicitProviderIDs := routeCandidateLookupIDs(routes)
	providers, err := loadRouteCandidateProviders(db, providerIDs)
	if err != nil {
		return nil, &routeCandidateBatchLookupError{target: "providers", err: err}
	}
	explicitResources, err := loadRouteCandidateResourcesByID(db, explicitResourceIDs)
	if err != nil {
		return nil, &routeCandidateBatchLookupError{target: "provider resources", err: err}
	}
	implicitResources, err := loadRouteCandidateResourcesByProvider(db, implicitProviderIDs, now)
	if err != nil {
		return nil, err
	}

	selections := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		provider, ok := providers[route.ProviderID]
		if !ok || provider.Status != StatusActive || !provider.Healthy {
			continue
		}
		if route.ProviderResourceID != "" {
			resource, ok := explicitResources[route.ProviderResourceID]
			if !ok || resource.ProviderID != provider.ID || resource.Status != StatusActive || !halfOpenEligible(resource, now) {
				continue
			}
			selections = append(selections, s.routeSelection(provider, &resource, route))
			continue
		}

		group := strings.TrimSpace(route.ResourceGroup)
		matched := false
		for _, resource := range implicitResources[provider.ID] {
			if group != "" && resource.Group != group {
				continue
			}
			matched = true
			resourceRoute := route
			resourceRoute.ProviderResourceID = resource.ID
			if resource.Weight > 0 {
				resourceRoute.Weight = resource.Weight
			}
			selections = append(selections, s.routeSelection(provider, &resource, resourceRoute))
		}
		if !matched {
			selections = append(selections, s.routeSelection(provider, nil, route))
		}
	}
	if err := s.attachRouteRuntimeStats(db, selections, now); err != nil {
		return nil, err
	}
	return selections, nil
}

func (s *GormStore) loadRouteCandidatesIndividually(db *gorm.DB, modelName string, now time.Time) ([]RouteSelection, error) {
	var routes []ModelRoute
	if err := db.Where("model_name = ? AND status = ?", modelName, StatusActive).
		Order("priority asc, weight desc, created_at asc").
		Find(&routes).Error; err != nil {
		return nil, err
	}

	selections := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		var provider Provider
		found, err := s.bestEffortRouteCandidateLookup(db, func() error {
			return db.First(&provider, "id = ?", route.ProviderID).Error
		})
		if err != nil {
			return nil, err
		}
		if !found || provider.Status != StatusActive || !provider.Healthy {
			continue
		}
		if route.ProviderResourceID != "" {
			var resource ProviderResource
			found, err := s.bestEffortRouteCandidateLookup(db, func() error {
				return db.First(&resource, "id = ? AND provider_id = ?", route.ProviderResourceID, provider.ID).Error
			})
			if err != nil {
				return nil, err
			}
			if !found || resource.Status != StatusActive || !halfOpenEligible(resource, now) {
				continue
			}
			selections = append(selections, s.routeSelection(provider, &resource, route))
			continue
		}

		var resources []ProviderResource
		query := db.Where("provider_id = ? AND status = ? AND (healthy = ? OR cooldown_until <= ?)",
			provider.ID, StatusActive, true, now)
		if group := strings.TrimSpace(route.ResourceGroup); group != "" {
			query = query.Where("\"group\" = ?", group)
		}
		if err := query.Order("priority asc, weight desc, created_at asc, id asc").Find(&resources).Error; err != nil {
			return nil, err
		}
		if len(resources) == 0 {
			selections = append(selections, s.routeSelection(provider, nil, route))
			continue
		}
		for _, resource := range resources {
			resourceRoute := route
			resourceRoute.ProviderResourceID = resource.ID
			if resource.Weight > 0 {
				resourceRoute.Weight = resource.Weight
			}
			selections = append(selections, s.routeSelection(provider, &resource, resourceRoute))
		}
	}
	if err := s.attachRouteRuntimeStats(db, selections, now); err != nil {
		return nil, err
	}
	return selections, nil
}

func (s *GormStore) bestEffortRouteCandidateLookup(db *gorm.DB, lookup func() error) (bool, error) {
	if s.dbDriver != "postgres" {
		return lookup() == nil, nil
	}
	const (
		createSavepoint   = "SAVEPOINT route_candidate_item_lookup"
		rollbackSavepoint = "ROLLBACK TO SAVEPOINT route_candidate_item_lookup"
		releaseSavepoint  = "RELEASE SAVEPOINT route_candidate_item_lookup"
	)
	if err := db.Exec(createSavepoint).Error; err != nil {
		return false, fmt.Errorf("create route candidate item savepoint: %w", err)
	}
	if err := lookup(); err != nil {
		if rollbackErr := db.Exec(rollbackSavepoint).Error; rollbackErr != nil {
			return false, fmt.Errorf("load route candidate item: %v; rollback savepoint: %w", err, rollbackErr)
		}
		if releaseErr := db.Exec(releaseSavepoint).Error; releaseErr != nil {
			return false, fmt.Errorf("release failed route candidate item savepoint: %w", releaseErr)
		}
		return false, nil
	}
	if err := db.Exec(releaseSavepoint).Error; err != nil {
		return false, fmt.Errorf("release route candidate item savepoint: %w", err)
	}
	return true, nil
}

func routeCandidateLookupIDs(routes []ModelRoute) ([]string, []string, []string) {
	providerIDs := make([]string, 0, len(routes))
	explicitResourceIDs := make([]string, 0, len(routes))
	implicitProviderIDs := make([]string, 0, len(routes))
	providersSeen := make(map[string]bool, len(routes))
	explicitSeen := make(map[string]bool, len(routes))
	implicitSeen := make(map[string]bool, len(routes))
	for _, route := range routes {
		if route.ProviderID != "" && !providersSeen[route.ProviderID] {
			providersSeen[route.ProviderID] = true
			providerIDs = append(providerIDs, route.ProviderID)
		}
		if route.ProviderResourceID != "" {
			if !explicitSeen[route.ProviderResourceID] {
				explicitSeen[route.ProviderResourceID] = true
				explicitResourceIDs = append(explicitResourceIDs, route.ProviderResourceID)
			}
			continue
		}
		if route.ProviderID != "" && !implicitSeen[route.ProviderID] {
			implicitSeen[route.ProviderID] = true
			implicitProviderIDs = append(implicitProviderIDs, route.ProviderID)
		}
	}
	return providerIDs, explicitResourceIDs, implicitProviderIDs
}

func loadRouteCandidateProviders(db *gorm.DB, ids []string) (map[string]Provider, error) {
	providers := make(map[string]Provider, len(ids))
	err := eachRouteCandidateBatch(ids, func(batch []string) error {
		var items []Provider
		if err := db.Where("id IN ?", batch).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			providers[item.ID] = item
		}
		return nil
	})
	return providers, err
}

func loadRouteCandidateResourcesByID(db *gorm.DB, ids []string) (map[string]ProviderResource, error) {
	resources := make(map[string]ProviderResource, len(ids))
	err := eachRouteCandidateBatch(ids, func(batch []string) error {
		var items []ProviderResource
		if err := db.Where("id IN ?", batch).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			resources[item.ID] = item
		}
		return nil
	})
	return resources, err
}

func loadRouteCandidateResourcesByProvider(db *gorm.DB, providerIDs []string, now time.Time) (map[string][]ProviderResource, error) {
	resources := make(map[string][]ProviderResource, len(providerIDs))
	err := eachRouteCandidateBatch(providerIDs, func(batch []string) error {
		var items []ProviderResource
		if err := db.Where("provider_id IN ? AND status = ? AND (healthy = ? OR cooldown_until <= ?)",
			batch, StatusActive, true, now).
			Order("priority asc, weight desc, created_at asc, id asc").
			Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			resources[item.ProviderID] = append(resources[item.ProviderID], item)
		}
		return nil
	})
	return resources, err
}

func eachRouteCandidateBatch(ids []string, load func([]string) error) error {
	for start := 0; start < len(ids); start += routeCandidateLookupBatchSize {
		end := min(start+routeCandidateLookupBatchSize, len(ids))
		if err := load(ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}
