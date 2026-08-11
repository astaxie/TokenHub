package server

import (
	"errors"
	"net/http"
	"strings"

	"tokenhub/backend/internal/guardrails"
)

type guardrailPolicyTestRequest struct {
	Policy guardrails.Policy `json:"policy"`
	Text   string            `json:"text"`
}

type guardrailPolicyTestResponse struct {
	Action            string                       `json:"action"`
	Findings          []guardrailPolicyTestFinding `json:"findings"`
	ShortCircuited    bool                         `json:"short_circuited"`
	DetectionDegraded bool                         `json:"detection_degraded"`
	DurationMS        int64                        `json:"duration_ms"`
	MaskedText        string                       `json:"masked_text,omitempty"`
}

type guardrailPolicyTestFinding struct {
	guardrails.Finding
	Start int `json:"start"`
	End   int `json:"end"`
}

func (s *Server) handleAdminGuardrailPolicyTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "security", r.Method); !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	var request guardrailPolicyTestRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(request.Text) == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "guardrail_test_text_required", "Test text is required"))
		return
	}
	resetGuardrailRequestOwnedFields(&request.Policy, true)
	policy, err := guardrails.NormalizePolicy(request.Policy)
	if err != nil {
		writeError(w, r, guardrailStoreError(err))
		return
	}
	// Testing validates detector behavior before activation; bindings and the
	// runtime enabled state are deliberately ignored for this one request.
	policy.Status = guardrails.StatusActive
	decision, err := s.guardrailEngine.Evaluate(r.Context(), guardrails.EvaluationRequest{
		Checkpoint:     guardrails.CheckpointBeforeProvider,
		Protocol:       guardrails.ProtocolAll,
		Fragments:      []guardrails.Fragment{{ID: "test", Text: request.Text, Mutable: true}},
		Policies:       []guardrails.Policy{policy},
		IgnoreBindings: true,
	})
	if err != nil {
		writeError(w, r, guardrailEvaluationError(err))
		return
	}
	findings := make([]guardrailPolicyTestFinding, 0, len(decision.Findings))
	for _, finding := range decision.Findings {
		findings = append(findings, guardrailPolicyTestFinding{Finding: finding, Start: finding.Start, End: finding.End})
	}
	response := guardrailPolicyTestResponse{
		Action: decision.Action, Findings: findings, ShortCircuited: decision.ShortCircuited,
		DetectionDegraded: decision.DetectionDegraded, DurationMS: decision.DurationMS,
	}
	if masked, ok := decision.Replacements["test"]; ok {
		response.MaskedText = masked
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleAdminGuardrailPolicies(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "security", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		policies, err := s.store.ListGuardrailPolicies()
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": policies})
	case http.MethodPost:
		var policy guardrails.Policy
		if err := s.decodeJSON(w, r, &policy); err != nil {
			writeError(w, r, err)
			return
		}
		resetGuardrailRequestOwnedFields(&policy, false)
		created, err := s.store.CreateGuardrailPolicy(policy)
		if err != nil {
			writeError(w, r, guardrailStoreError(err))
			return
		}
		s.recordAdminAudit(r, user, "create", "guardrail_policy", created.ID, nil, guardrailPolicyAuditSnapshot(created))
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminGuardrailPolicyItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "security", r.Method)
	if !ok {
		return
	}
	parts := splitEscapedAdminPath(r.URL.EscapedPath(), "/api/admin/guardrail-policies/")
	if len(parts) != 1 || strings.TrimSpace(parts[0]) == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	policyID := strings.TrimSpace(parts[0])
	switch r.Method {
	case http.MethodGet:
		policy, err := s.store.GetGuardrailPolicy(policyID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
	case http.MethodPut:
		var policy guardrails.Policy
		if err := s.decodeJSON(w, r, &policy); err != nil {
			writeError(w, r, err)
			return
		}
		resetGuardrailRequestOwnedFields(&policy, true)
		before, updated, err := s.store.UpdateGuardrailPolicy(policyID, policy)
		if err != nil {
			writeError(w, r, guardrailStoreError(err))
			return
		}
		s.recordAdminAudit(r, user, "update", "guardrail_policy", policyID, guardrailPolicyAuditSnapshot(before), guardrailPolicyAuditSnapshot(updated))
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		before, err := s.store.DeleteGuardrailPolicy(policyID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "guardrail_policy", policyID, guardrailPolicyAuditSnapshot(before), nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func resetGuardrailRequestOwnedFields(policy *guardrails.Policy, preserveDetectionItemIDs bool) {
	policy.ID = ""
	for index := range policy.DetectionItems {
		item := &policy.DetectionItems[index]
		if !preserveDetectionItemIDs {
			item.ID = ""
		}
		item.PolicyID = ""
	}
	for index := range policy.Bindings {
		binding := &policy.Bindings[index]
		binding.ID = ""
		binding.PolicyID = ""
	}
}

func guardrailStoreError(err error) error {
	var validationErr *guardrails.ValidationError
	if errors.As(err, &validationErr) {
		return NewHTTPError(http.StatusBadRequest, validationErr.Code, validationErr.Message)
	}
	return err
}

func guardrailPolicyAuditSnapshot(policy guardrails.Policy) map[string]any {
	items := make([]map[string]any, 0, len(policy.DetectionItems))
	for _, item := range policy.DetectionItems {
		items = append(items, map[string]any{
			"id":             item.ID,
			"name":           item.Name,
			"detector_type":  item.DetectorType,
			"action":         item.Action,
			"config_version": item.ConfigVersion,
		})
	}
	bindings := make([]map[string]any, 0, len(policy.Bindings))
	for _, binding := range policy.Bindings {
		bindings = append(bindings, map[string]any{
			"id":             binding.ID,
			"scope_type":     binding.ScopeType,
			"scope_id":       binding.ScopeID,
			"checkpoint":     binding.Checkpoint,
			"protocol":       binding.Protocol,
			"config_version": binding.ConfigVersion,
		})
	}
	return map[string]any{
		"id":              policy.ID,
		"name":            policy.Name,
		"description":     policy.Description,
		"status":          policy.Status,
		"config_version":  policy.ConfigVersion,
		"detection_items": items,
		"bindings":        bindings,
	}
}
