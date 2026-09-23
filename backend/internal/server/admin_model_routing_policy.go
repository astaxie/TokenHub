package server

import (
	"net/http"
	"strings"
)

func (s *Server) serveAdminModelRoutingPolicyPatch(w http.ResponseWriter, r *http.Request, user AdminUser, modelName string) {
	var policy ModelRoutePolicy
	if err := s.decodeJSON(w, r, &policy); err != nil {
		writeError(w, r, err)
		return
	}
	policy.Strategy = strings.TrimSpace(policy.Strategy)
	if policy.Strategy == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_route_strategy", "Routing strategy is required"))
		return
	}
	if err := s.validateRoutePolicy(ModelRoute{Strategy: policy.Strategy}); err != nil {
		writeError(w, r, err)
		return
	}
	routes, err := s.store.UpdateModelRoutePolicy(modelName, policy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, model := range s.store.ListModels() {
		if model.Name == modelName {
			saved := modelSemanticRoutingPolicy(model)
			policy.SemanticRouting = &saved
			break
		}
	}
	s.recordAdminAudit(r, user, "update", "model_routing_policy", modelName, "", map[string]any{
		"semantic_routing": policy.SemanticRouting,
		"strategy":         policy.Strategy,
		"routes":           routes,
	})
	writeJSON(w, http.StatusOK, map[string]any{"strategy": policy.Strategy, "data": routes, "semantic_routing": policy.SemanticRouting})
}
