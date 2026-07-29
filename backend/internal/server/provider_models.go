package server

import (
	"net/http"
	"strings"
	"time"
)

const (
	modelDirectoryRoleKey      = "directory_role"
	modelDirectoryRoleExternal = "external"
)

func (s *Server) handleAdminProviderModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	models := s.store.ListProviderModels()
	if providerID != "" {
		filtered := make([]ProviderModel, 0, len(models))
		for _, model := range models {
			if model.ProviderID == providerID {
				filtered = append(filtered, model)
			}
		}
		models = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": models})
}

func (s *Server) handleAdminProviderModelImport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "model", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	var req ProviderModelImportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
		return
	}
	result, err := s.importProviderModels(req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "import", "provider_model", req.ProviderID, "", result)
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleAdminProviderModelItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/provider-models/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req ProviderModel
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
			return
		}
		model, err := s.store.UpdateProviderModel(id, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "provider_model", id, "", model)
		writeJSON(w, http.StatusOK, model)
	case http.MethodDelete:
		if providerModelHasRoutes(id, s.store.ListProviderModels(), s.store.ListRoutes()) {
			writeError(w, r, NewHTTPError(http.StatusConflict, "provider_model_in_use", "Provider model is still used by a model route"))
			return
		}
		if err := s.store.DeleteProviderModel(id); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "provider_model", id, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) importProviderModels(req ProviderModelImportRequest) (ProviderModelImportResult, error) {
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusBadRequest, "provider_required", "provider_id is required")
	}
	provider, ok := s.store.GetProvider(providerID)
	if !ok {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if req.Publish {
		if _, ok := s.adapterRegistry.Describe(provider.Type); !ok {
			return ProviderModelImportResult{}, NewHTTPError(http.StatusBadRequest, "provider_adapter_missing", "Provider adapter is not registered")
		}
	}
	if len(req.Models) == 0 {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusBadRequest, "provider_models_required", "Select at least one provider model")
	}

	result := ProviderModelImportResult{ProviderModels: []ProviderModel{}}
	knownModels := map[string]Model{}
	for _, model := range s.store.ListModels() {
		knownModels[strings.TrimSpace(model.Name)] = model
	}
	existingRoutes := s.store.ListRoutes()
	routePriorities := routePriorityByModel(existingRoutes)
	seenRoutes := existingProviderModelRouteSet(existingRoutes)
	for _, catalogModel := range req.Models {
		catalogModel.ID = strings.TrimSpace(catalogModel.ID)
		if catalogModel.ID == "" {
			continue
		}
		providerModel := providerModelFromCatalog(providerID, catalogModel)
		providerModel = s.store.AddProviderModel(providerModel)
		result.ProviderModels = append(result.ProviderModels, providerModel)
		result.ImportedModels++
		if !req.Publish {
			continue
		}

		externalName := strings.TrimSpace(req.ExternalNames[catalogModel.ID])
		if externalName == "" {
			externalName = firstNonEmpty(catalogModel.CanonicalName, canonicalModelName(catalogModel.ID, catalogModel.DisplayName), catalogModel.ID)
		}
		existingModel, exists := knownModels[externalName]
		if !exists {
			record := withExternalModelRole(providerCatalogModelRecord(catalogModel, externalName))
			if record.Metadata == nil {
				record.Metadata = map[string]string{}
			}
			record.Metadata["source"] = "provider-import"
			record.Metadata["provider_id"] = providerID
			record.Metadata["upstream_model"] = catalogModel.ID
			s.store.AddModel(record)
			knownModels[externalName] = record
			result.CreatedModels++
		} else if existingModel.Status != StatusActive {
			existingModel.Status = StatusActive
			updated, err := s.store.UpdateModel(externalName, existingModel)
			if err != nil {
				return ProviderModelImportResult{}, err
			}
			knownModels[externalName] = updated
		}
		if err := s.markExternalModel(externalName); err != nil {
			return ProviderModelImportResult{}, err
		}
		result.ModelNames = appendUniqueString(result.ModelNames, externalName)

		routeKey := providerModelRouteKey(providerID, catalogModel.ID, externalName)
		if seenRoutes[routeKey] {
			continue
		}
		route := ProviderCatalogModelRoute(providerID, catalogModel)
		route.ID = stableProviderModelRouteID(providerID, catalogModel.ID, externalName)
		route.ModelName = externalName
		route.Priority = takeNextRoutePriority(routePriorities, externalName)
		route = s.store.AddRoute(route)
		seenRoutes[routeKey] = true
		result.CreatedRoutes++
		result.RouteIDs = append(result.RouteIDs, route.ID)
	}
	if result.ImportedModels == 0 {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusBadRequest, "provider_models_required", "Select at least one provider model")
	}
	return result, nil
}

