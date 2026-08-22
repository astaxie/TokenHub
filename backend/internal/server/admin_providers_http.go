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

func (s *Server) handleAdminProvidersGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListProviders()})
}

func (s *Server) handleAdminProvidersPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	var req ProviderCreateRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if costErr := providerModelCostDecodeError(err); costErr != nil {
			writeError(w, r, costErr)
			return
		}
		writeError(w, r, err)
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
	if err := validateProviderHeaderConfig(&provider); err != nil {
		writeError(w, r, err)
		return
	}
	created := s.store.AddProvider(provider)
	result := ProviderCreateResult{
		Provider:      created,
		CatalogSource: catalogSource,
	}
	result.ImportedModels = s.importSelectedProviderCatalogModels(created.ID, catalog, req.SelectedModels)
	s.recordAdminAudit(r, user, "create", "provider", created.ID, "", auditProviderCreateResult(result))
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleAdminProviderMonitoring(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.providerMonitoringSnapshots(r.Context(), "")})
}

func (s *Server) handleAdminProviderCatalogGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
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
	rawID := strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/api/admin/provider-catalog/"), "/")
	if rawID == "" || strings.Contains(rawID, "/") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	// The escaped path preserves %2F inside a single segment; decode it so a
	// catalog ID containing "/" is looked up under its real value. Malformed
	// escapes are rejected rather than looked up under the raw text.
	id, err := url.PathUnescape(rawID)
	if err != nil || id == "" {
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
			if decodeErr := s.decodeJSON(w, r, &credentials); decodeErr != nil {
				writeError(w, r, decodeErr)
				return
			}
			entry, err = s.codexSubscription.ModelsWithCredentials(r.Context(), credentials)
		default:
			jsonMethodNotAllowed(http.MethodGet+", "+http.MethodPost)(w, r)
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
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		catalogRequests := []ProviderCreateRequest{req}
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
				if req.Headers == nil {
					req.Headers = provider.Headers
					req.SensitiveHeaders = provider.SensitiveHeaders
				} else {
					req.Headers = retainStoredSensitiveProviderHeaders(req.Headers, req.SensitiveHeaders, provider.Headers, provider.SensitiveHeaders)
				}
				req.Options = mergedStringMap(provider.Options, req.Options)
				catalogRequests = nil
				for _, listedResource := range s.store.ListProviderResources() {
					if listedResource.ProviderID != providerID || listedResource.Status != StatusActive {
						continue
					}
					resource, ok := s.store.GetProviderResource(listedResource.ID)
					if !ok {
						continue
					}
					effective := effectiveProviderResourceConfig(Provider{Type: req.Type, BaseURL: req.BaseURL, APIKey: req.APIKey, Headers: req.Headers, SensitiveHeaders: req.SensitiveHeaders, Options: req.Options}, &resource)
					candidate := req
					candidate.BaseURL, candidate.APIKey, candidate.Headers, candidate.SensitiveHeaders, candidate.Options = effective.BaseURL, effective.APIKey, effective.Headers, effective.SensitiveHeaders, effective.Options
					catalogRequests = append(catalogRequests, candidate)
				}
				if len(catalogRequests) == 0 {
					catalogRequests = []ProviderCreateRequest{req}
				}
			}
		}
		var entry ProviderCatalogEntry
		var err error
		for _, candidate := range catalogRequests {
			entry, err = CustomProviderCatalogFromUpstream(r.Context(), s.upstreamClient, candidate)
			if err == nil {
				break
			}
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": entry, "source": entry.Source})
		return
	}
	if id == ProviderKronk && r.Method == http.MethodPost {
		var req ProviderCreateRequest
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		entry, err := s.discoverKronkCatalog(r.Context(), req)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": entry, "source": entry.Source})
		return
	}
	if r.Method != http.MethodGet {
		allowedMethods := http.MethodGet
		if id == "custom" || id == ProviderKronk {
			allowedMethods += ", " + http.MethodPost
		}
		jsonMethodNotAllowed(allowedMethods)(w, r)
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
	if catalogID == ProviderKronk && len(req.CustomModels) > 0 {
		catalog = customProviderCatalogFromModels(req.CustomModels, req.ModelCategory)
		catalog.ID = ProviderKronk
		catalog.Name = "Kronk"
		catalog.DisplayName = "Kronk"
		catalog.Type = ProviderKronk
		catalog.BaseURL = firstNonEmpty(strings.TrimSpace(req.BaseURL), kronkDefaultBaseURL)
		catalog.DocURL = kronkDocURL
		catalog.Source = "kronk-upstream"
		catalogSource = catalog.Source
	} else if catalogID == codexProviderCatalogID {
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
		ID:               id,
		Name:             firstNonEmpty(req.Name, catalog.DisplayName, catalog.Name),
		Type:             firstNonEmpty(req.Type, catalog.Type, ProviderOpenAICompatible),
		BaseURL:          firstNonEmpty(req.BaseURL, catalog.BaseURL),
		APIKey:           req.APIKey,
		ClearAPIKey:      req.ClearAPIKey,
		Status:           firstNonEmpty(req.Status, StatusActive),
		Healthy:          req.Healthy != nil && *req.Healthy,
		Priority:         req.Priority,
		Headers:          req.Headers,
		SensitiveHeaders: req.SensitiveHeaders,
		Options:          req.Options,
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
	// SSRF guard at the admin persistence boundary: admin create and update both
	// flow through here, so those untrusted entry points cannot save a base URL
	// with a literal IP in loopback, private, link-local or curated high-risk/
	// non-provider ranges. The operator
	// allowlist (TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS) and the explicit
	// loopback opt-in apply exactly as they do for upstream model discovery.
	if err := ValidateProviderUpstreamBaseURL(provider.BaseURL); err != nil {
		return Provider{}, ProviderCatalogEntry{}, catalogSource, err
	}
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	if err := configureAnthropicProviderAuth(&provider, req.AnthropicAuthType); err != nil {
		return Provider{}, ProviderCatalogEntry{}, catalogSource, err
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

type adminProviderItemHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

func (s *Server) handleAdminProviderItemRoute(w http.ResponseWriter, r *http.Request, serve adminProviderItemHandler) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	providerID := r.PathValue("provider_id")
	if providerID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	serve(w, r, user, providerID)
}

func (s *Server) handleAdminProviderTestConnectionPost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	s.serveAdminProviderTestConnection(w, r)
}

func (s *Server) handleAdminProviderPatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderItemRoute(w, r, s.serveAdminProviderPatch)
}

