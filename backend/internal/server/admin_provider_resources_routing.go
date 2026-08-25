package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
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
	var (
		provider            Provider
		providerOK          bool
		adapter             any
		adapterErr          error
		usesStructuredProbe bool
	)
	if resourceOK {
		provider, providerOK = s.providerByID(resource.ProviderID)
	}
	if providerOK {
		adapter, adapterErr = s.adapterRegistry.Resolve(provider.Type)
		descriptor, described := s.adapterRegistry.Describe(provider.Type)
		_, usesStructuredProbe = adapter.(ProviderResourceProber)
		usesStructuredProbe = usesStructuredProbe && described && adapterSupports(descriptor, AdapterCapabilityProbe)
	}
	if resourceOK && providerOK && adapterErr == nil && usesStructuredProbe {
		var req codexSubscriptionTestRequest
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
		startedAt := time.Now()
		rawResult, supported, err := s.executeProviderResourceProbeAction(r.Context(), user, resourceID, ProviderProbeRequest(req))
		if !supported {
			rawResult, err = s.integrations.TestProviderResource(r.Context(), resourceID, &req)
		}
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
	result, err := s.executeProviderResourceCredentialRefreshAction(r.Context(), user, resourceID, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "refresh_token", "provider_resource", resourceID, "", result.Data)
	writeJSON(w, http.StatusOK, result.Data)
}

func (s *Server) serveAdminOpenAIAccountQuota(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	result, err := s.executeProviderResourceQuotaAction(r.Context(), user, resourceID, r.URL.Query().Get("refresh") == "true")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "query_quota", "provider_resource", resourceID, "", result.Data)
	writeJSON(w, http.StatusOK, result.Data)
}

func (s *Server) executeProviderResourceQuotaAction(ctx context.Context, user AdminUser, resourceID string, refresh bool) (pluginmeta.ActionResult, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerResourcePanelAction(provider.Type, "quota", AdapterCapabilityQuota)
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "provider_resource_quota_unsupported", "Quota is not available for this provider resource")
	}
	payload, err := json.Marshal(map[string]any{
		"resource_id": resourceID,
		"refresh":     refresh,
	})
	if err != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return pluginmeta.ActionResult{}, pluginActionHTTPError(err)
	}
	return result, nil
}

func (s *Server) executeProviderResourceQuotaResetCreditsAction(ctx context.Context, user AdminUser, resourceID string) (openAIAccountQuotaResetCredits, bool, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return openAIAccountQuotaResetCredits{}, false, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return openAIAccountQuotaResetCredits{}, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerPluginCapabilityAction(provider.Type, AdapterCapabilityQuota, "quota.reset_credits.read")
	if !ok {
		return openAIAccountQuotaResetCredits{}, false, nil
	}
	payload, err := json.Marshal(map[string]any{
		"resource_id": resourceID,
	})
	if err != nil {
		return openAIAccountQuotaResetCredits{}, true, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return openAIAccountQuotaResetCredits{}, true, pluginActionHTTPError(err)
	}
	credits, ok := openAIAccountQuotaResetCreditsFromActionData(result.Data)
	if !ok {
		return openAIAccountQuotaResetCredits{}, true, NewHTTPError(http.StatusInternalServerError, "provider_quota_reset_credits_invalid_result", "Provider quota reset credits returned an invalid result")
	}
	return credits, true, nil
}

func (s *Server) executeProviderResourceQuotaResetAction(ctx context.Context, user AdminUser, resourceID string, req openAIAccountQuotaResetRequest) (openAIAccountQuotaResetResult, bool, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return openAIAccountQuotaResetResult{}, false, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return openAIAccountQuotaResetResult{}, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerPluginCapabilityAction(provider.Type, AdapterCapabilityQuota, "quota.reset")
	if !ok {
		return openAIAccountQuotaResetResult{}, false, nil
	}
	payload, err := json.Marshal(map[string]any{
		"resource_id":              resourceID,
		"confirm":                  req.Confirm,
		"idempotency_key":          req.IdempotencyKey,
		"expected_available_count": req.ExpectedAvailableCount,
		"credit_id":                req.CreditID,
		"danger_confirmation":      openAIAccountQuotaResetDangerValue,
	})
	if err != nil {
		return openAIAccountQuotaResetResult{}, true, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return openAIAccountQuotaResetResult{}, true, pluginActionHTTPError(err)
	}
	reset, ok := openAIAccountQuotaResetResultFromActionData(result.Data)
	if !ok {
		return openAIAccountQuotaResetResult{}, true, NewHTTPError(http.StatusInternalServerError, "provider_quota_reset_invalid_result", "Provider quota reset returned an invalid result")
	}
	return reset, true, nil
}