func providerModelFromCatalog(providerID string, model ProviderCatalogModel) ProviderModel {
	now := time.Now().UTC()
	return ProviderModel{
		ProviderID:             providerID,
		UpstreamModel:          model.ID,
		DisplayName:            firstNonEmpty(model.DisplayName, model.Name, model.ID),
		CanonicalName:          firstNonEmpty(model.CanonicalName, canonicalModelName(model.ID, model.DisplayName)),
		Category:               standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.ID, model.DisplayName))),
		Family:                 firstNonEmpty(model.Family, inferModelFamily(model.ID)),
		Modality:               firstNonEmpty(model.Type, normalizeModelModality(model.ID)),
		ContextWindow:          model.ContextWindow,
		InputPriceUSDPer1M:     model.InputPriceUSDPer1M,
		CacheReadPriceUSDPer1M: model.CacheReadPriceUSDPer1M,
		OutputPriceUSDPer1M:    model.OutputPriceUSDPer1M,
		InputModalities:        append([]string(nil), model.InputModalities...),
		OutputModalities:       append([]string(nil), model.OutputModalities...),
		Capabilities:           append([]string(nil), model.Capabilities...),
		SupportedParameters:    append([]string(nil), model.SupportedParameters...),
		Metadata:               cloneStringMap(model.Metadata),
		Source:                 firstNonEmpty(model.Metadata["source"], "provider-catalog"),
		Status:                 StatusActive,
		LastSeenAt:             &now,
	}
}

func existingProviderModelRouteSet(routes []ModelRoute) map[string]bool {
	set := map[string]bool{}
	for _, route := range routes {
		set[providerModelRouteKey(route.ProviderID, route.ProviderModel, route.ModelName)] = true
	}
	return set
}

func providerModelRouteKey(providerID string, upstreamModel string, externalName string) string {
	return strings.TrimSpace(providerID) + "\x00" + strings.TrimSpace(upstreamModel) + "\x00" + strings.TrimSpace(externalName)
}

func providerModelHasRoutes(id string, models []ProviderModel, routes []ModelRoute) bool {
	for _, model := range models {
		if model.ID != id {
			continue
		}
		for _, route := range routes {
			if route.ProviderID == model.ProviderID && route.ProviderModel == model.UpstreamModel {
				return true
			}
		}
		return false
	}
	return false
}

func appendUniqueString(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func backfillProviderModelsFromRoutes(store Store) {
	existing := map[string]bool{}
	for _, model := range store.ListProviderModels() {
		existing[providerModelRouteKey(model.ProviderID, model.UpstreamModel, "")] = true
	}
	models := store.ListModels()
	for _, route := range store.ListRoutes() {
		key := providerModelRouteKey(route.ProviderID, route.ProviderModel, "")
		if existing[key] {
			continue
		}
		store.AddProviderModel(providerModelFromRoute(route, models))
		existing[key] = true
	}
}

func withExternalModelRole(model Model) Model {
	model.Metadata = cloneStringMap(model.Metadata)
	if model.Metadata == nil {
		model.Metadata = map[string]string{}
	}
	model.Metadata[modelDirectoryRoleKey] = modelDirectoryRoleExternal
	return model
}

func (s *Server) markExternalModel(name string) error {
	for _, model := range s.store.ListModels() {
		if model.Name != strings.TrimSpace(name) {
			continue
		}
		if model.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal {
			return nil
		}
		_, err := s.store.UpdateModel(model.Name, withExternalModelRole(model))
		return err
	}
	return NewHTTPError(http.StatusBadRequest, "route_model_not_found", "Route external model does not exist")
}

func backfillExternalModelRolesFromRoutes(store Store) {
	routed := map[string]bool{}
	for _, route := range store.ListRoutes() {
		routed[route.ModelName] = true
	}
	for _, model := range store.ListModels() {
		if !routed[model.Name] || model.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal {
			continue
		}
		_, _ = store.UpdateModel(model.Name, withExternalModelRole(model))
	}
}

func providerModelFromRoute(route ModelRoute, models []Model) ProviderModel {
	providerModel := ProviderModel{
		ProviderID:    route.ProviderID,
		UpstreamModel: route.ProviderModel,
		DisplayName:   route.ProviderModel,
		CanonicalName: route.ModelName,
		Category:      inferModelCategory(route.ProviderModel, route.ModelName),
		Family:        inferModelFamily(route.ProviderModel),
		Modality:      normalizeModelModality(route.ProviderModel),
		Source:        "existing-route",
		Status:        StatusActive,
		Metadata: map[string]string{
			"source":          "existing-route",
			"published_model": route.ModelName,
		},
	}
	for _, model := range models {
		if model.Name != route.ModelName && model.ID != route.ModelName {
			continue
		}
		providerModel.Category = model.Category
		providerModel.Family = model.Family
		providerModel.Modality = model.Modality
		providerModel.ContextWindow = model.ContextWindow
		providerModel.InputPriceUSDPer1M = model.InputPriceUSDPer1M
		providerModel.CacheReadPriceUSDPer1M = model.CacheReadPriceUSDPer1M
		providerModel.OutputPriceUSDPer1M = model.OutputPriceUSDPer1M
		providerModel.InputModalities = append([]string(nil), model.InputModalities...)
		providerModel.OutputModalities = append([]string(nil), model.OutputModalities...)
		providerModel.Capabilities = append([]string(nil), model.Capabilities...)
		providerModel.SupportedParameters = append([]string(nil), model.SupportedParameters...)
		break
	}
	return providerModel
}
