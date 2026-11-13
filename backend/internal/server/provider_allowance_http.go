package server

import (
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"time"

	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

func (s *Server) registerProviderAllowanceRoutes() {
	s.registerPublicSingleMethodRoute(http.MethodGet, "/v1/allowance", s.handleProjectAllowanceGet, jsonMethodNotAllowed(http.MethodGet))
	for _, action := range []string{"history", "events"} {
		s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-resources/{resource_id}/allowance/"+action, s.handleProviderAllowanceRead, s.adminMethodNotAllowed("provider", http.MethodGet))
	}
	s.registerMethodRoutes("/api/admin/provider-resources/{resource_id}/allowance/config", func(methods string) http.HandlerFunc { return s.adminMethodNotAllowed("provider", methods) }, methodRoute{Method: http.MethodGet, Handler: s.handleProviderAllowanceRead}, methodRoute{Method: http.MethodPut, Handler: s.handleProviderAllowanceConfigPut})
	s.registerSingleMethodRoute(http.MethodPost, "/api/admin/provider-resources/{resource_id}/allowance/refresh", s.handleProviderAllowanceRefreshPost, s.adminMethodNotAllowed("provider", http.MethodPost))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-allowances", s.handleProviderAllowancesGet, s.adminMethodNotAllowed("provider", http.MethodGet))
	s.registerSingleMethodRoute(http.MethodGet, "/api/admin/provider-allowances/presets", s.handleProviderAllowancePresetsGet, s.adminMethodNotAllowed("provider", http.MethodGet))
}

func (s *Server) handleProviderAllowancePresetsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	writeJSON(w, 200, quotasource.Presets())
}

func (s *Server) allowanceHTTPStore(w http.ResponseWriter, r *http.Request) (*GormStore, string, bool) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return nil, "", false
	}
	id := r.PathValue("resource_id")
	if id != "" {
		if _, ok := s.store.GetProviderResource(id); !ok {
			writeError(w, r, NewHTTPError(404, "provider_resource_not_found", "Provider resource not found"))
			return nil, "", false
		}
	}
	store, ok := s.store.(*GormStore)
	if !ok {
		writeError(w, r, NewHTTPError(503, "allowance_store_unavailable", "Allowance storage is unavailable"))
		return nil, "", false
	}
	return store, id, true
}

type providerAllowanceEntry struct {
	ResourceID   string                  `json:"resource_id"`
	ResourceName string                  `json:"resource_name"`
	ProviderID   string                  `json:"provider_id"`
	ProviderName string                  `json:"provider_name"`
	ProviderType string                  `json:"provider_type"`
	Status       allowance.Status        `json:"status"`
	Allowance    allowance.Snapshot      `json:"allowance"`
	Config       providerAllowanceConfig `json:"config"`
	Estimated    bool                    `json:"estimated"`
}

func (s *Server) handleProviderAllowancesGet(w http.ResponseWriter, r *http.Request) {
	store, _, ok := s.allowanceHTTPStore(w, r)
	if !ok {
		return
	}
	rows := []providerAllowanceEntry{}
	for _, resource := range s.store.ListProviderResources() {
		provider, exists := s.store.GetProvider(resource.ProviderID)
		if !exists {
			continue
		}
		config, err := store.providerAllowanceConfig(r.Context(), resource.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		a := allowance.Snapshot{Version: allowance.Version, Status: allowance.Unknown}
		if observation, found := s.store.GetProviderResourceObservation(resource.ID); found {
			at := time.Time{}
			if observation.QuotaFetchedAt != nil {
				at = *observation.QuotaFetchedAt
			}
			a = allowance.Read([]byte(observation.QuotaSnapshot), at)
		}
		a, err = store.withWalletObservations(r.Context(), resource.ID, a, config.Source, time.Now().UTC())
		if err != nil {
			writeError(w, r, err)
			return
		}
		a, err = store.withManualAllowance(r.Context(), resource.ID, a, config.Policy.Wallet, time.Now().UTC())
		if err != nil {
			writeError(w, r, err)
			return
		}
		rows = append(rows, providerAllowanceEntry{ResourceID: resource.ID, ResourceName: resource.Name, ProviderID: provider.ID, ProviderName: provider.Name, ProviderType: provider.Type, Allowance: a, Status: a.For("", time.Now().UTC(), accountAllowanceMaxAge).Status, Config: publicProviderAllowanceConfig(config), Estimated: config.Policy.Wallet.Enabled || a.Source == "manual"})
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

func (s *Server) handleProviderAllowanceRead(w http.ResponseWriter, r *http.Request) {
	store, id, ok := s.allowanceHTTPStore(w, r)
	if !ok {
		return
	}
	switch path.Base(r.URL.Path) {
	case "config":
		c, err := store.providerAllowanceConfig(r.Context(), id)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, 200, publicProviderAllowanceConfig(c))
	case "history":
		since := time.Now().UTC().Add(-7 * 24 * time.Hour)
		if value := r.URL.Query().Get("since"); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(w, r, NewHTTPError(400, "invalid_history_since", "History start must be RFC3339"))
				return
			}
			since = parsed
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		points, err := store.providerAllowanceHistory(r.Context(), id, since, limit)
		if err != nil {
			writeError(w, r, err)
			return
		}
		rows := []map[string]any{}
		for _, point := range points {
			var a allowance.Snapshot
			if json.Unmarshal([]byte(point.Snapshot), &a) == nil {
				rows = append(rows, map[string]any{"id": point.ID, "fetched_at": point.FetchedAt, "allowance": a})
			}
		}
		writeJSON(w, 200, map[string]any{"data": rows})
	case "events":
		var rows []providerAllowanceEvent
		if err := store.db.WithContext(r.Context()).Where("resource_id = ? AND kind <> ?", id, "reset_intent").Order("created_at desc").Limit(200).Find(&rows).Error; err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"data": rows})
	}
}

func (s *Server) handleProviderAllowanceConfigPut(w http.ResponseWriter, r *http.Request) {
	store, id, ok := s.allowanceHTTPStore(w, r)
	if !ok {
		return
	}
	var req providerAllowanceConfig
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Source.Enabled {
		resource, _ := s.store.GetProviderResource(id)
		provider, _ := s.store.GetProvider(resource.ProviderID)
		if _, err := quotasource.Resolve(req.Source, firstNonEmpty(resource.BaseURL, provider.BaseURL)); err != nil {
			writeError(w, r, NewHTTPError(400, "invalid_allowance_source", err.Error()))
			return
		}
	}
	saved, err := store.saveProviderAllowanceConfig(r.Context(), id, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	user, _ := s.requireAdmin(w, r, "provider", r.Method)
	s.recordAdminAudit(r, user, "configure_allowance", "provider_resource", id, "", map[string]any{"revision": saved.Revision, "source_enabled": saved.Source.Enabled, "preset": saved.Source.Preset})
	writeJSON(w, 200, saved)
}

func (s *Server) handleProviderAllowanceRefreshPost(w http.ResponseWriter, r *http.Request) {
	_, id, ok := s.allowanceHTTPStore(w, r)
	if !ok {
		return
	}
	user, _ := s.requireAdmin(w, r, "provider", r.Method)
	result, err := s.executeProviderResourceQuotaAction(r.Context(), user, id, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resource, _ := s.store.GetProviderResource(id)
	if store, ok := s.store.(*GormStore); ok {
		cfg, err := store.providerAllowanceConfig(r.Context(), id)
		if err == nil {
			s.refreshAllowanceEntitlements(r.Context(), resource, cfg)
		}
	}
	writeJSON(w, 200, result.Data)
}
