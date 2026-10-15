package server

import (
	"net/http"
	"strings"
)

// allUsersQuotaScopeID selects a template, never a shared quota bucket.
const allUsersQuotaScopeID = "all_users"

func userQuotaPolicyApplies(scopeID, attributedUserID string) bool {
	return attributedUserID != "" && attributedUserID != unattributedQuotaUserID &&
		(scopeID == allUsersQuotaScopeID || scopeID == attributedUserID)
}

func isDefaultUserQuotaPolicy(fields map[string]any) bool {
	return strings.EqualFold(strings.TrimSpace(firstStringField(fields, "scope", "scope_type")), "user") &&
		strings.TrimSpace(stringField(fields, "scope_id")) == allUsersQuotaScopeID
}

func defaultUserQuotaForbidden() *HTTPError {
	return NewHTTPError(http.StatusForbidden, "quota_forbidden", "Only platform administrators can manage default user quota policies")
}

func defaultUserQuotaProjectConflict() *HTTPError {
	return NewHTTPError(http.StatusConflict, "default_user_quota_project_conflict", "Default user quota policies cannot be changed through project quota increases; configure a project-scoped quota policy instead")
}

func (s *Server) validateProjectQuotaApprovalTarget(resourceID string, payload map[string]any) error {
	fields := fieldsFromPayload(payload["fields"])
	projectScoped := strings.EqualFold(strings.TrimSpace(firstStringField(fields, "scope", "scope_type")), "project")
	if resourceID == "" || !projectScoped && strings.TrimSpace(stringFromPayload(payload, "project_id")) == "" || isDefaultUserQuotaPolicy(fields) {
		return nil
	}
	existing, err := s.findResource("quota-policies", resourceID)
	if err != nil {
		return err
	}
	if isDefaultUserQuotaPolicy(existing.Fields) {
		return defaultUserQuotaProjectConflict()
	}
	return nil
}

func (s *Server) validateDefaultUserQuotaMutation(user AdminUser, resourceID string, fields map[string]any) error {
	if isPlatformAdminRole(user.Role) {
		return nil
	}
	if isDefaultUserQuotaPolicy(fields) {
		return defaultUserQuotaForbidden()
	}
	if resourceID != "" {
		existing, err := s.findResource("quota-policies", resourceID)
		if err != nil {
			return err
		}
		if isDefaultUserQuotaPolicy(existing.Fields) {
			return defaultUserQuotaForbidden()
		}
		mergedFields := cloneAdminResourceFields(existing.Fields)
		for key, value := range fields {
			mergedFields[key] = value
		}
		if isDefaultUserQuotaPolicy(mergedFields) {
			return defaultUserQuotaForbidden()
		}
	}
	return nil
}

func (s *Server) validateUserQuotaPolicyTarget(user AdminUser, resourceID, status string, fields map[string]any) error {
	scopeID := strings.TrimSpace(stringField(fields, "scope_id"))
	if scopeID == "" {
		return NewHTTPError(http.StatusBadRequest, "invalid_quota_policy_scope", "User quota policies require a user scope_id or all_users")
	}
	if scopeID == allUsersQuotaScopeID {
		if !isPlatformAdminRole(user.Role) {
			return defaultUserQuotaForbidden()
		}
		return nil
	}
	target, ok := s.findAdminUser(scopeID)
	if !ok {
		return NewHTTPError(http.StatusNotFound, "admin_user_not_found", "Quota policy user not found")
	}
	resultingStatus := StatusActive
	if resourceID != "" {
		existing, err := s.findResource("quota-policies", resourceID)
		if err != nil {
			return err
		}
		resultingStatus = existing.Status
	}
	if status != "" {
		resultingStatus = status
	}
	if target.Status != StatusActive && !strings.EqualFold(strings.TrimSpace(resultingStatus), StatusDisabled) {
		return NewHTTPError(http.StatusBadRequest, "invalid_quota_policy_scope", "User quota policies require an active user")
	}
	if normalizeAdminRole(user.Role) == "team_leader" && !userHasTeam(target, user.TeamID) {
		return NewHTTPError(http.StatusForbidden, "quota_forbidden", "Team leader can only manage quotas for users in own team")
	}
	return nil
}
