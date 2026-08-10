package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleAdminProviders(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListProviders()})
	case http.MethodPost:
		var req ProviderCreateRequest
		if err := decodeJSON(r, &req); err != nil {
			if costErr := providerModelCostDecodeError(err); costErr != nil {
				writeError(w, r, costErr)
				return
			}
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		provider, catalog, catalogSource, err := s.providerForCreate(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if provider.Name == "" || provider.Type == "" {
			writeError(w, r, NewHTTPError(400, "invalid_provider", "name and type are required"))
			return
		}
		created := s.store.AddProvider(provider)
		result := ProviderCreateResult{
			Provider:      created,
			CatalogSource: catalogSource,
		}
		result.ImportedModels = s.importSelectedProviderCatalogModels(created.ID, catalog, req.SelectedModels)
		s.recordAdminAudit(r, user, "create", "provider", created.ID, "", result)
		writeJSON(w, http.StatusCreated, result)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminProviderMonitoring(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.providerMonitoringSnapshots(r.Context(), "")})
}

func (s *Server) handleAdminProviderCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	refresh := r.URL.Query().Get("refresh") == "true"
	entries, source, err := s.providerCatalog.List(r.Context(), refresh)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries, "source": source})
}

func (s *Server) handleAdminProviderCatalogItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/provider-catalog/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if id == codexProviderCatalogID {
		var (
			entry ProviderCatalogEntry
			err   error
		)
		switch r.Method {
		case http.MethodGet:
			resourceID := strings.TrimSpace(r.URL.Query().Get("resource_id"))
			if resourceID == "" {
				for _, resource := range s.store.ListProviderResources() {
					if isOpenAIAccountResource(resource.ResourceType) && resource.Status == StatusActive {
						resourceID = resource.ID
						break
					}
				}
			}
			if resourceID == "" {
				writeError(w, r, NewHTTPError(http.StatusConflict, "codex_account_required", "Connect an OpenAI Codex subscription account before loading its models"))
				return
			}
			entry, err = s.queryOpenAICodexModels(r.Context(), resourceID)
		case http.MethodPost:
			var credentials ProviderResourceCredentials
			if decodeErr := decodeJSON(r, &credentials); decodeErr != nil {
				writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", decodeErr.Error()))
				return
			}
			entry, err = s.codexSubscription.ModelsWithCredentials(r.Context(), credentials)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": entry, "source": entry.Source})
		return
	}
	if id == "custom" && r.Method == http.MethodPost {
		var req ProviderCreateRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
			return
		}
		if providerID := firstNonEmpty(strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.ID)); providerID != "" {
			if provider, ok := s.store.GetProvider(providerID); ok {
				if req.Name == "" {
					req.Name = provider.Name
				}
				if req.Type == "" {
					req.Type = provider.Type
				}
				if req.BaseURL == "" {
					req.BaseURL = provider.BaseURL
				}
				if req.APIKey == "" {
					req.APIKey = provider.APIKey
				}
			}
		}
		entry, err := CustomProviderCatalogFromUpstream(r.Context(), http.DefaultClient, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": entry, "source": entry.Source})
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	refresh := r.URL.Query().Get("refresh") == "true"
	entry, source, ok, err := s.providerCatalog.Get(r.Context(), id, refresh)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !ok {
		writeError(w, r, NewHTTPError(404, "provider_catalog_not_found", "Provider catalog entry not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entry, "source": source})
}

func (s *Server) providerFromCreateRequest(ctx context.Context, req ProviderCreateRequest) (Provider, ProviderCatalogEntry, string, error) {
	var catalog ProviderCatalogEntry
	catalogSource := ""
	catalogID := strings.TrimSpace(req.CatalogID)
	if catalogID == codexProviderCatalogID {
		if len(req.CustomModels) > 0 {
			catalog = codexProviderCatalogFromModels(req.CustomModels)
		} else {
			catalog = s.codexProviderCatalogFromStandardModels(req.SelectedModels)
		}
		catalogSource = catalog.Source
	} else if catalogID != "" {
		entry, source, ok, err := s.providerCatalog.Get(ctx, catalogID, false)
		if err != nil {
			return Provider{}, ProviderCatalogEntry{}, source, err
		}
		if !ok {
			return Provider{}, ProviderCatalogEntry{}, source, NewHTTPError(400, "provider_catalog_not_found", "Provider catalog entry not found")
		}
		catalog = entry
		catalogSource = source
	}
	if catalog.ID == "custom" {
		if len(req.CustomModels) > 0 {
			catalog = customProviderCatalogFromModels(req.CustomModels, req.ModelCategory)
		} else {
			catalog = s.customProviderCatalogFromStandardModels(req.ModelCategory)
		}
		catalogSource = catalog.Source
	}
	id := strings.TrimSpace(req.ID)
	if id == "" && catalog.ID != "" && catalog.ID != "custom" {
		id = "prv_" + sanitizeIdentifier(catalog.ID)
	}
	provider := Provider{
		ID:       id,
		Name:     firstNonEmpty(req.Name, catalog.DisplayName, catalog.Name),
		Type:     firstNonEmpty(req.Type, catalog.Type, ProviderOpenAICompatible),
		BaseURL:  firstNonEmpty(req.BaseURL, catalog.BaseURL),
		APIKey:   req.APIKey,
		Status:   firstNonEmpty(req.Status, StatusActive),
		Healthy:  req.Healthy != nil && *req.Healthy,
		Priority: req.Priority,
		Headers:  req.Headers,
		Options:  req.Options,
	}
	if _, ok := s.adapterRegistry.Describe(provider.Type); !ok {
		return Provider{}, ProviderCatalogEntry{}, catalogSource, NewHTTPError(
			http.StatusBadRequest,
			"provider_adapter_missing",
			fmt.Sprintf("Provider adapter type %q is not registered", provider.Type),
		)
	}
	if provider.Priority == 0 {
		provider.Priority = 10
	}
	provider.BaseURL = normalizeProviderBaseURL(provider.ID, provider.BaseURL)
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	if catalog.ID != "" {
		provider.Options["catalog_id"] = catalog.ID
		provider.Options["catalog_source"] = catalogSource
		if catalog.DocURL != "" {
			provider.Options["doc_url"] = catalog.DocURL
		}
	}
	if strings.TrimSpace(req.ModelCategory) != "" {
		provider.Options["model_category"] = strings.TrimSpace(req.ModelCategory)
	}
	options, err := applyClaudeCodeAttributionPolicy(provider.Options, req.ClaudeCodeAttributionPolicy)
	if err != nil {
		return Provider{}, ProviderCatalogEntry{}, catalogSource, err
	}
	if err := validateClaudeCodeAttributionOptions(options); err != nil {
		return Provider{}, ProviderCatalogEntry{}, catalogSource, err
	}
	provider.Options = options
	return provider, catalog, catalogSource, nil
}

func (s *Server) importSelectedProviderCatalogModels(providerID string, catalog ProviderCatalogEntry, selectedModels []string) int {
	selected := map[string]bool{}
	for _, modelID := range selectedModels {
		if modelID = strings.TrimSpace(modelID); modelID != "" {
			selected[modelID] = true
		}
	}
	if len(selected) == 0 {
		return 0
	}
	imported := 0
	for _, model := range catalog.Models {
		if !selected[model.ID] {
			continue
		}
		s.store.AddProviderModel(providerModelFromCatalog(providerID, model))
		imported++
	}
	return imported
}

func (s *Server) customProviderCatalogFromStandardModels(category string) ProviderCatalogEntry {
	models := []ProviderCatalogModel{}
	normalizedCategory := standardModelCategory(category)
	for _, model := range s.store.ListModels() {
		modelCategory := standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.Name, model.Name)))
		if normalizedCategory != "" && normalizedCategory != "all" && modelCategory != normalizedCategory {
			continue
		}
		models = append(models, ProviderCatalogModel{
			ID:                     model.Name,
			Name:                   model.Name,
			DisplayName:            model.Name,
			CanonicalName:          model.Name,
			Category:               modelCategory,
			Family:                 model.Family,
			Type:                   model.Modality,
			ContextWindow:          model.ContextWindow,
			InputPriceUSDPer1M:     model.InputPriceUSDPer1M,
			CacheReadPriceUSDPer1M: model.CacheReadPriceUSDPer1M,
			OutputPriceUSDPer1M:    model.OutputPriceUSDPer1M,
			InputModalities:        append([]string(nil), model.InputModalities...),
			OutputModalities:       append([]string(nil), model.OutputModalities...),
			Capabilities:           append([]string(nil), model.Capabilities...),
			SupportedParameters:    append([]string(nil), model.SupportedParameters...),
			Metadata:               map[string]string{"source": "tokenhub-standard-catalog"},
		})
	}
	categories, categoryCounts := catalogCategorySummary(models)
	if len(models) == 0 {
		entry := customProviderCatalogEntry()
		entry.Categories = []string{firstNonEmpty(normalizedCategory, "custom")}
		entry.CategoryCounts = map[string]int{firstNonEmpty(normalizedCategory, "custom"): 0}
		entry.Models = nil
		entry.ModelsCount = 0
		return entry
	}
	entry := customProviderCatalogEntry()
	entry.Categories = categories
	entry.CategoryCounts = categoryCounts
	entry.Models = models
	entry.ModelsCount = len(models)
	return entry
}