func (s *Server) handleAdminProviderDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderItemRoute(w, r, s.serveAdminProviderDelete)
}

func (s *Server) handleAdminProviderHealthPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderItemRoute(w, r, s.serveAdminProviderHealth)
}

func (s *Server) handleAdminProviderRefreshTokenPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderItemRoute(w, r, s.serveAdminProviderHealth)
}

func (s *Server) handleAdminProviderTestPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderItemRoute(w, r, s.serveAdminProviderTest)
}

func (s *Server) handleAdminProviderNested(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/providers/")
	if len(parts) == 1 && parts[0] == "monitoring" && !strings.HasSuffix(r.URL.EscapedPath(), "/") {
		jsonMethodNotAllowed(http.MethodGet)(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "test-connection" {
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminProviderTestConnection(w, r)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			s.serveAdminProviderPatch(w, r, user, parts[0])
		case http.MethodDelete:
			s.serveAdminProviderDelete(w, r, user, parts[0])
		default:
			jsonMethodNotAllowed(http.MethodPatch+", "+http.MethodDelete)(w, r)
		}
		return
	}
	if len(parts) != 2 || (parts[1] != "health" && parts[1] != "test" && parts[1] != "refresh-token") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	if parts[1] == "test" {
		s.serveAdminProviderTest(w, r, user, parts[0])
		return
	}
	// Provider-level refresh-token has historically shared the health update
	// behavior. Keep it on the compatibility subtree until its API contract is
	// clarified instead of presenting it as a newly migrated route.
	s.serveAdminProviderHealth(w, r, user, parts[0])
}

func (s *Server) serveAdminProviderTestConnection(w http.ResponseWriter, r *http.Request) {
	var req ProviderCreateRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(req.BaseURL) == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "provider_base_url_required", "Base URL is required to test the connection"))
		return
	}
	if strings.TrimSpace(req.APIKey) == "" && strings.TrimSpace(req.Type) != ProviderKronk {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "provider_api_key_required", "API key is required to test the connection"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	startedAt := time.Now()
	var catalog ProviderCatalogEntry
	var health *KronkHealthResult
	var err error
	if strings.TrimSpace(req.Type) == ProviderKronk {
		adapter, ok := resolveTypedAdapter[KronkAdapter](s.adapterRegistry, ProviderKronk)
		if !ok {
			writeError(w, r, NewHTTPError(http.StatusInternalServerError, "provider_adapter_missing", "Kronk adapter is unavailable"))
			return
		}
		provider := Provider{Name: req.Name, Type: ProviderKronk, BaseURL: req.BaseURL, APIKey: req.APIKey, Headers: req.Headers, SensitiveHeaders: req.SensitiveHeaders, Options: req.Options}
		result, healthErr := adapter.Health(ctx, provider)
		health, err = &result, healthErr
		if err == nil {
			catalog, err = KronkProviderCatalogFromUpstream(ctx, s.upstreamClient, req)
		}
	} else {
		catalog, err = CustomProviderCatalogFromUpstream(ctx, s.upstreamClient, req)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"healthy":      true,
		"latency_ms":   time.Since(startedAt).Milliseconds(),
		"models_count": catalog.ModelsCount,
		"health":       health,
	})
}

