package server

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) handleAdminRoutingPolicySimulation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "routing", r.Method); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	var req struct {
		ProjectID string `json:"project_id"`
		APIKeyID  string `json:"api_key_id"`
		Model     string `json:"model"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
		return
	}
	project, ok := s.store.GetProject(strings.TrimSpace(req.ProjectID))
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found"))
		return
	}
	var key APIKey
	for _, candidate := range s.store.ListAPIKeys() {
		if candidate.ID == strings.TrimSpace(req.APIKeyID) {
			key = candidate
			break
		}
	}
	if key.ID == "" || key.ProjectID != project.ID {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "api_key_not_found", "API key not found"))
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "missing_model", "model is required"))
		return
	}
	if reasons := modelAccessReasons(project, key, modelName); len(reasons) > 0 {
		writeJSON(w, http.StatusOK, RoutingPolicyResolution{
			MatchReason:   "access_denied",
			Candidates:    []RoutingPolicyCandidateDecision{},
			ErrorCode:     "model_not_allowed",
			ErrorMessage:  "Model is not allowed for this credential",
			AccessAllowed: false,
			AccessReasons: reasons,
		})
		return
	}
	var routes []RouteSelection
	var err error
	if modelName == codexImageModelName || modelName == openAIImageModelName {
		routes, err = s.imageRouteCandidates(modelName)
	} else {
		routes, err = s.store.SelectRouteCandidates(modelName)
	}
	if err != nil && AsHTTPError(err).Code != ErrProviderMissing.Code {
		writeError(w, r, err)
		return
	}
	call := CallContext{RequestID: NewID("sim"), Project: project, Key: key, Model: Model{Name: modelName}}
	if err != nil || len(routes) == 0 {
		_, resolution, policyErr := s.resolveScopedRoutingPolicy(call, nil)
		if policyErr != nil {
			var httpErr *HTTPError
			if errors.As(policyErr, &httpErr) && strings.HasPrefix(httpErr.Code, "routing_policy_") {
				resolution.ErrorCode = httpErr.Code
				resolution.ErrorMessage = httpErr.Message
				writeJSON(w, http.StatusOK, resolution)
				return
			}
			writeError(w, r, policyErr)
			return
		}
		writeError(w, r, ErrProviderMissing)
		return
	}
	filtered, resolution, err := s.resolveScopedRoutingPolicy(call, routes)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && strings.HasPrefix(httpErr.Code, "routing_policy_") {
			resolution.ErrorCode = httpErr.Code
			resolution.ErrorMessage = httpErr.Message
			writeJSON(w, http.StatusOK, resolution)
			return
		}
		writeError(w, r, err)
		return
	}
	planned := s.planRouteOrder(call, filtered)
	if len(planned) > 0 {
		resolution.SelectedRouteID = planned[0].Route.ID
	}
	writeJSON(w, http.StatusOK, resolution)
}

func (s *Server) handleAdminRoutingPolicyAction(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/routing-policies/")
	if r.Method != http.MethodPost || len(parts) != 2 || parts[0] == "" || (parts[1] != "bind" && parts[1] != "unbind") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	policy, err := s.findResource(routingPolicyResourceKind, parts[0])
	if err != nil {
		writeError(w, r, err)
		return
	}
	fields := make(map[string]any, len(policy.Fields)+3)
	for key, value := range policy.Fields {
		fields[key] = value
	}
	action := parts[1]
	if action == "unbind" {
		fields["scope"] = RoutingPolicyScopeUnbound
		fields["scope_id"] = ""
	} else {
		var req struct {
			Scope   string `json:"scope"`
			ScopeID string `json:"scope_id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
			return
		}
		fields["scope"] = req.Scope
		fields["scope_id"] = req.ScopeID
	}
	patch := AdminResource{Fields: fields}
	if err := s.validateScopedResourceMutation(user, routingPolicyResourceKind, policy.ID, patch); err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := s.store.UpdateResource(routingPolicyResourceKind, policy.ID, patch)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, action, routingPolicyResourceKind, policy.ID, policy, updated)
	writeJSON(w, http.StatusOK, updated)
}
