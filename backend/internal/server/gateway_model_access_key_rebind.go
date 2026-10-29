package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const gatewayModelAccessKeyProjectRebindAction = "gateway_model_access_key_project_rebind"

type GatewayModelAccessKeyProjectInput struct {
	TenantExternalID          string `json:"tenant_id"`
	PrincipalType             string `json:"principal_type"`
	PrincipalExternalID       string `json:"principal_id"`
	ProjectExternalID         string `json:"project_id"`
	ExpectedProjectExternalID string `json:"expected_project_id"`
	RequestedBy               string `json:"requested_by"`
}

func (s *Server) handleGatewayModelAccessKeyProject(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	var input GatewayModelAccessKeyProjectInput
	if err := s.decodeJSON(w, r, &input); err != nil {
		writeError(w, r, err)
		return
	}
	key, err := s.store.RebindGatewayModelAccessKeyProject(id, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": key})
}

func (s *GormStore) RebindGatewayModelAccessKeyProject(id string, input GatewayModelAccessKeyProjectInput) (APIKey, error) {
	id = strings.TrimSpace(id)
	input.TenantExternalID = strings.TrimSpace(input.TenantExternalID)
	input.PrincipalType = strings.TrimSpace(input.PrincipalType)
	input.PrincipalExternalID = strings.TrimSpace(input.PrincipalExternalID)
	input.ProjectExternalID = strings.TrimSpace(input.ProjectExternalID)
	input.ExpectedProjectExternalID = strings.TrimSpace(input.ExpectedProjectExternalID)
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	if id == "" || input.TenantExternalID == "" || input.PrincipalType != "user" || input.PrincipalExternalID == "" || input.ProjectExternalID == "" || input.ExpectedProjectExternalID == "" || input.RequestedBy == "" {
		return APIKey{}, NewHTTPError(http.StatusBadRequest, "invalid_model_access_key_project", "Key, tenant, user principal, project, expected project, and actor are required")
	}
	if input.RequestedBy != input.PrincipalExternalID {
		return APIKey{}, NewHTTPError(http.StatusForbidden, "model_access_key_actor_mismatch", "Only the credential owner can change its project")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var key APIKey
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Projection events take the exclusive tenant lock before touching keys.
		// Share that order to keep authorization stable without lock inversions.
		if err := s.lockScopeForSharedRead(tx, "integration_tenant", input.TenantExternalID); err != nil {
			return err
		}
		// Use the same cross-instance key lock as admission and settlement. Neither
		// a scope change nor a concurrent revocation may overwrite the other.
		if err := s.lockScopeForUpdate(tx, "api_key", id); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&key, "id = ? AND managed_by = ? AND tenant_external_id = ? AND principal_type = ? AND principal_external_id = ?", id, gatewayModelAccessKeyManagedBy, input.TenantExternalID, "user", input.PrincipalExternalID).Error; err != nil {
			return notFound(err, "model_access_key_not_found", "Model access key not found")
		}
		if key.Status != StatusActive || key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now().UTC()) {
			return NewHTTPError(http.StatusConflict, "model_access_key_unavailable", "Model access key is not active")
		}
		if key.ProjectExternalID != input.ExpectedProjectExternalID {
			replayed, err := gatewayModelAccessKeyProjectReplay(tx, key, input)
			if err != nil {
				return err
			}
			if !replayed {
				return gatewayModelAccessKeyProjectConflict()
			}
		}
		project, err := resolveGatewayModelAccessKeyRebindScope(tx, input)
		if err != nil {
			return err
		}
		if key.ProjectID == project.ID && key.ProjectExternalID == project.ExternalProjectID {
			return nil
		}
		if err := syncGatewayServingProject(tx, project, time.Now().UTC()); err != nil {
			return err
		}
		before, err := json.Marshal(map[string]string{"project_id": key.ProjectExternalID})
		if err != nil {
			return err
		}
		after, err := json.Marshal(map[string]string{"project_id": project.ExternalProjectID})
		if err != nil {
			return err
		}
		// Update only scope columns. Hash, encrypted secret, cumulative quota buckets,
		// active leases, and historical records all retain their existing identity.
		updated := tx.Model(&APIKey{}).Where("id = ? AND project_id = ? AND project_external_id = ? AND status = ?", id, key.ProjectID, input.ExpectedProjectExternalID, StatusActive).
			Updates(map[string]any{"project_id": project.ID, "project_external_id": project.ExternalProjectID})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return gatewayModelAccessKeyProjectConflict()
		}
		key.ProjectID = project.ID
		key.ProjectExternalID = project.ExternalProjectID
		return tx.Create(&AuditEvent{
			ID: NewID("audit"), ActorUserID: input.RequestedBy, Action: gatewayModelAccessKeyProjectRebindAction,
			ResourceType: "api_key", ResourceID: key.ID, Status: "success",
			Message:        "Model access key project changed through the integration API",
			BeforeSnapshot: string(before), AfterSnapshot: string(after), CreatedAt: time.Now().UTC(),
		}).Error
	})
	if err != nil {
		return APIKey{}, err
	}
	// Authentication and admission read the database for every request; there is
	// no credential scope cache to evict. Admitted CallContext values are snapshots.
	hydrateAPIKey(&key)
	return publicKey(key), nil
}

