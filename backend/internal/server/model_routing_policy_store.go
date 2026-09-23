package server

import (
	"net/http"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *GormStore) UpdateModelRoutePolicy(modelName string, policy ModelRoutePolicy) ([]ModelRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateSemanticRoutingPolicy(policy.SemanticRouting); err != nil {
		return nil, err
	}
	modelName = strings.TrimSpace(modelName)
	var updated []ModelRoute
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Match model edits and catalog refreshes: lock the model before routes.
		var model Model
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "name = ?", modelName).Error; err != nil {
			return notFound(err, "model_not_found", "Model not found")
		}
		if policy.Strategy != RouteStrategyJev {
			saved := modelSemanticRoutingPolicy(model)
			if policy.SemanticRouting != nil {
				saved = *policy.SemanticRouting
			}
			if len(saved.Candidates) > 0 {
				saved.Mode = "off"
				policy.SemanticRouting = &saved
			}
		}
		var routes []ModelRoute
		if err := tx.Where("model_name = ?", modelName).Order("priority asc, created_at asc, id asc").Find(&routes).Error; err != nil {
			return err
		}
		if len(routes) == 0 {
			return NewHTTPError(http.StatusNotFound, "model_routes_not_found", "Model has no routing rules")
		}
		if err := validateJevStrategyPolicy(policy, routes); err != nil {
			return err
		}
		previous := modelSemanticRoutingPolicy(model)
		if previous.ResponseBindingRequired || len(previous.Candidates) > 0 || policy.Strategy == RouteStrategyJev {
			if policy.SemanticRouting == nil {
				policy.SemanticRouting = &previous
			}
			copy := *policy.SemanticRouting
			copy.ResponseBindingRequired = true
			policy.SemanticRouting = &copy
		}
		if len(policy.Routes) != len(routes) {
			return NewHTTPError(http.StatusBadRequest, "invalid_model_route_policy", "Routing policy must include every route for the model")
		}

		routeByID := make(map[string]*ModelRoute, len(routes))
		for index := range routes {
			routeByID[routes[index].ID] = &routes[index]
		}
		seen := make(map[string]bool, len(policy.Routes))
		for _, patch := range policy.Routes {
			if seen[patch.RouteID] || routeByID[patch.RouteID] == nil {
				return NewHTTPError(http.StatusBadRequest, "invalid_model_route_policy", "Routing policy contains an unknown or duplicate route")
			}
			if patch.Weight <= 0 || patch.QualityScore < 1 || patch.QualityScore > 100 || patch.CostScore < 1 || patch.CostScore > 100 {
				return NewHTTPError(http.StatusBadRequest, "invalid_model_route_parameters", "Weight must be positive and route scores must be between 1 and 100")
			}
			seen[patch.RouteID] = true
		}

		updated = make([]ModelRoute, 0, len(routes))
		for index, patch := range policy.Routes {
			route := routeByID[patch.RouteID]
			route.Strategy = policy.Strategy
			route.Weight = patch.Weight
			route.QualityScore = patch.QualityScore
			route.CostScore = patch.CostScore
			if policy.Strategy == RouteStrategyPriorityOnly {
				route.Priority = index + 1
			} else {
				route.Priority = 1
			}
			if err := tx.Save(route).Error; err != nil {
				return err
			}
			updated = append(updated, *route)
		}
		return saveSemanticRoutingPolicy(tx, modelName, policy.SemanticRouting)
	})
	return updated, err
}