func (s *Server) executeProviderResourceProbeAction(ctx context.Context, user AdminUser, resourceID string, req ProviderProbeRequest) (any, bool, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return nil, false, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return nil, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerPluginCapabilityAction(provider.Type, AdapterCapabilityProbe, "probe.run")
	if !ok {
		return nil, false, nil
	}
	payload, err := json.Marshal(map[string]any{
		"resource_id":      resourceID,
		"model":            req.Model,
		"reasoning_effort": req.ReasoningEffort,
		"speed":            req.Speed,
		"prompt":           req.Prompt,
	})
	if err != nil {
		return nil, true, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return nil, true, pluginActionHTTPError(err)
	}
	probe, ok := providerProbeResultFromActionData(result.Data)
	if !ok {
		return nil, true, NewHTTPError(http.StatusInternalServerError, "provider_probe_invalid_result", "Provider probe returned an invalid result")
	}
	return probe, true, nil
}

func openAIAccountQuotaResetCreditsFromActionData(data any) (openAIAccountQuotaResetCredits, bool) {
	if result, ok := data.(openAIAccountQuotaResetCredits); ok {
		return result, true
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return openAIAccountQuotaResetCredits{}, false
	}
	var result openAIAccountQuotaResetCredits
	if err := json.Unmarshal(raw, &result); err != nil {
		return openAIAccountQuotaResetCredits{}, false
	}
	return result, result.FetchedAt > 0
}

func openAIAccountQuotaResetResultFromActionData(data any) (openAIAccountQuotaResetResult, bool) {
	if result, ok := data.(openAIAccountQuotaResetResult); ok {
		return result, true
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return openAIAccountQuotaResetResult{}, false
	}
	var result openAIAccountQuotaResetResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return openAIAccountQuotaResetResult{}, false
	}
	return result, strings.TrimSpace(result.Code) != ""
}

func (s *Server) executeProviderResourceCredentialRefreshAction(ctx context.Context, user AdminUser, resourceID string, force bool) (pluginmeta.ActionResult, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerPluginCapabilityAction(provider.Type, AdapterCapabilityOAuth, "credentials.refresh")
	if !ok {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "provider_resource_refresh_unsupported", "Credential refresh is not available for this provider resource")
	}
	payload, err := json.Marshal(map[string]any{
		"resource_id": resourceID,
		"force":       force,
	})
	if err != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return pluginmeta.ActionResult{}, pluginActionHTTPError(err)
	}
	return result, nil
}

func (s *Server) providerResourcePanelAction(providerType string, panelID string, capability AdapterCapability) (string, string, bool) {
	descriptor, ok := s.adapterRegistry.Describe(providerType)
	if !ok || descriptor.PluginID == "" || !adapterSupports(descriptor, capability) {
		return "", "", false
	}
	for _, contribution := range s.adminUI.List() {
		if contribution.PluginID != descriptor.PluginID || contribution.Slot != pluginmeta.SlotProviderResourcePanel || contribution.ID != panelID || contribution.Action == "" {
			continue
		}
		if len(contribution.ProviderTypes) == 0 || stringInList(providerType, contribution.ProviderTypes) {
			return contribution.PluginID, contribution.Action, true
		}
	}
	return "", "", false
}

func providerProbeResultFromActionData(data any) (ProviderProbeResult, bool) {
	if result, ok := data.(ProviderProbeResult); ok {
		return result, true
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ProviderProbeResult{}, false
	}
	var result ProviderProbeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ProviderProbeResult{}, false
	}
	return result, strings.TrimSpace(result.ResourceID) != ""
}

func (s *Server) providerPluginCapabilityAction(providerType string, capability AdapterCapability, actionCapability string) (string, string, bool) {
	descriptor, ok := s.adapterRegistry.Describe(providerType)
	if !ok || descriptor.PluginID == "" || !adapterSupports(descriptor, capability) {
		return "", "", false
	}
	for _, action := range s.pluginActions.List() {
		if action.PluginID != descriptor.PluginID || action.Capability != actionCapability {
			continue
		}
		if action.Subject != "" && action.Subject != providerType {
			continue
		}
		return action.PluginID, action.ActionID, true
	}
	return "", "", false
}
