package server

import (
	"net/http"
	"strings"
)

func (s *Server) serveAdminModelsPost(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var req struct {
		Model
		Routes     []ModelRoute `json:"routes"`
		CreateOnly bool         `json:"create_only"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Model.Name = strings.TrimSpace(req.Model.Name)
	if req.Model.Name == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_model", "name is required"))
		return
	}
	priorities := routePriorityByModel(s.store.ListRoutes())
	seenRoutes := existingProviderModelRouteSet(s.store.ListRoutes())
	if req.CreateOnly {
		// Existing mappings are rejected by the atomic model collision guard.
		seenRoutes = map[string]bool{}
	}
	preparedRoutes := make([]ModelRoute, 0, len(req.Routes))
	for _, route := range req.Routes {
		if req.CreateOnly {
			route.ID = ""
		}
		route.ModelName = req.Model.Name
		route.ProviderID = strings.TrimSpace(route.ProviderID)
		route.ProviderModel = strings.TrimSpace(route.ProviderModel)
		if route.ProviderID == "" || route.ProviderModel == "" {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_route", "provider_id and provider_model are required"))
			return
		}
		if err := s.validateRouteAdapterForModel(route, &req.Model); err != nil {
			writeError(w, r, err)
			return
		}
		if err := s.validateImportedProviderModel(route); err != nil {
			writeError(w, r, err)
			return
		}
		routeKey := providerModelRouteKey(route.ProviderID, route.ProviderModel, route.ModelName)
		if seenRoutes[routeKey] {
			writeError(w, r, NewHTTPError(http.StatusConflict, "model_route_conflict", "This external model is already mapped to the selected provider model"))
			return
		}
		seenRoutes[routeKey] = true
		if route.Priority <= 0 {
			route.Priority = takeNextRoutePriority(priorities, route.ModelName)
		}
		preparedRoutes = append(preparedRoutes, route)
	}
	if err := s.validateInitialEmbeddingRoutes(r.Context(), req.Model, preparedRoutes); err != nil {
		writeError(w, r, err)
		return
	}
	req.Model = withExternalModelRole(req.Model)
	model, err := s.store.CreateModelWithRoutes(req.Model, preparedRoutes, ModelCreateOptions{CreateOnly: req.CreateOnly})
	if err != nil {
		if AsHTTPError(err).Code == "model_conflict" {
			writeError(w, r, err)
			return
		}
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "model_create_failed", "Failed to create model and initial routes"))
		return
	}
	s.recordAdminAudit(r, user, "create", "model", model.Name, "", model)
	writeJSON(w, http.StatusCreated, model)
}
