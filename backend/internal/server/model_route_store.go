package server

import (
	"errors"
	"net/http"
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

type ModelCreateOptions struct {
	CreateOnly bool
}

func (s *GormStore) CreateModelWithRoutes(model Model, routes []ModelRoute, options ...ModelCreateOptions) (Model, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var created Model
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = createModelRecord(tx, model, options...)
		if err != nil {
			return err
		}
		for _, route := range routes {
			if len(options) > 0 && options[0].CreateOnly {
				route.ID = ""
			}
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

func createModelRecord(db *gorm.DB, model Model, options ...ModelCreateOptions) (Model, error) {
	createOnly := len(options) > 0 && options[0].CreateOnly
	if createOnly {
		model = withExternalModelRole(model)
	}
	if err := validateAudioPriceMetadata(model.Metadata); err != nil {
		return Model{}, err
	}
	if err := validateRetrievalPriceMetadata(model.Metadata); err != nil {
		return Model{}, err
	}
	if err := validateModelPricingPeriods(model.PricingPeriods); err != nil {
		return Model{}, err
	}
	normalizeModelCacheWriteConfiguration(&model)
	if model.Modality == "embedding" {
		model.CacheReadPriceUSDPer1M = 0
		model.CacheWritePriceUSDPer1M = 0
		model.CacheWritePriceConfigured = false
		model.CacheWrite5mPriceUSDPer1M = 0
		model.CacheWrite5mPriceConfigured = false
		model.CacheWrite1hPriceUSDPer1M = 0
		model.CacheWrite1hPriceConfigured = false
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
	err := db.Transaction(func(tx *gorm.DB) error {
		var existing Model
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "name = ?", model.Name)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// A concurrent creator can win after the lookup. Do not overwrite its
			// managed policy in that case; acquire its row lock before updating it.
			inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
			if inserted.Error != nil {
				return inserted.Error
			}
			if inserted.RowsAffected == 1 {
				return nil
			}
			result = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name = ? OR id = ?", model.Name, model.ID).First(&existing)
		}
		if result.Error != nil {
			return result.Error
		}
		if createOnly {
			if err := validateModelCreateOnly(tx, existing, model.Name); err != nil {
				return err
			}
			// Promotion retains the reference row's identity and creation history.
			model.ID, model.CreatedAt = existing.ID, existing.CreatedAt
		}
		if existing.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal && model.Metadata[modelDirectoryRoleKey] != modelDirectoryRoleExternal {
			model = withExternalModelRole(model)
			model.Status = existing.Status
			model.CreatedAt = existing.CreatedAt
		}
		model.Metadata = preserveSemanticRoutingMetadata(existing.Metadata, model.Metadata)
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model).Error
	})
	return model, err
}

// The caller holds the existing model row lock until its routes are committed.
// Rechecking here also covers an insert lost to a concurrent model creator.
func validateModelCreateOnly(tx *gorm.DB, existing Model, name string) error {
	if existing.Name != name || existing.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal {
		return modelCreateConflict()
	}
	switch existing.Metadata["source"] {
	case "tokenhub-standard-catalog", "public-provider-conf":
	default:
		return modelCreateConflict()
	}
	var routes int64
	if err := tx.Model(&ModelRoute{}).Where("model_name = ?", existing.Name).Count(&routes).Error; err != nil {
		return err
	}
	if routes > 0 {
		return modelCreateConflict()
	}
	return nil
}

func modelCreateConflict() error {
	return NewHTTPError(http.StatusConflict, "model_conflict", "An external model with this name already exists")
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