func (s *Server) serveAdminProviderPatch(w http.ResponseWriter, r *http.Request, user AdminUser, providerID string) {
	var req ProviderCreateRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		if costErr := providerModelCostDecodeError(err); costErr != nil {
			writeError(w, r, costErr)
			return
		}
		writeError(w, r, err)
		return
	}
	if err := validateProviderRouteCreation(req); err != nil {
		writeError(w, r, err)
		return
	}
	current, ok := s.store.GetProvider(providerID)
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
	provider.ID = providerID
	if err := validateProviderHeaderSupport(provider.Type, provider.Headers); err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.store.UpdateProvider(providerID, provider)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result := ProviderCreateResult{Provider: updated, CatalogSource: catalogSource}
	result.ImportedModels = s.importSelectedProviderCatalogModels(updated.ID, catalog, req.SelectedModels)
	s.recordAdminAudit(r, user, "update", "provider", providerID, "", auditProviderCreateResult(result))
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) serveAdminProviderDelete(w http.ResponseWriter, r *http.Request, user AdminUser, providerID string) {
	provider, found := s.store.GetProvider(providerID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found"))
		return
	}
	deleteProvider := func() error { return s.store.DeleteProvider(providerID) }
	var err error
	if provider.Type == ProviderOpenAICodex {
		err = s.store.RunClusterOperation(r.Context(), "codex-image-capability:"+providerID, func(context.Context) error {
			return deleteProvider()
		})
	} else {
		err = deleteProvider()
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "provider", providerID, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveAdminProviderTest(w http.ResponseWriter, r *http.Request, user AdminUser, providerID string) {
	result, err := s.integrations.TestProvider(r.Context(), providerID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	auditResult := result
	if testedProvider, ok := result.(Provider); ok {
		auditResult = auditProvider(testedProvider)
	} else if testedResource, ok := result.(ProviderResource); ok {
		auditResult = auditProviderResource(testedResource)
	}
	s.recordAdminAudit(r, user, "test", "provider", providerID, "", auditResult)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) serveAdminProviderHealth(w http.ResponseWriter, r *http.Request, user AdminUser, providerID string) {
	var req struct {
		Healthy bool `json:"healthy"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	provider, err := s.store.SetProviderHealth(providerID, req.Healthy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "health", "provider", providerID, "", auditProvider(provider))
	writeJSON(w, http.StatusOK, provider)
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
		req.SensitiveHeaders = current.SensitiveHeaders
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
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminProviderResourceBulk(w, r, user)
		return
	}
	if len(parts) == 1 && parts[0] == "import" {
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminProviderResourceImport(w, r, user)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			s.serveAdminProviderResourcePatch(w, r, user, parts[0])
		case http.MethodDelete:
			s.serveAdminProviderResourceDelete(w, r, user, parts[0])
		default:
			jsonMethodNotAllowed(http.MethodPatch+", "+http.MethodDelete)(w, r)
		}
		return
	}
	// splitNestedAdminPath guarantees parts[1] is a known action here.
	if len(parts) != 2 {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if parts[1] == "quota/reset-credits" {
		if r.Method != http.MethodGet {
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		s.serveAdminOpenAIAccountQuotaResetCredits(w, r, user, parts[0])
		return
	}
	if parts[1] == "quota/reset" {
		if r.Method != http.MethodPost {
			jsonMethodNotAllowed(http.MethodPost)(w, r)
			return
		}
		s.serveAdminOpenAIAccountQuotaReset(w, r, user, parts[0])
		return
	}
	if parts[1] == "quota" {
		if r.Method != http.MethodGet {
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		s.serveAdminOpenAIAccountQuota(w, r, user, parts[0])
		return
	}
	if r.Method != http.MethodPost {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	switch parts[1] {
	case "image-capability":
		s.handleAdminCodexImageCapability(w, r, user, parts[0])
	case "test":
		s.serveAdminProviderResourceTest(w, r, user, parts[0])
	case "refresh-token":
		s.serveAdminProviderResourceRefreshToken(w, r, user, parts[0])
	default:
		s.serveAdminProviderResourceHealth(w, r, user, parts[0])
	}
}

func (s *Server) serveAdminProviderResourceBulk(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := s.store.BulkOperateProviderResources(req.Action, req.IDs)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "bulk_"+req.Action, "provider_resource", strings.Join(req.IDs, ","), "", auditProviderResourceBulkResult(result))
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) serveAdminProviderResourceImport(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var req struct {
		Resources []ProviderResource `json:"resources"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := s.store.ImportProviderResources(req.Resources)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "import", "provider_resource", "", "", auditProviderResourceImportResult(result))
	status := http.StatusCreated
	if result.Failed > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

func (s *Server) serveAdminModelsGet(w http.ResponseWriter, user AdminUser) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.accessibleModelsForAdminUser(user)})
}

func (s *Server) serveAdminModelsPost(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var req struct {
		Model
		Routes []ModelRoute `json:"routes"`
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
	preparedRoutes := make([]ModelRoute, 0, len(req.Routes))
	for _, route := range req.Routes {
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
	req.Model = withExternalModelRole(req.Model)
	model, err := s.store.CreateModelWithRoutes(req.Model, preparedRoutes)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "model_create_failed", "Failed to create model and initial routes"))
		return
	}
	s.recordAdminAudit(r, user, "create", "model", model.Name, "", model)
	writeJSON(w, http.StatusCreated, model)
}