func gatewayModelAccessKeyProjectConflict() error {
	return NewHTTPError(http.StatusConflict, "model_access_key_project_conflict", "Model access key project changed; refresh the credential and retry")
}

func gatewayModelAccessKeyProjectReplay(tx *gorm.DB, key APIKey, input GatewayModelAccessKeyProjectInput) (bool, error) {
	if key.ProjectExternalID != input.ProjectExternalID {
		return false, nil
	}
	var audit AuditEvent
	if err := tx.Where("action = ? AND resource_id = ?", gatewayModelAccessKeyProjectRebindAction, key.ID).Order("created_at DESC, id DESC").First(&audit).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	var before, after map[string]string
	if json.Unmarshal([]byte(audit.BeforeSnapshot), &before) != nil || json.Unmarshal([]byte(audit.AfterSnapshot), &after) != nil {
		return false, nil
	}
	return audit.ActorUserID == input.RequestedBy && before["project_id"] == input.ExpectedProjectExternalID && after["project_id"] == input.ProjectExternalID, nil
}

func resolveGatewayModelAccessKeyRebindScope(tx *gorm.DB, input GatewayModelAccessKeyProjectInput) (GatewayProject, error) {
	// Shared row locks keep projected authorization valid until the scope update
	// commits. SQLite serializes writes; PostgreSQL honors these locks across replicas.
	locked := func() *gorm.DB { return tx.Clauses(clause.Locking{Strength: "SHARE"}) }
	var tenant GatewayTenant
	if err := locked().First(&tenant, "external_tenant_id = ? AND status = ? AND deleted_at IS NULL", input.TenantExternalID, StatusActive).Error; err != nil {
		return GatewayProject{}, gatewayModelAccessKeyScopeError(err, "gateway_tenant_unavailable", "Gateway tenant projection is not active")
	}
	var project GatewayProject
	if err := locked().First(&project, "tenant_id = ? AND external_project_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, input.ProjectExternalID, StatusActive).Error; err != nil {
		return GatewayProject{}, gatewayModelAccessKeyScopeError(err, "gateway_project_unavailable", "Gateway project projection is not active")
	}
	if project.OrganizationID != "" {
		var organization GatewayOrganization
		if err := locked().First(&organization, "id = ? AND tenant_id = ? AND status = ? AND deleted_at IS NULL", project.OrganizationID, tenant.ID, StatusActive).Error; err != nil {
			return GatewayProject{}, gatewayModelAccessKeyScopeError(err, "gateway_organization_unavailable", "Gateway project organization is not active")
		}
	}
	var principal GatewayPrincipal
	if err := locked().First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
		return GatewayProject{}, gatewayModelAccessKeyScopeError(err, "gateway_principal_unavailable", "Gateway principal projection is not active")
	}
	if project.OrganizationID != "" {
		var binding GatewayPrincipalOrganizationBinding
		if err := locked().First(&binding, "tenant_id = ? AND principal_id = ? AND organization_id = ? AND status = ? AND deleted_at IS NULL", tenant.ID, principal.ID, project.OrganizationID, StatusActive).Error; err != nil {
			return GatewayProject{}, gatewayModelAccessKeyScopeError(err, "gateway_organization_membership_unavailable", "Gateway principal is not active in the project organization")
		}
	}
	return project, nil
}

func gatewayModelAccessKeyScopeError(err error, code string, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewHTTPError(http.StatusConflict, code, message)
	}
	return err
}
