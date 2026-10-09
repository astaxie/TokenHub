package server

import (
	"errors"
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
		if !isSemanticStrategy(policy.Strategy) {
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
		if isSemanticStrategy(policy.Strategy) {
			if err := validateJevClassifierModel(tx, modelName, *policy.SemanticRouting); err != nil {
				return err
			}
		}
		previous := modelSemanticRoutingPolicy(model)
		if previous.ResponseBindingRequired || len(previous.Candidates) > 0 || isSemanticStrategy(policy.Strategy) {
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

// validateJevClassifierModel checks that a model evaluator names a public model
// that can answer on its own: active, not the routed model, with an active route,
// and not itself routed by Jev. The gateway re-checks nesting at request time.
func validateJevClassifierModel(tx *gorm.DB, modelName string, policy SemanticRoutingPolicy) error {
	if !policy.usesModelEvaluator() {
		return nil
	}
	invalid := func(message string) error {
		return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", message)
	}
	if policy.ClassifierModel == modelName {
		return invalid("The classifier model must differ from the routed model")
	}
	var classifier Model
	if err := tx.First(&classifier, "name = ?", policy.ClassifierModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invalid("The classifier model does not exist")
		}
		return err
	}
	if classifier.Status != StatusActive {
		return invalid("The classifier model is not active")
	}
	if modelSemanticRoutingPolicy(classifier).Mode != "off" {
		return invalid("The classifier model must not use smart routing")
	}
	var routes []ModelRoute
	if err := tx.Where("model_name = ? AND status = ?", classifier.Name, StatusActive).Find(&routes).Error; err != nil {
		return err
	}
	if len(routes) == 0 {
		return invalid("The classifier model has no active route")
	}
	for _, route := range routes {
		if route.Status == StatusActive && isSemanticStrategy(routeStrategy(route)) {
			return invalid("The classifier model must not use smart routing")
		}
	}
	return nil
}
