package server

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) AddModel(model Model) Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	model, _ = createModelRecord(s.db, model)
	// createModelRecord reports no error here, so the snapshot is dropped either
	// way: an unnecessary invalidation costs one reload, a missed one leaves a new
	// model reported as unknown for a full TTL.
	s.modelLabels.invalidate()
	return model
}

func (s *GormStore) CreateModelWithRoutes(model Model, routes []ModelRoute) (Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var created Model
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = createModelRecord(tx, model)
		if err != nil {
			return err
		}
		for _, route := range routes {
			if _, err := createRouteRecord(tx, route); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.modelLabels.invalidate()
	}
	return created, err
}

func createModelRecord(db *gorm.DB, model Model) (Model, error) {
	var existing Model
	if err := db.First(&existing, "name = ?", model.Name).Error; err == nil &&
		existing.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal &&
		model.Metadata[modelDirectoryRoleKey] != modelDirectoryRoleExternal {
		model = withExternalModelRole(model)
		model.Status = existing.Status
		model.CreatedAt = existing.CreatedAt
	}

	if model.Modality == "embedding" {
		model.CacheReadPriceUSDPer1M = 0
	}
	if model.ID == "" {
		model.ID = model.Name
	}
	if model.Status == "" {
		model.Status = StatusActive
	}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = time.Now().UTC()
	}
	return model, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model).Error
}

func (s *GormStore) AddRoute(route ModelRoute) ModelRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	route, _ = createRouteRecord(s.db, route)
	return route
}

// CreateRoute persists a route and reports the database error. AddRoute
// discards it for callers that treat the write as best-effort, but admin
// route creation needs the error so a failed write is not mistaken for a
// successful route followed by a partial catalog update.
func (s *GormStore) CreateRoute(route ModelRoute) (ModelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return createRouteRecord(s.db, route)
}

func createRouteRecord(db *gorm.DB, route ModelRoute) (ModelRoute, error) {
	if route.ID == "" {
		route.ID = NewID("route")
	}
	if route.Status == "" {
		route.Status = StatusActive
	}
	if route.Weight <= 0 {
		route.Weight = 1
	}
	if route.Strategy == "" {
		route.Strategy = RouteStrategyBalanced
	}
	route.ProjectScope, route.ProjectIDs = normalizeRouteProjectScope(route.ProjectScope, route.ProjectIDs)
	route.Tags = uniqueStrings(route.Tags)
	if route.CreatedAt.IsZero() {
		route.CreatedAt = time.Now().UTC()
	}
	return route, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&route).Error
}
