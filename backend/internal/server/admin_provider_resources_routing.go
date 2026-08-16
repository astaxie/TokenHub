package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

type adminProviderResourceItemHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

func (s *Server) handleAdminProviderResourcesGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListProviderResources()})
}

func (s *Server) handleAdminProviderResourcesPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	var req ProviderResource
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.ProviderID == "" || req.Name == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_provider_resource", "provider_id and name are required"))
		return
	}
	provider, found := s.store.GetProvider(req.ProviderID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found"))
		return
	}
	if err := validateProviderHeaderSupport(provider.Type, req.Headers); err != nil {
		writeError(w, r, err)
		return
	}
	resource, err := s.store.AddProviderResource(req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "create", "provider_resource", resource.ID, "", auditProviderResource(resource))
	writeJSON(w, http.StatusCreated, resource)
}

func (s *Server) handleAdminProviderResourceBulkPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	s.serveAdminProviderResourceBulk(w, r, user)
}

func (s *Server) handleAdminProviderResourceImportPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	s.serveAdminProviderResourceImport(w, r, user)
}

func (s *Server) handleAdminProviderResourceItemRoute(w http.ResponseWriter, r *http.Request, serve adminProviderResourceItemHandler) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	resourceID := r.PathValue("resource_id")
	if resourceID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	serve(w, r, user, resourceID)
}

func (s *Server) handleAdminProviderResourcePatch(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceItemRoute(w, r, s.serveAdminProviderResourcePatch)
}

func (s *Server) handleAdminProviderResourceDelete(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceItemRoute(w, r, s.serveAdminProviderResourceDelete)
}

type adminProviderResourceActionHandler func(http.ResponseWriter, *http.Request, AdminUser, string)

func (s *Server) handleAdminProviderResourceActionRoute(w http.ResponseWriter, r *http.Request, serve adminProviderResourceActionHandler) {
	user, ok := s.requireAdmin(w, r, "provider", r.Method)
	if !ok {
		return
	}
	resourceID := r.PathValue("resource_id")
	if resourceID == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	serve(w, r, user, resourceID)
}

func (s *Server) handleAdminProviderResourceQuotaGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.serveAdminOpenAIAccountQuota)
}

func (s *Server) handleAdminProviderResourceQuotaResetCreditsGet(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.serveAdminOpenAIAccountQuotaResetCredits)
}

func (s *Server) handleAdminProviderResourceQuotaResetPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.serveAdminOpenAIAccountQuotaReset)
}

func (s *Server) handleAdminProviderResourceHealthPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.serveAdminProviderResourceHealth)
}

func (s *Server) handleAdminProviderResourceTestPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.serveAdminProviderResourceTest)
}

func (s *Server) handleAdminProviderResourceImageCapabilityPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.handleAdminCodexImageCapability)
}

func (s *Server) handleAdminProviderResourceRefreshTokenPost(w http.ResponseWriter, r *http.Request) {
	s.handleAdminProviderResourceActionRoute(w, r, s.serveAdminProviderResourceRefreshToken)
}

func (s *Server) serveAdminProviderResourcePatch(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	var req ProviderResource
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	current, found := s.store.GetProviderResource(resourceID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found"))
		return
	}
	providerID := firstNonEmpty(req.ProviderID, current.ProviderID)
	provider, found := s.store.GetProvider(providerID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found"))
		return
	}
	headers := req.Headers
	if headers == nil {
		headers = current.Headers
	}
	if err := validateProviderHeaderSupport(provider.Type, headers); err != nil {
		writeError(w, r, err)
		return
	}
	var resource ProviderResource
	updateResource := func() error {
		var updateErr error
		resource, updateErr = s.store.UpdateProviderResource(resourceID, req)
		return updateErr
	}
	var err error
	if current.ResourceType == ProviderResourceOpenAISubscription {
		err = s.store.RunClusterOperation(r.Context(), "codex-image-capability:"+current.ProviderID, func(context.Context) error {
			return updateResource()
		})
	} else {
		err = updateResource()
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "update", "provider_resource", resourceID, "", auditProviderResource(resource))
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) serveAdminProviderResourceDelete(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	deleteResource := func() error { return s.store.DeleteProviderResource(resourceID) }
	resource, found := s.store.GetProviderResource(resourceID)
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found"))
		return
	}
	var err error
	if resource.ResourceType == ProviderResourceOpenAISubscription {
		err = s.store.RunClusterOperation(r.Context(), "codex-image-capability:"+resource.ProviderID, func(context.Context) error {
			return deleteResource()
		})
	} else {
		err = deleteResource()
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "provider_resource", resourceID, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveAdminProviderResourceHealth(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	var req struct {
		Healthy bool `json:"healthy"`
	}
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	resource, err := s.store.SetProviderResourceHealth(resourceID, req.Healthy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "health", "provider_resource", resourceID, "", auditProviderResource(resource))
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) serveAdminProviderResourceTest(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	resource, resourceOK := s.providerResourceByID(resourceID)
	provider, providerOK := s.providerByID(resource.ProviderID)
	adapter, adapterErr := s.adapterRegistry.Resolve(provider.Type)
	_, usesStructuredProbe := adapter.(ProviderResourceProber)
	if resourceOK && providerOK && adapterErr == nil && usesStructuredProbe {
		var req codexSubscriptionTestRequest
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		startedAt := time.Now()
		rawResult, err := s.integrations.TestProviderResource(r.Context(), resourceID, &req)
		if err != nil {
			httpErr := AsHTTPError(err)
			s.recordAdminAuditWithStatus(r, user, "test", "provider_resource", resourceID, "failed", httpErr.Code, "", map[string]any{
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
		s.recordAdminAudit(r, user, "test", "provider_resource", resourceID, "", map[string]any{
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
	tested, err := s.integrations.TestProviderResource(r.Context(), resourceID, nil)
	if err != nil {
		writeError(w, r, err)
		return
	}
	auditResult := tested
	if testedResource, ok := tested.(ProviderResource); ok {
		auditResult = auditProviderResource(testedResource)
	}
	s.recordAdminAudit(r, user, "test", "provider_resource", resourceID, "", auditResult)
	writeJSON(w, http.StatusOK, tested)
}

func (s *Server) serveAdminProviderResourceRefreshToken(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	creds, err := s.store.RefreshProviderResourceCredentials(r.Context(), resourceID, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "refresh_token", "provider_resource", resourceID, "", providerAccountCredentialSummary(creds))
	writeJSON(w, http.StatusOK, map[string]any{"credential_summary": providerAccountCredentialSummary(creds)})
}

func (s *Server) serveAdminOpenAIAccountQuota(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	quota, err := s.queryOpenAIAccountQuotaCached(r.Context(), resourceID, r.URL.Query().Get("refresh") == "true")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "query_quota", "provider_resource", resourceID, "", quota)
	writeJSON(w, http.StatusOK, quota)
}
