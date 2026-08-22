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

type providerModelPatchRequest struct {
	ProviderModel
	InputPriceUSDPer1M     *float64 `json:"input_price_usd_per_1m"`
	CacheReadPriceUSDPer1M *float64 `json:"cache_read_price_usd_per_1m"`
	OutputPriceUSDPer1M    *float64 `json:"output_price_usd_per_1m"`
}

func (patch providerModelPatchRequest) withCurrentCosts(current ProviderModel) ProviderModel {
	model := patch.ProviderModel
	model.InputPriceUSDPer1M = current.InputPriceUSDPer1M
	model.CacheReadPriceUSDPer1M = current.CacheReadPriceUSDPer1M
	model.OutputPriceUSDPer1M = current.OutputPriceUSDPer1M
	if patch.InputPriceUSDPer1M != nil {
		model.InputPriceUSDPer1M = *patch.InputPriceUSDPer1M
	}
	if patch.CacheReadPriceUSDPer1M != nil {
		model.CacheReadPriceUSDPer1M = *patch.CacheReadPriceUSDPer1M
	}
	if patch.OutputPriceUSDPer1M != nil {
		model.OutputPriceUSDPer1M = *patch.OutputPriceUSDPer1M
	}
	return model
}

func (s *Server) handleAdminProviderModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
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
	var req ProviderModelImportRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if costErr := providerModelCostDecodeError(err); costErr != nil {
			writeError(w, r, costErr)
			return
		}
		writeError(w, r, err)
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

type adminProviderModelItemHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

func (s *Server) handleAdminProviderModelItemRoute(w http.ResponseWriter, r *http.Request, serve adminProviderModelItemHandler) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	id := strings.Trim(r.PathValue("provider_model_id"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	serve(w, r, user, id)
}

func (s *Server) handleAdminProviderModelPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderModelItemRoute(w, r, s.serveAdminProviderModelPatch)
}

func (s *Server) handleAdminProviderModelDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderModelItemRoute(w, r, s.serveAdminProviderModelDelete)
}

func (s *Server) handleAdminProviderModelItem(w http.ResponseWriter, r *http.Request) {
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/provider-models/")
	if !strings.HasSuffix(r.URL.EscapedPath(), "/") && len(parts) == 1 && parts[0] == "import" {
		s.adminMethodNotAllowed("model", http.MethodPost)(w, r)
		return
	}
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
		s.serveAdminProviderModelPatch(w, r, user, id)
	case http.MethodDelete:
		s.serveAdminProviderModelDelete(w, r, user, id)
	default:
		jsonMethodNotAllowed(http.MethodPatch+", "+http.MethodDelete)(w, r)
	}
}

func (s *Server) serveAdminProviderModelPatch(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
	var patch providerModelPatchRequest
	if err := s.decodeJSON(w, r, &patch); err != nil {
		if costErr := providerModelCostDecodeError(err); costErr != nil {
			writeError(w, r, costErr)
			return
		}
		writeError(w, r, err)
		return
	}
	var current ProviderModel
	found := false
	for _, model := range s.store.ListProviderModels() {
		if model.ID == id {
			current = model
			found = true
			break
		}
	}
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_model_not_found", "Provider model not found"))
		return
	}
	req := patch.withCurrentCosts(current)
	if err := validateProviderModelCosts(req); err != nil {
		writeError(w, r, err)
		return
	}
	model, err := s.store.UpdateProviderModel(id, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "provider_model", id, "", model)
	writeJSON(w, http.StatusOK, model)
}

func (s *Server) serveAdminProviderModelDelete(w http.ResponseWriter, r *http.Request, user AdminUser, id string) {
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
}

func (s *Server) importProviderModels(req ProviderModelImportRequest) (ProviderModelImportResult, error) {
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusBadRequest, "provider_required", "provider_id is required")
	}
	_, ok := s.store.GetProvider(providerID)
	if !ok {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if req.Publish {
		return ProviderModelImportResult{}, NewHTTPError(
			http.StatusBadRequest,
			"provider_model_publication_must_be_configured_separately",
			"Create external models in Model Directory and routes in Routing Policies",
		)
	}
	if len(req.Models) == 0 {
		return ProviderModelImportResult{}, NewHTTPError(http.StatusBadRequest, "provider_models_required", "Select at least one provider model")
	}
	for _, catalogModel := range req.Models {
		if strings.TrimSpace(catalogModel.ID) == "" {
			continue
		}
		if err := validateProviderModelCosts(providerModelFromCatalog(providerID, catalogModel)); err != nil {
			return ProviderModelImportResult{}, err
		}
	}

	result := ProviderModelImportResult{ProviderModels: []ProviderModel{}}
	for _, catalogModel := range req.Models {
		catalogModel.ID = strings.TrimSpace(catalogModel.ID)
		if catalogModel.ID == "" {
			continue
		}
		providerModel := providerModelFromCatalog(providerID, catalogModel)
		providerModel = s.store.AddProviderModel(providerModel)
		result.ProviderModels = append(result.ProviderModels, providerModel)
		result.ImportedModels++
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