func (s *Server) handleAdminModelsRestoreDefaults(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "model", r.Method)
	if !ok {
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
	if !strings.HasSuffix(r.URL.EscapedPath(), "/") && modelName == "restore-defaults" {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.serveAdminModelPatch(w, r, user, modelName)
	case http.MethodDelete:
		s.serveAdminModelDelete(w, r, user, modelName)
	default:
		jsonMethodNotAllowed(http.MethodPatch+", "+http.MethodDelete)(w, r)
	}
}

func (s *Server) serveAdminModelPatch(w http.ResponseWriter, r *http.Request, user AdminUser, modelName string) {
	var req Model
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	model, err := s.store.UpdateModel(modelName, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "model", modelName, "", model)
	writeJSON(w, http.StatusOK, model)
}

func (s *Server) serveAdminModelDelete(w http.ResponseWriter, r *http.Request, user AdminUser, modelName string) {
	if err := s.store.DeleteModel(modelName); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "model", modelName, "", nil)
	w.WriteHeader(http.StatusNoContent)
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
		jsonMethodNotAllowed(http.MethodPatch)(w, r)
		return
	}
	modelName, ok := adminModelRoutingPolicyNameFromPath(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	s.serveAdminModelRoutingPolicyPatch(w, r, user, modelName)
}

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

func (s *Server) serveAdminRoutesGet(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListRoutes()})
}

func (s *Server) serveAdminRoutesPost(w http.ResponseWriter, r *http.Request, user AdminUser) {
	var req ModelRoute
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
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
	route, err := s.store.CreateRoute(req)
	if err != nil {
		// The route was not persisted, so the catalog must not be marked
		// as if it had been.
		writeError(w, r, err)
		return
	}
	if err := s.markExternalModel(req.ModelName); err != nil {
		// The route was created but the model directory could not be marked as
		// external. The next call to backfillExternalModelRolesFromRoutes will
		// reconcile the catalog, but surface the error so the caller knows the
		// operation was only partially successful.
		s.recordAdminAudit(r, user, "create", "routing_rule", route.ID, "", route)
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "routing_rule", route.ID, "", route)
	writeJSON(w, http.StatusCreated, route)
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
			jsonMethodNotAllowed(http.MethodGet)(w, r)
			return
		}
		s.serveAdminRouteExplain(w, r, user, routeID)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.serveAdminRoutePatch(w, r, user, routeID)
	case http.MethodDelete:
		s.serveAdminRouteDelete(w, r, user, routeID)
	default:
		jsonMethodNotAllowed(http.MethodPatch+", "+http.MethodDelete)(w, r)
	}
}

func (s *Server) serveAdminRouteExplain(w http.ResponseWriter, r *http.Request, _ AdminUser, _ string) {
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
}