func customProviderCatalogFromModels(input []ProviderCatalogModel, category string) ProviderCatalogEntry {
	normalizedCategory := strings.TrimSpace(category)
	if normalizedCategory != "" {
		normalizedCategory = standardModelCategory(normalizedCategory)
	}
	models := make([]ProviderCatalogModel, 0, len(input))
	seen := map[string]bool{}
	for _, model := range input {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		model.Name = firstNonEmpty(strings.TrimSpace(model.Name), model.ID)
		model.DisplayName = firstNonEmpty(strings.TrimSpace(model.DisplayName), model.Name)
		model.CanonicalName = firstNonEmpty(strings.TrimSpace(model.CanonicalName), canonicalModelName(model.ID, model.DisplayName))
		model.Category = standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(model.ID, model.DisplayName)))
		if normalizedCategory != "" && normalizedCategory != "all" && model.Category != normalizedCategory {
			continue
		}
		model.Family = firstNonEmpty(model.Family, inferModelFamily(model.ID))
		model.Type = firstNonEmpty(model.Type, normalizeModelModality(model.ID))
		if model.Metadata == nil {
			model.Metadata = map[string]string{}
		}
		if model.Metadata["source"] == "" {
			model.Metadata["source"] = "custom-upstream"
		}
		models = append(models, model)
	}
	sort.SliceStable(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
	entry := customProviderCatalogEntry()
	entry.Source = "custom-upstream"
	entry.Models = models
	entry.ModelsCount = len(models)
	entry.Categories, entry.CategoryCounts = catalogCategorySummary(models)
	return entry
}

func routePriorityByModel(routes []ModelRoute) map[string]int {
	priorities := map[string]int{}
	for _, route := range routes {
		modelName := strings.TrimSpace(route.ModelName)
		if modelName == "" {
			continue
		}
		if route.Priority > priorities[modelName] {
			priorities[modelName] = route.Priority
		}
	}
	return priorities
}

func takeNextRoutePriority(priorities map[string]int, modelName string) int {
	modelName = strings.TrimSpace(modelName)
	next := priorities[modelName] + 1
	if next <= 0 {
		next = 1
	}
	priorities[modelName] = next
	return next
}

func normalizeModelLookupName(value string) string {
	return canonicalModelName(value, value)
}

func (s *Server) handleAdminProviderNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/admin/providers/"), "/")
	if len(parts) == 1 && parts[0] == "test-connection" {
		if r.Method != http.MethodPost {
			writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
			return
		}
		var req ProviderCreateRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
			return
		}
		if strings.TrimSpace(req.BaseURL) == "" {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "provider_base_url_required", "Base URL is required to test the connection"))
			return
		}
		if strings.TrimSpace(req.APIKey) == "" {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "provider_api_key_required", "API key is required to test the connection"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		startedAt := time.Now()
		catalog, err := CustomProviderCatalogFromUpstream(ctx, http.DefaultClient, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"healthy":      true,
			"latency_ms":   time.Since(startedAt).Milliseconds(),
			"models_count": catalog.ModelsCount,
		})
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var req ProviderCreateRequest
			if err := decodeJSON(r, &req); err != nil {
				if costErr := providerModelCostDecodeError(err); costErr != nil {
					writeError(w, r, costErr)
					return
				}
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			if err := validateProviderRouteCreation(req); err != nil {
				writeError(w, r, err)
				return
			}
			current, ok := s.store.GetProvider(parts[0])
			if !ok {
				writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found"))
				return
			}
			mergeProviderPatchRequest(&req, current)
			provider, catalog, catalogSource, err := s.providerFromCreateRequest(r.Context(), req)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if err := validateSelectedProviderModelCosts(catalog, req.SelectedModels); err != nil {
				writeError(w, r, err)
				return
			}
			provider.ID = parts[0]
			updated, err := s.store.UpdateProvider(parts[0], provider)
			if err != nil {
				writeError(w, r, err)
				return
			}
			result := ProviderCreateResult{
				Provider:      updated,
				CatalogSource: catalogSource,
			}
			result.ImportedModels = s.importSelectedProviderCatalogModels(updated.ID, catalog, req.SelectedModels)
			s.recordAdminAudit(r, user, "update", "provider", parts[0], "", result)
			writeJSON(w, http.StatusOK, result)
		case http.MethodDelete:
			if err := s.store.DeleteProvider(parts[0]); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "provider", parts[0], "", nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	if len(parts) != 2 || (parts[1] != "health" && parts[1] != "test" && parts[1] != "refresh-token") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	if parts[1] == "test" {
		result, err := s.integrations.TestProvider(r.Context(), parts[0])
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "test", "provider", parts[0], "", result)
		writeJSON(w, http.StatusOK, result)
		return
	}
	var req struct {
		Healthy bool `json:"healthy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	provider, err := s.store.SetProviderHealth(parts[0], req.Healthy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "health", "provider", parts[0], "", provider)
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) handleAdminProviderResources(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListProviderResources()})
	case http.MethodPost:
		var req ProviderResource
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if req.ProviderID == "" || req.Name == "" {
			writeError(w, r, NewHTTPError(400, "invalid_provider_resource", "provider_id and name are required"))
			return
		}
		resource, err := s.store.AddProviderResource(req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "provider_resource", resource.ID, "", resource)
		writeJSON(w, http.StatusCreated, resource)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func mergeProviderPatchRequest(req *ProviderCreateRequest, current Provider) {
	if req.Name == "" {
		req.Name = current.Name
	}
	if req.Type == "" {
		req.Type = current.Type
	}
	if req.BaseURL == "" {
		req.BaseURL = current.BaseURL
	}
	if req.Status == "" {
		req.Status = current.Status
	}
	if req.Healthy == nil {
		healthy := current.Healthy
		req.Healthy = &healthy
	}
	if req.Priority == 0 {
		req.Priority = current.Priority
	}
	if req.Headers == nil {
		req.Headers = current.Headers
	}
	if req.Options == nil {
		req.Options = current.Options
	}
}

func (s *Server) handleAdminProviderResourceNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	parts := splitNestedEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/provider-resources/", providerResourceActions)
	if len(parts) == 1 && parts[0] == "bulk" {
		s.handleAdminProviderResourceBulk(w, r, user)
		return
	}
	if len(parts) == 1 && parts[0] == "import" {
		s.handleAdminProviderResourceImport(w, r, user)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var req ProviderResource
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			resource, err := s.store.UpdateProviderResource(parts[0], req)
			if err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "update", "provider_resource", parts[0], "", resource)
			writeJSON(w, http.StatusOK, resource)
		case http.MethodDelete:
			if err := s.store.DeleteProviderResource(parts[0]); err != nil {
				writeError(w, r, err)
				return
			}
			s.recordAdminAudit(r, user, "delete", "provider_resource", parts[0], "", nil)
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		}
		return
	}
	// splitNestedAdminPath guarantees parts[1] is a known action here.
	if len(parts) != 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if parts[1] == "quota/reset-credits" {
		s.handleAdminOpenAIAccountQuotaResetCredits(w, r, user, parts[0])
		return
	}
	if parts[1] == "quota/reset" {
		s.handleAdminOpenAIAccountQuotaReset(w, r, user, parts[0])
		return
	}
	if parts[1] == "quota" {
		if r.Method != http.MethodGet {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		quota, err := s.queryOpenAIAccountQuotaCached(r.Context(), parts[0], r.URL.Query().Get("refresh") == "true")
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "query_quota", "provider_resource", parts[0], "", quota)
		writeJSON(w, http.StatusOK, quota)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	if parts[1] == "test" {
		resource, resourceOK := s.providerResourceByID(parts[0])
		provider, providerOK := s.providerByID(resource.ProviderID)
		adapter, adapterErr := s.adapterRegistry.Resolve(provider.Type)
		_, usesStructuredProbe := adapter.(ProviderResourceProber)
		if resourceOK && providerOK && adapterErr == nil && usesStructuredProbe {
			var req codexSubscriptionTestRequest
			if err := decodeJSON(r, &req); err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
				return
			}
			startedAt := time.Now()
			rawResult, err := s.integrations.TestProviderResource(r.Context(), parts[0], &req)
			if err != nil {
				httpErr := AsHTTPError(err)
				s.recordAdminAuditWithStatus(r, user, "test", "provider_resource", parts[0], "failed", httpErr.Code, "", map[string]any{
					"healthy":          false,
					"model":            strings.TrimSpace(req.Model),
					"reasoning_effort": strings.ToLower(strings.TrimSpace(req.ReasoningEffort)),
					"speed":            strings.ToLower(strings.TrimSpace(req.Speed)),
					"latency_ms":       time.Since(startedAt).Milliseconds(),
					"error_code":       httpErr.Code,
				})
				writeError(w, r, err)
				return
			}
			result, ok := rawResult.(ProviderProbeResult)
			if !ok {
				writeError(w, r, NewHTTPError(http.StatusInternalServerError, "provider_probe_invalid_result", "Provider probe returned an invalid result"))
				return
			}
			s.recordAdminAudit(r, user, "test", "provider_resource", parts[0], "", map[string]any{
				"healthy":          true,
				"model":            result.Model,
				"reasoning_effort": result.ReasoningEffort,
				"speed":            result.Speed,
				"latency_ms":       result.LatencyMS,
				"usage":            result.Usage,
			})
			writeJSON(w, http.StatusOK, result)
			return
		}
		tested, err := s.integrations.TestProviderResource(r.Context(), parts[0], nil)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "test", "provider_resource", parts[0], "", tested)
		writeJSON(w, http.StatusOK, tested)
		return
	}
	if parts[1] == "refresh-token" {
		creds, err := s.store.RefreshProviderResourceCredentials(r.Context(), parts[0], true)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "refresh_token", "provider_resource", parts[0], "", providerAccountCredentialSummary(creds))
		writeJSON(w, http.StatusOK, map[string]any{"credential_summary": providerAccountCredentialSummary(creds)})
		return
	}
	var req struct {
		Healthy bool `json:"healthy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	resource, err := s.store.SetProviderResourceHealth(parts[0], req.Healthy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "health", "provider_resource", parts[0], "", resource)
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) handleAdminProviderResourceBulk(w http.ResponseWriter, r *http.Request, user AdminUser) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	result, err := s.store.BulkOperateProviderResources(req.Action, req.IDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "bulk_"+req.Action, "provider_resource", strings.Join(req.IDs, ","), "", result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAdminProviderResourceImport(w http.ResponseWriter, r *http.Request, user AdminUser) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		Resources []ProviderResource `json:"resources"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
		return
	}
	result, err := s.store.ImportProviderResources(req.Resources)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "import", "provider_resource", "", "", result)
	status := http.StatusCreated
	if result.Failed > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func (s *Server) handleAdminModels(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authorizeAdminUser(w, r)
	if !ok {
		return
	}
	if !canAdmin(user.Role, "model", r.Method) {
		writeError(w, r, NewHTTPError(403, "admin_forbidden", "Admin role is not allowed to perform this action"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.accessibleModelsForAdminUser(user)})
	case http.MethodPost:
		var req struct {
			Model
			Routes []ModelRoute `json:"routes"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		req.Model.Name = strings.TrimSpace(req.Model.Name)
		if req.Model.Name == "" {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_model", "name is required"))
			return
		}
		priorities := routePriorityByModel(s.store.ListRoutes())
		seenRoutes := existingProviderModelRouteSet(s.store.ListRoutes())
		preparedRoutes := make([]ModelRoute, 0, len(req.Routes))
		for _, route := range req.Routes {
			route.ModelName = req.Model.Name
			route.ProviderID = strings.TrimSpace(route.ProviderID)
			route.ProviderModel = strings.TrimSpace(route.ProviderModel)
			if route.ProviderID == "" || route.ProviderModel == "" {
				writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_route", "provider_id and provider_model are required"))
				return
			}
			if err := s.validateRouteAdapter(route); err != nil {
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
		req.Model = withExternalModelRole(req.Model)
		model, err := s.store.CreateModelWithRoutes(req.Model, preparedRoutes)
		if err != nil {
			writeError(w, r, NewHTTPError(http.StatusInternalServerError, "model_create_failed", "Failed to create model and initial routes"))
			return
		}
		s.recordAdminAudit(r, user, "create", "model", model.Name, "", model)
		writeJSON(w, http.StatusCreated, model)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminModelsRestoreDefaults(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "model", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	catalogFile := strings.TrimSpace(s.config.ModelCatalogFile)
	if catalogFile == "" {
		catalogFile = defaultModelCatalogFile()
	}
	models, err := defaultModelCatalog(catalogFile)
	if err != nil {
		writeError(w, r, NewHTTPError(500, "model_catalog_restore_failed", err.Error()))
		return
	}
	for _, model := range models {
		s.store.AddModel(model)
	}
	s.recordAdminAudit(r, user, "restore_defaults", "model", "model_catalog", "", map[string]any{
		"catalog_file": catalogFile,
		"models":       len(models),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"restored": len(models),
		"data":     s.accessibleModelsForAdminUser(user),
	})
}

func (s *Server) handleAdminModelItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "model", r.Method)
	if !ok {
		return
	}
	modelName, ok := adminModelNameFromPath(r)
	if !ok {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req Model
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		model, err := s.store.UpdateModel(modelName, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "model", modelName, "", model)
		writeJSON(w, http.StatusOK, model)
	case http.MethodDelete:
		if err := s.store.DeleteModel(modelName); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "model", modelName, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func adminModelNameFromPath(r *http.Request) (string, bool) {
	const prefix = "/api/admin/models/"
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), prefix)
	escaped = strings.Trim(escaped, "/")
	if escaped == "" || strings.Contains(escaped, "/") {
		return "", false
	}
	modelName, err := url.PathUnescape(escaped)
	if err != nil {
		return "", false
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", false
	}
	return modelName, true
}

func (s *Server) handleAdminModelRoutingPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	modelName, ok := adminModelRoutingPolicyNameFromPath(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	var policy ModelRoutePolicy
	if err := decodeJSON(r, &policy); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
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
	s.recordAdminAudit(r, user, "update", "model_routing_policy", modelName, "", map[string]any{
		"strategy": policy.Strategy,
		"routes":   routes,
	})
	writeJSON(w, http.StatusOK, map[string]any{"strategy": policy.Strategy, "data": routes})
}

func adminModelRoutingPolicyNameFromPath(r *http.Request) (string, bool) {
	const prefix = "/api/admin/model-routing-policies/"
	escaped := strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), prefix), "/")
	if escaped == "" || strings.Contains(escaped, "/") {
		return "", false
	}
	modelName, err := url.PathUnescape(escaped)
	if err != nil {
		return "", false
	}
	modelName = strings.TrimSpace(modelName)
	return modelName, modelName != ""
}