func (s *Server) serveAdminRoutePatch(w http.ResponseWriter, r *http.Request, user AdminUser, routeID string) {
	var req ModelRoute
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
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
	route, err := s.store.UpdateRoute(routeID, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.markExternalModel(candidate.ModelName); err != nil {
		// The route was updated but the model directory could not be marked as
		// external. backfillExternalModelRolesFromRoutes reconciles the catalog
		// on startup, but surface the error so the caller knows the operation
		// was only partially successful.
		s.recordAdminAudit(r, user, "update", "routing_rule", routeID, "", route)
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "routing_rule", routeID, "", route)
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) serveAdminRouteDelete(w http.ResponseWriter, r *http.Request, user AdminUser, routeID string) {
	if err := s.store.DeleteRoute(routeID); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "routing_rule", routeID, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validateRouteAdapter(route ModelRoute) error {
	return s.validateRouteAdapterForModel(route, nil)
}

func (s *Server) validateRouteAdapterForModel(route ModelRoute, pendingModel *Model) error {
	if err := s.validateRoutePolicy(route); err != nil {
		return err
	}
	provider, ok := s.providerByID(route.ProviderID)
	if !ok {
		return NewHTTPError(http.StatusBadRequest, "route_provider_not_found", "Route provider does not exist")
	}
	descriptor, ok := s.adapterRegistry.Describe(provider.Type)
	if !ok {
		return NewHTTPError(http.StatusBadRequest, "provider_adapter_missing", "Route provider adapter is not registered")
	}
	if err := s.validateRouteModelProtocol(route.ModelName, pendingModel, provider.Type, descriptor); err != nil {
		return err
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

// validateRouteModelProtocol rejects only known catalog mismatches. Models
// without endpoint metadata remain valid so operators can route custom models.
func (s *Server) validateRouteModelProtocol(modelName string, pendingModel *Model, providerType string, descriptor AdapterDescriptor) error {
	var model Model
	found := pendingModel != nil && pendingModel.Name == modelName
	if found {
		model = *pendingModel
	} else {
		for _, candidate := range s.store.ListModels() {
			if candidate.Name == modelName {
				model, found = candidate, true
				break
			}
		}
	}
	if !found || model.Metadata == nil {
		return nil
	}
	endpoints := strings.Split(model.Metadata["endpoints"], ",")
	if len(endpoints) == 0 || strings.TrimSpace(model.Metadata["endpoints"]) == "" {
		return nil
	}
	compatible := routeProviderProtocols(providerType, descriptor)
	for _, endpoint := range endpoints {
		if compatible[strings.ToLower(strings.TrimSpace(endpoint))] {
			return nil
		}
	}
	return NewHTTPError(http.StatusBadRequest, "route_protocol_mismatch", "Model does not support the selected Provider protocol")
}

func routeProviderProtocols(providerType string, descriptor AdapterDescriptor) map[string]bool {
	if providerType == ProviderAnthropic {
		return map[string]bool{"anthropic": true}
	}
	if providerType == ProviderGemini {
		return map[string]bool{"gemini": true}
	}
	protocols := map[string]bool{}
	if adapterSupports(descriptor, AdapterCapabilityChat) {
		protocols["chat/completions"] = true
	}
	if adapterSupports(descriptor, AdapterCapabilityResponses) {
		protocols["responses"] = true
	}
	if adapterSupports(descriptor, AdapterCapabilityEmbeddings) {
		protocols["embeddings"] = true
	}
	return protocols
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
	if strings.TrimSpace(route.ModelName) == codexImageModelName {
		provider, ok := s.providerByID(providerID)
		if !ok || provider.Type != ProviderOpenAICodex {
			return NewHTTPError(http.StatusBadRequest, "codex_image_provider_required", "The Codex subscription image model must use an OpenAI Codex Provider")
		}
		if upstreamModel != codexImageUpstreamModel {
			return NewHTTPError(http.StatusBadRequest, "codex_image_upstream_model_invalid", "The Codex subscription image route must use gpt-image-2 as its upstream model")
		}
		status := strings.TrimSpace(route.Status)
		if (status == "" || status == StatusActive) && !codexImageRouteHasSupportedResource(route, s.store.ListProviderResources()) {
			return NewHTTPError(http.StatusConflict, "codex_image_capability_required", "Test image generation with an eligible Codex subscription account before activating this route")
		}
		return nil
	}
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
	if patch.Status != "" {
		current.Status = patch.Status
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