func (s *Server) handleAdminRoutes(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListRoutes()})
	case http.MethodPost:
		var req ModelRoute
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		if req.ModelName == "" || req.ProviderID == "" || req.ProviderModel == "" {
			writeError(w, r, NewHTTPError(400, "invalid_route", "model_name, provider_id and provider_model are required"))
			return
		}
		if req.Priority <= 0 {
			req.Priority = takeNextRoutePriority(routePriorityByModel(s.store.ListRoutes()), req.ModelName)
		}
		if err := s.validateRouteAdapter(req); err != nil {
			writeError(w, r, err)
			return
		}
		if err := s.validateImportedProviderModel(req); err != nil {
			writeError(w, r, err)
			return
		}
		if modelRouteMappingExists(req, s.store.ListRoutes(), "") {
			writeError(w, r, NewHTTPError(http.StatusConflict, "model_route_conflict", "This external model is already mapped to the selected provider model"))
			return
		}
		if err := s.markExternalModel(req.ModelName); err != nil {
			writeError(w, r, err)
			return
		}
		route := s.store.AddRoute(req)
		s.recordAdminAudit(r, user, "create", "routing_rule", route.ID, "", route)
		writeJSON(w, http.StatusCreated, route)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminRouteItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	parts := splitNestedEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/routing-rules/", routingRuleActions)
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	routeID := parts[0]
	if len(parts) == 2 {
		// splitNestedAdminPath guarantees parts[1] == "explain" here.
		if r.Method != http.MethodGet {
			writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
			return
		}
		modelName := r.URL.Query().Get("model")
		if modelName == "" {
			writeError(w, r, NewHTTPError(400, "missing_model", "model query is required"))
			return
		}
		routes, err := s.store.SelectRouteCandidates(modelName)
		if err != nil {
			writeError(w, r, err)
			return
		}
		call := CallContext{RequestID: NewID("exp"), Project: Project{ID: r.URL.Query().Get("project_id")}, Key: APIKey{ID: r.URL.Query().Get("api_key_id")}}
		planned := s.planRouteOrder(call, routes)
		steps := make([]RouteExplainStep, 0, len(planned))
		for _, route := range planned {
			steps = append(steps, RouteExplainStep{
				RouteID:          route.Route.ID,
				ProviderID:       route.Provider.ID,
				ResourceID:       routeResourceID(route),
				ProviderModel:    route.ProviderModel,
				Priority:         route.Route.Priority,
				ResourcePriority: routeResourcePriority(route),
				Weight:           routeWeight(route.Route),
				QualityScore:     routeQualityScore(route.Route),
				CostScore:        routeCostScore(route.Route),
				Strategy:         routeStrategy(route.Route),
				ProjectScope:     routeProjectScope(route.Route),
				ProjectIDs:       route.Route.ProjectIDs,
				EffectiveWeight:  routeEffectiveWeight(route),
				Samples:          route.Runtime.Samples,
				SuccessRate:      route.Runtime.SuccessRate,
				LatencyMS:        route.Runtime.LatencyMS,
				Status:           "candidate",
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": steps})
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req ModelRoute
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_request", err.Error()))
			return
		}
		current, found := modelRouteByID(s.store.ListRoutes(), routeID)
		if !found {
			writeError(w, r, NewHTTPError(http.StatusNotFound, "route_not_found", "Route not found"))
			return
		}
		candidate := mergedModelRoute(current, req)
		if err := s.validateRouteAdapter(candidate); err != nil {
			writeError(w, r, err)
			return
		}
		if err := s.validateImportedProviderModel(candidate); err != nil {
			writeError(w, r, err)
			return
		}
		if modelRouteMappingExists(candidate, s.store.ListRoutes(), routeID) {
			writeError(w, r, NewHTTPError(http.StatusConflict, "model_route_conflict", "This external model is already mapped to the selected provider model"))
			return
		}
		if err := s.markExternalModel(candidate.ModelName); err != nil {
			writeError(w, r, err)
			return
		}
		route, err := s.store.UpdateRoute(routeID, req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "routing_rule", routeID, "", route)
		writeJSON(w, http.StatusOK, route)
	case http.MethodDelete:
		if err := s.store.DeleteRoute(routeID); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "routing_rule", routeID, "", nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) validateRouteAdapter(route ModelRoute) error {
	if err := s.validateRoutePolicy(route); err != nil {
		return err
	}
	provider, ok := s.providerByID(route.ProviderID)
	if !ok {
		return NewHTTPError(http.StatusBadRequest, "route_provider_not_found", "Route provider does not exist")
	}
	if _, ok := s.adapterRegistry.Describe(provider.Type); !ok {
		return NewHTTPError(http.StatusBadRequest, "provider_adapter_missing", "Route provider adapter is not registered")
	}
	if strings.TrimSpace(route.ProviderResourceID) == "" {
		return nil
	}
	resource, ok := s.providerResourceByID(route.ProviderResourceID)
	if !ok || resource.ProviderID != provider.ID {
		return NewHTTPError(http.StatusBadRequest, "route_resource_mismatch", "Route resource must belong to the selected Provider")
	}
	return nil
}

func (s *Server) validateRoutePolicy(route ModelRoute) error {
	switch routeStrategy(route) {
	case RouteStrategyBalanced, RouteStrategyAdaptive, RouteStrategyCost, RouteStrategyQuality, RouteStrategyPriorityWeighted, RouteStrategyPriorityOnly:
	default:
		return NewHTTPError(http.StatusBadRequest, "invalid_route_strategy", "Unsupported route strategy")
	}
	scope := strings.ToLower(strings.TrimSpace(route.ProjectScope))
	switch scope {
	case "", RouteProjectScopeAll:
		return nil
	case RouteProjectScopeInclude, RouteProjectScopeExclude:
	default:
		return NewHTTPError(http.StatusBadRequest, "invalid_route_project_scope", "Unsupported route project scope")
	}
	projectIDs := uniqueStrings(route.ProjectIDs)
	if len(projectIDs) == 0 {
		return NewHTTPError(http.StatusBadRequest, "route_projects_required", "Project-scoped routes require at least one project")
	}
	for _, projectID := range projectIDs {
		if _, ok := s.store.GetProject(projectID); !ok {
			return NewHTTPError(http.StatusBadRequest, "route_project_not_found", "Route project does not exist")
		}
	}
	return nil
}

func (s *Server) validateImportedProviderModel(route ModelRoute) error {
	providerID := strings.TrimSpace(route.ProviderID)
	upstreamModel := strings.TrimSpace(route.ProviderModel)
	for _, model := range s.store.ListProviderModels() {
		if model.ProviderID == providerID && model.UpstreamModel == upstreamModel {
			return nil
		}
	}
	return NewHTTPError(http.StatusConflict, "provider_model_not_imported", "Import the upstream model for this Provider before creating a route")
}

func modelRouteMappingExists(candidate ModelRoute, routes []ModelRoute, excludeID string) bool {
	for _, route := range routes {
		if route.ID == excludeID {
			continue
		}
		if strings.TrimSpace(route.ModelName) == strings.TrimSpace(candidate.ModelName) &&
			strings.TrimSpace(route.ProviderID) == strings.TrimSpace(candidate.ProviderID) &&
			strings.TrimSpace(route.ProviderModel) == strings.TrimSpace(candidate.ProviderModel) {
			return true
		}
	}
	return false
}

func modelRouteByID(routes []ModelRoute, routeID string) (ModelRoute, bool) {
	for _, route := range routes {
		if route.ID == routeID {
			return route, true
		}
	}
	return ModelRoute{}, false
}

func mergedModelRoute(current ModelRoute, patch ModelRoute) ModelRoute {
	if patch.ModelName != "" {
		current.ModelName = patch.ModelName
	}
	if patch.ProviderID != "" {
		current.ProviderID = patch.ProviderID
	}
	current.ProviderResourceID = patch.ProviderResourceID
	current.ResourceGroup = patch.ResourceGroup
	if patch.ProviderModel != "" {
		current.ProviderModel = patch.ProviderModel
	}
	if patch.Strategy != "" {
		current.Strategy = patch.Strategy
	}
	if patch.ProjectScope != "" || patch.ProjectIDs != nil {
		current.ProjectScope = patch.ProjectScope
		current.ProjectIDs = patch.ProjectIDs
	}
	if patch.Tags != nil {
		current.Tags = patch.Tags
	}
	return current
}
