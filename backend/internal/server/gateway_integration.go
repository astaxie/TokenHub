package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	integrationStatusApplied = "applied"
	integrationStatusDeleted = "deleted"
)

func (s *Server) registerGatewayRoutes() {
	s.mux.HandleFunc("/api/internal/integration/events", s.handleGatewayIntegrationEvent)
	s.mux.HandleFunc("/api/internal/integration/reconciliation", s.handleGatewayIntegrationReconciliation)
	s.mux.HandleFunc("/api/internal/models", s.handleGatewayModels)
	s.mux.HandleFunc("/api/internal/providers", s.handleGatewayProviders)
	s.mux.HandleFunc("/api/internal/routes", s.handleGatewayRoutes)
	s.mux.HandleFunc("/api/internal/model-access-keys", s.handleGatewayModelAccessKeys)
	s.mux.HandleFunc("/api/internal/model-access-keys/", s.handleGatewayModelAccessKeyItem)
	s.mux.HandleFunc("/api/internal/request-logs", s.handleGatewayRequestLogs)
	s.mux.HandleFunc("/api/internal/usage", s.handleGatewayUsage)
}

type GatewayIntegrationEvent struct {
	SchemaVersion int                    `json:"schemaVersion"`
	EventID       string                 `json:"eventId"`
	EventType     string                 `json:"eventType"`
	AggregateType string                 `json:"aggregateType"`
	AggregateID   string                 `json:"aggregateId"`
	TenantID      string                 `json:"tenantId"`
	Version       int64                  `json:"version"`
	OccurredAt    time.Time              `json:"occurredAt"`
	TraceID       string                 `json:"traceId"`
	SourceService string                 `json:"sourceService"`
	Payload       map[string]interface{} `json:"payload"`
}

type GatewayIntegrationApplyResult struct {
	EventID          string `json:"event_id"`
	Outcome          string `json:"outcome"`
	TokenHubEntityID string `json:"tokenhub_entity_id,omitempty"`
	EntityType       string `json:"entity_type"`
	AppliedVersion   int64  `json:"applied_version"`
}

type GatewayIntegrationReconciliationSummary struct {
	TenantID            string                                            `json:"tenant_id"`
	ReceivedEvents      int64                                             `json:"received_events"`
	AggregateCounts     map[string]int64                                  `json:"aggregate_counts"`
	ProjectionSummaries map[string]GatewayProjectionReconciliationSummary `json:"projection_summaries"`
	LatestReceivedAt    *time.Time                                        `json:"latest_received_at"`
	LatestProcessedAt   *time.Time                                        `json:"latest_processed_at"`
}

type GatewayProjectionReconciliationSummary struct {
	Count      int64  `json:"count"`
	MaxVersion int64  `json:"max_version"`
	Digest     string `json:"digest"`
}

type gatewayProjectionDigestItem struct {
	ExternalID     string `json:"external_id"`
	PrincipalID    string `json:"principal_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	ParentID       string `json:"parent_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	OwnerID        string `json:"owner_id,omitempty"`
	Name           string `json:"name,omitempty"`
	WorkloadType   string `json:"workload_type,omitempty"`
	Environment    string `json:"environment,omitempty"`
	Status         string `json:"status"`
	Version        int64  `json:"version"`
	Deleted        bool   `json:"deleted"`
}

type GatewayTenant struct {
	ID               string `gorm:"primaryKey"`
	ExternalTenantID string `gorm:"uniqueIndex"`
	Name             string
	Status           string `gorm:"index"`
	Version          int64
	SyncedAt         time.Time
	DeletedAt        *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type GatewayOrganization struct {
	ID                     string `gorm:"primaryKey"`
	TenantID               string `gorm:"uniqueIndex:idx_gateway_org_external,priority:1;index"`
	ExternalOrganizationID string `gorm:"uniqueIndex:idx_gateway_org_external,priority:2"`
	ParentID               string `gorm:"index"`
	Name                   string
	Status                 string `gorm:"index"`
	Version                int64
	SyncedAt               time.Time
	DeletedAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type GatewayPrincipal struct {
	ID                   string `gorm:"primaryKey"`
	TenantID             string `gorm:"uniqueIndex:idx_gateway_principal_external,priority:1;index"`
	ExternalPrincipalID  string `gorm:"uniqueIndex:idx_gateway_principal_external,priority:2"`
	ExternalMembershipID string `gorm:"index"`
	DisplayName          string
	Status               string `gorm:"index"`
	Version              int64
	SourceOccurredAt     time.Time
	SyncedAt             time.Time
	DeletedAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type GatewayPrincipalOrganizationBinding struct {
	ID                   string `gorm:"primaryKey"`
	TenantID             string `gorm:"uniqueIndex:idx_gateway_org_binding_external,priority:1;index"`
	ExternalMembershipID string `gorm:"uniqueIndex:idx_gateway_org_binding_external,priority:2"`
	PrincipalID          string `gorm:"index"`
	OrganizationID       string `gorm:"index"`
	Status               string `gorm:"index"`
	Version              int64
	SyncedAt             time.Time
	DeletedAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type GatewayProject struct {
	ID                string `gorm:"primaryKey"`
	TenantID          string `gorm:"uniqueIndex:idx_gateway_project_external,priority:1;index"`
	ExternalProjectID string `gorm:"uniqueIndex:idx_gateway_project_external,priority:2"`
	OrganizationID    string `gorm:"index"`
	OwnerPrincipalID  string `gorm:"index"`
	Name              string
	Status            string `gorm:"index"`
	Version           int64
	SyncedAt          time.Time
	DeletedAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type GatewayWorkload struct {
	ID                 string `gorm:"primaryKey"`
	TenantID           string `gorm:"uniqueIndex:idx_gateway_workload_external,priority:1;index"`
	ExternalWorkloadID string `gorm:"uniqueIndex:idx_gateway_workload_external,priority:2"`
	ProjectID          string `gorm:"index"`
	OwnerPrincipalID   string `gorm:"index"`
	Name               string
	WorkloadType       string
	Environment        string `gorm:"index"`
	Status             string `gorm:"index"`
	Version            int64
	SyncedAt           time.Time
	DeletedAt          *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type IntegrationInbox struct {
	EventID          string `gorm:"primaryKey"`
	EventDigest      string
	TenantID         string `gorm:"index"`
	EventType        string
	AggregateType    string `gorm:"index:idx_integration_inbox_aggregate,priority:1"`
	AggregateID      string `gorm:"index:idx_integration_inbox_aggregate,priority:2"`
	AggregateVersion int64  `gorm:"index:idx_integration_inbox_aggregate,priority:3"`
	AppliedVersion   int64
	Status           string `gorm:"index"`
	EntityType       string
	TokenHubEntityID string
	ReceivedAt       time.Time
	ProcessedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (IntegrationInbox) TableName() string { return "integration_inbox" }

func (s *Server) handleGatewayIntegrationEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}

	var event GatewayIntegrationEvent
	if err := decodeJSON(r, &event); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Invalid integration event"))
		return
	}
	result, err := s.store.ApplyGatewayIntegrationEvent(event)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGatewayIntegrationReconciliation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if tenantID == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_tenant_scope", "tenant_id is required"))
		return
	}
	summary, err := s.store.GetGatewayIntegrationReconciliation(tenantID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) requireGatewayIntegrationToken(w http.ResponseWriter, r *http.Request) bool {
	configured := strings.TrimSpace(s.config.IntegrationToken)
	presented := bearerToken(r)
	if configured == "" || !hmac.Equal([]byte(presented), []byte(configured)) {
		writeError(w, r, NewHTTPError(http.StatusUnauthorized, "invalid_integration_token", "Invalid integration token"))
		return false
	}
	return true
}

func (s *GormStore) ApplyGatewayIntegrationEvent(event GatewayIntegrationEvent) (GatewayIntegrationApplyResult, error) {
	if err := validateGatewayIntegrationEvent(event); err != nil {
		return GatewayIntegrationApplyResult{}, err
	}
	digest, err := gatewayIntegrationEventDigest(event)
	if err != nil {
		return GatewayIntegrationApplyResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var result GatewayIntegrationApplyResult
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "integration_tenant", event.TenantID); err != nil {
			return err
		}
		var existing IntegrationInbox
		if err := tx.First(&existing, "event_id = ?", event.EventID).Error; err == nil {
			if existing.EventDigest != digest {
				return NewHTTPError(http.StatusConflict, "integration_event_conflict", "Integration event ID was reused with different content")
			}
			result = GatewayIntegrationApplyResult{
				EventID: event.EventID, Outcome: "duplicate", TokenHubEntityID: existing.TokenHubEntityID,
				EntityType: existing.EntityType, AppliedVersion: existing.AppliedVersion,
			}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		now := time.Now().UTC()
		inbox := IntegrationInbox{
			EventID: event.EventID, EventDigest: digest, TenantID: event.TenantID, EventType: event.EventType,
			AggregateType: event.AggregateType, AggregateID: event.AggregateID, AggregateVersion: event.Version,
			Status: "processing", ReceivedAt: now,
		}
		if err := tx.Create(&inbox).Error; err != nil {
			return err
		}

		entityID, entityType, appliedVersion, outcome, err := applyGatewayProjection(tx, event, now)
		if err != nil {
			return err
		}
		inbox.Status = integrationStatusApplied
		inbox.EntityType = entityType
		inbox.TokenHubEntityID = entityID
		inbox.AppliedVersion = appliedVersion
		inbox.ProcessedAt = &now
		if err := tx.Save(&inbox).Error; err != nil {
			return err
		}
		result = GatewayIntegrationApplyResult{
			EventID: event.EventID, Outcome: outcome, TokenHubEntityID: entityID,
			EntityType: entityType, AppliedVersion: appliedVersion,
		}
		return nil
	})
	return result, err
}

func (s *GormStore) GetGatewayIntegrationReconciliation(tenantExternalID string) (GatewayIntegrationReconciliationSummary, error) {
	tenantID := strings.TrimSpace(tenantExternalID)
	if tenantID == "" {
		return GatewayIntegrationReconciliationSummary{}, NewHTTPError(http.StatusBadRequest, "invalid_tenant_scope", "tenant_id is required")
	}
	summary := GatewayIntegrationReconciliationSummary{
		TenantID: tenantID,
		AggregateCounts: map[string]int64{
			"tenant": 0, "organization": 0, "tenant_member": 0,
			"organization_member": 0, "project": 0, "workload": 0,
		},
	}
	if err := s.db.Model(&IntegrationInbox{}).Where("tenant_id = ?", tenantID).Count(&summary.ReceivedEvents).Error; err != nil {
		return GatewayIntegrationReconciliationSummary{}, err
	}
	var groups []struct {
		AggregateType string
		EventCount    int64
	}
	if err := s.db.Model(&IntegrationInbox{}).Where("tenant_id = ?", tenantID).
		Select("aggregate_type, count(*) AS event_count").Group("aggregate_type").Scan(&groups).Error; err != nil {
		return GatewayIntegrationReconciliationSummary{}, err
	}
	for _, group := range groups {
		summary.AggregateCounts[group.AggregateType] = group.EventCount
	}
	var latest IntegrationInbox
	if err := s.db.Where("tenant_id = ?", tenantID).Order("received_at DESC").First(&latest).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return GatewayIntegrationReconciliationSummary{}, err
	} else if err == nil {
		summary.LatestReceivedAt = &latest.ReceivedAt
	}
	var processed IntegrationInbox
	if err := s.db.Where("tenant_id = ? AND processed_at IS NOT NULL", tenantID).Order("processed_at DESC").First(&processed).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return GatewayIntegrationReconciliationSummary{}, err
	} else if err == nil {
		summary.LatestProcessedAt = processed.ProcessedAt
	}
	projectionSummaries, err := s.gatewayProjectionReconciliation(tenantID)
	if err != nil {
		return GatewayIntegrationReconciliationSummary{}, err
	}
	summary.ProjectionSummaries = projectionSummaries
	return summary, nil
}

func (s *GormStore) gatewayProjectionReconciliation(tenantExternalID string) (map[string]GatewayProjectionReconciliationSummary, error) {
	itemsByType := map[string][]gatewayProjectionDigestItem{
		"tenant": {}, "organization": {}, "tenant_member": {},
		"organization_member": {}, "project": {}, "workload": {},
	}
	var tenant GatewayTenant
	if err := s.db.First(&tenant, "external_tenant_id = ?", tenantExternalID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return gatewayProjectionSummaries(itemsByType), nil
		}
		return nil, err
	}
	itemsByType["tenant"] = append(itemsByType["tenant"], gatewayProjectionDigestItem{
		ExternalID: tenant.ExternalTenantID, Name: tenant.Name, Status: tenant.Status,
		Version: tenant.Version, Deleted: tenant.DeletedAt != nil,
	})

	var organizations []GatewayOrganization
	if err := s.db.Where("tenant_id = ?", tenant.ID).Find(&organizations).Error; err != nil {
		return nil, err
	}
	organizationIDs := make(map[string]string, len(organizations))
	for _, item := range organizations {
		organizationIDs[item.ID] = item.ExternalOrganizationID
	}
	for _, item := range organizations {
		itemsByType["organization"] = append(itemsByType["organization"], gatewayProjectionDigestItem{
			ExternalID: item.ExternalOrganizationID, ParentID: organizationIDs[item.ParentID], Name: item.Name,
			Status: item.Status, Version: item.Version, Deleted: item.DeletedAt != nil,
		})
	}

	var principals []GatewayPrincipal
	if err := s.db.Where("tenant_id = ?", tenant.ID).Find(&principals).Error; err != nil {
		return nil, err
	}
	principalIDs := make(map[string]string, len(principals))
	for _, item := range principals {
		principalIDs[item.ID] = item.ExternalPrincipalID
		itemsByType["tenant_member"] = append(itemsByType["tenant_member"], gatewayProjectionDigestItem{
			ExternalID: item.ExternalMembershipID, PrincipalID: item.ExternalPrincipalID, Name: item.DisplayName,
			Status: item.Status, Version: item.Version, Deleted: item.DeletedAt != nil,
		})
	}

	var bindings []GatewayPrincipalOrganizationBinding
	if err := s.db.Where("tenant_id = ?", tenant.ID).Find(&bindings).Error; err != nil {
		return nil, err
	}
	for _, item := range bindings {
		itemsByType["organization_member"] = append(itemsByType["organization_member"], gatewayProjectionDigestItem{
			ExternalID: item.ExternalMembershipID, PrincipalID: principalIDs[item.PrincipalID], OrganizationID: organizationIDs[item.OrganizationID],
			Status: item.Status, Version: item.Version, Deleted: item.DeletedAt != nil,
		})
	}

	var projects []GatewayProject
	if err := s.db.Where("tenant_id = ?", tenant.ID).Find(&projects).Error; err != nil {
		return nil, err
	}
	projectIDs := make(map[string]string, len(projects))
	for _, item := range projects {
		projectIDs[item.ID] = item.ExternalProjectID
		itemsByType["project"] = append(itemsByType["project"], gatewayProjectionDigestItem{
			ExternalID: item.ExternalProjectID, OrganizationID: organizationIDs[item.OrganizationID], OwnerID: principalIDs[item.OwnerPrincipalID],
			Name: item.Name, Status: item.Status, Version: item.Version, Deleted: item.DeletedAt != nil,
		})
	}

	var workloads []GatewayWorkload
	if err := s.db.Where("tenant_id = ?", tenant.ID).Find(&workloads).Error; err != nil {
		return nil, err
	}
	for _, item := range workloads {
		itemsByType["workload"] = append(itemsByType["workload"], gatewayProjectionDigestItem{
			ExternalID: item.ExternalWorkloadID, ProjectID: projectIDs[item.ProjectID], OwnerID: principalIDs[item.OwnerPrincipalID],
			Name: item.Name, WorkloadType: item.WorkloadType, Environment: item.Environment,
			Status: item.Status, Version: item.Version, Deleted: item.DeletedAt != nil,
		})
	}
	return gatewayProjectionSummaries(itemsByType), nil
}

func gatewayProjectionSummaries(itemsByType map[string][]gatewayProjectionDigestItem) map[string]GatewayProjectionReconciliationSummary {
	summaries := make(map[string]GatewayProjectionReconciliationSummary, len(itemsByType))
	for aggregateType, items := range itemsByType {
		sort.Slice(items, func(i, j int) bool { return items[i].ExternalID < items[j].ExternalID })
		maxVersion := int64(0)
		for _, item := range items {
			if item.Version > maxVersion {
				maxVersion = item.Version
			}
		}
		payload, _ := json.Marshal(items)
		digest := sha256.Sum256(payload)
		summaries[aggregateType] = GatewayProjectionReconciliationSummary{
			Count: int64(len(items)), MaxVersion: maxVersion, Digest: hex.EncodeToString(digest[:]),
		}
	}
	return summaries
}

func validateGatewayIntegrationEvent(event GatewayIntegrationEvent) error {
	if event.SchemaVersion != 1 || strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.TenantID) == "" ||
		strings.TrimSpace(event.AggregateID) == "" || event.Version < 1 || event.OccurredAt.IsZero() ||
		strings.TrimSpace(event.TraceID) == "" || strings.TrimSpace(event.SourceService) == "" || event.Payload == nil {
		return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Invalid integration event envelope")
	}
	if event.AggregateType == "" || !strings.HasPrefix(event.EventType, event.AggregateType+".") {
		return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Integration event aggregate does not match its type")
	}
	if payloadString(event.Payload, "externalId") != event.AggregateID {
		return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Integration event aggregate does not match its payload")
	}
	if event.AggregateType == "tenant" && event.TenantID != event.AggregateID {
		return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Tenant event scope does not match its aggregate")
	}
	if err := validateGatewayIntegrationPayload(event); err != nil {
		return err
	}
	allowedTypes := map[string]bool{
		"tenant.created": true, "tenant.updated": true, "tenant.disabled": true, "tenant.deleted": true,
		"organization.created": true, "organization.updated": true, "organization.moved": true, "organization.disabled": true, "organization.deleted": true,
		"tenant_member.added": true, "tenant_member.updated": true, "tenant_member.removed": true,
		"organization_member.added": true, "organization_member.updated": true, "organization_member.removed": true,
		"project.created": true, "project.updated": true, "project.archived": true, "project.deleted": true,
		"workload.created": true, "workload.updated": true, "workload.disabled": true, "workload.deleted": true,
	}
	if !allowedTypes[event.EventType] {
		return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Unknown integration event type")
	}
	return nil
}

func validateGatewayIntegrationPayload(event GatewayIntegrationEvent) error {
	required := func(fields ...string) error {
		for _, field := range fields {
			if payloadString(event.Payload, field) == "" {
				return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Integration event payload is incomplete")
			}
		}
		return nil
	}
	switch event.AggregateType {
	case "tenant":
		if event.EventType == "tenant.created" {
			return required("name")
		}
	case "organization":
		if payloadString(event.Payload, "parentExternalId") == event.AggregateID {
			return NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Organization cannot be its own parent")
		}
		if event.EventType == "organization.created" {
			return required("name")
		}
	case "tenant_member":
		if err := required("principalExternalId"); err != nil {
			return err
		}
		if event.EventType == "tenant_member.added" {
			return required("name")
		}
	case "organization_member":
		return required("principalExternalId", "organizationExternalId")
	case "project":
		if event.EventType == "project.created" {
			return required("name")
		}
	case "workload":
		if err := required("projectExternalId"); err != nil {
			return err
		}
		if event.EventType == "workload.created" {
			return required("name", "workloadType")
		}
	}
	return nil
}

func gatewayIntegrationEventDigest(event GatewayIntegrationEvent) (string, error) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func applyGatewayProjection(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	switch event.AggregateType {
	case "tenant":
		return applyGatewayTenant(tx, event, now)
	case "organization":
		return applyGatewayOrganization(tx, event, now)
	case "tenant_member":
		return applyGatewayPrincipal(tx, event, now)
	case "organization_member":
		return applyGatewayOrganizationBinding(tx, event, now)
	case "project":
		return applyGatewayProject(tx, event, now)
	case "workload":
		return applyGatewayWorkload(tx, event, now)
	default:
		return "", "", 0, "", NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Unknown integration aggregate type")
	}
}

func projectionStatus(event GatewayIntegrationEvent, currentStatus string, currentDeletedAt *time.Time, now time.Time) (string, *time.Time) {
	suffix := event.EventType[strings.LastIndex(event.EventType, ".")+1:]
	switch suffix {
	case "disabled":
		return StatusDisabled, nil
	case "deleted", "removed":
		if suffix == "removed" {
			status := payloadString(event.Payload, "status")
			if status != "" && status != "removed" && status != integrationStatusDeleted {
				return status, nil
			}
		}
		return integrationStatusDeleted, &now
	case "archived":
		return "archived", nil
	default:
		if status := payloadString(event.Payload, "status"); status != "" {
			return status, nil
		}
		if currentStatus != "" {
			return currentStatus, currentDeletedAt
		}
		return StatusActive, nil
	}
}

func payloadString(payload map[string]interface{}, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func requireGatewayTenant(tx *gorm.DB, externalTenantID string) (GatewayTenant, error) {
	var tenant GatewayTenant
	if err := tx.First(&tenant, "external_tenant_id = ?", externalTenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GatewayTenant{}, NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway tenant projection is not available")
		}
		return GatewayTenant{}, err
	}
	return tenant, nil
}

func applyGatewayTenant(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	var item GatewayTenant
	err := tx.First(&item, "external_tenant_id = ?", event.AggregateID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "tenant", 0, "", err
	}
	if err == nil && item.Version >= event.Version {
		return item.ID, "tenant", item.Version, "ignored_stale", nil
	}
	if item.ID == "" {
		item.ID = NewID("gwt")
		item.ExternalTenantID = event.AggregateID
	}
	if name := payloadString(event.Payload, "name"); name != "" {
		item.Name = name
	}
	item.Status, item.DeletedAt = projectionStatus(event, item.Status, item.DeletedAt, now)
	item.Version, item.SyncedAt = event.Version, now
	if err := tx.Save(&item).Error; err != nil {
		return "", "tenant", 0, "", err
	}
	if item.DeletedAt != nil {
		if err := revokeGatewayTenantModelAccessKeys(tx, item.ExternalTenantID); err != nil {
			return "", "tenant", 0, "", err
		}
	}
	return item.ID, "tenant", item.Version, "applied", nil
}

func applyGatewayOrganization(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	tenant, err := requireGatewayTenant(tx, event.TenantID)
	if err != nil {
		return "", "organization", 0, "", err
	}
	var item GatewayOrganization
	err = tx.First(&item, "tenant_id = ? AND external_organization_id = ?", tenant.ID, event.AggregateID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "organization", 0, "", err
	}
	if err == nil && item.Version >= event.Version {
		return item.ID, "organization", item.Version, "ignored_stale", nil
	}
	parentID := item.ParentID
	if _, supplied := event.Payload["parentExternalId"]; supplied {
		parentID = ""
		if externalParentID := payloadString(event.Payload, "parentExternalId"); externalParentID != "" {
			var parent GatewayOrganization
			if err := tx.First(&parent, "tenant_id = ? AND external_organization_id = ?", tenant.ID, externalParentID).Error; err != nil {
				return "", "organization", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Parent organization projection is not available")
			}
			parentID = parent.ID
		}
	}
	if item.ID == "" {
		item.ID, item.TenantID, item.ExternalOrganizationID = NewID("gwo"), tenant.ID, event.AggregateID
	}
	item.ParentID = parentID
	if name := payloadString(event.Payload, "name"); name != "" {
		item.Name = name
	}
	item.Status, item.DeletedAt = projectionStatus(event, item.Status, item.DeletedAt, now)
	item.Version, item.SyncedAt = event.Version, now
	if err := tx.Save(&item).Error; err != nil {
		return "", "organization", 0, "", err
	}
	if item.DeletedAt != nil {
		if err := revokeGatewayOrganizationModelAccessKeys(tx, tenant.ExternalTenantID, item.ID); err != nil {
			return "", "organization", 0, "", err
		}
	}
	return item.ID, "organization", item.Version, "applied", nil
}

func applyGatewayPrincipal(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	tenant, err := requireGatewayTenant(tx, event.TenantID)
	if err != nil {
		return "", "principal", 0, "", err
	}
	externalPrincipalID := payloadString(event.Payload, "principalExternalId")
	if externalPrincipalID == "" {
		return "", "principal", 0, "", NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Tenant member event requires principalExternalId")
	}
	var item GatewayPrincipal
	err = tx.First(&item, "tenant_id = ? AND external_principal_id = ?", tenant.ID, externalPrincipalID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "principal", 0, "", err
	}
	if err == nil {
		if item.ExternalMembershipID == event.AggregateID && item.Version >= event.Version {
			return item.ID, "principal", item.Version, "ignored_stale", nil
		}
		if item.ExternalMembershipID != event.AggregateID && !item.SourceOccurredAt.IsZero() {
			isEqualTimeReactivation := event.OccurredAt.Equal(item.SourceOccurredAt) && item.Status != StatusActive && payloadString(event.Payload, "status") == StatusActive
			if event.OccurredAt.Before(item.SourceOccurredAt) || event.OccurredAt.Equal(item.SourceOccurredAt) && !isEqualTimeReactivation {
				return item.ID, "principal", item.Version, "ignored_stale", nil
			}
		}
	}
	if item.ID == "" {
		item.ID, item.TenantID, item.ExternalPrincipalID = NewID("gwp"), tenant.ID, externalPrincipalID
	}
	item.ExternalMembershipID = event.AggregateID
	if name := payloadString(event.Payload, "name"); name != "" {
		item.DisplayName = name
	}
	item.Status, item.DeletedAt = projectionStatus(event, item.Status, item.DeletedAt, now)
	item.Version, item.SourceOccurredAt, item.SyncedAt = event.Version, event.OccurredAt, now
	if err := tx.Save(&item).Error; err != nil {
		return "", "principal", 0, "", err
	}
	if item.DeletedAt != nil {
		if err := revokeGatewayPrincipalModelAccessKeys(tx, tenant.ExternalTenantID, item.ExternalPrincipalID); err != nil {
			return "", "principal", 0, "", err
		}
	}
	return item.ID, "principal", item.Version, "applied", nil
}

func applyGatewayOrganizationBinding(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	tenant, err := requireGatewayTenant(tx, event.TenantID)
	if err != nil {
		return "", "organization_membership", 0, "", err
	}
	externalPrincipalID, externalOrganizationID := payloadString(event.Payload, "principalExternalId"), payloadString(event.Payload, "organizationExternalId")
	if externalPrincipalID == "" || externalOrganizationID == "" {
		return "", "organization_membership", 0, "", NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Organization member event requires principal and organization IDs")
	}
	var principal GatewayPrincipal
	if err := tx.First(&principal, "tenant_id = ? AND external_principal_id = ?", tenant.ID, externalPrincipalID).Error; err != nil {
		return "", "organization_membership", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway principal projection is not available")
	}
	var organization GatewayOrganization
	if err := tx.First(&organization, "tenant_id = ? AND external_organization_id = ?", tenant.ID, externalOrganizationID).Error; err != nil {
		return "", "organization_membership", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway organization projection is not available")
	}
	var item GatewayPrincipalOrganizationBinding
	err = tx.First(&item, "tenant_id = ? AND external_membership_id = ?", tenant.ID, event.AggregateID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "organization_membership", 0, "", err
	}
	if err == nil && item.Version >= event.Version {
		return item.ID, "organization_membership", item.Version, "ignored_stale", nil
	}
	if item.ID == "" {
		item.ID, item.TenantID, item.ExternalMembershipID = NewID("gwb"), tenant.ID, event.AggregateID
	}
	item.PrincipalID, item.OrganizationID = principal.ID, organization.ID
	item.Status, item.DeletedAt = projectionStatus(event, item.Status, item.DeletedAt, now)
	item.Version, item.SyncedAt = event.Version, now
	if err := tx.Save(&item).Error; err != nil {
		return "", "organization_membership", 0, "", err
	}
	return item.ID, "organization_membership", item.Version, "applied", nil
}

func applyGatewayProject(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	tenant, err := requireGatewayTenant(tx, event.TenantID)
	if err != nil {
		return "", "project", 0, "", err
	}
	var item GatewayProject
	err = tx.First(&item, "tenant_id = ? AND external_project_id = ?", tenant.ID, event.AggregateID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "project", 0, "", err
	}
	if err == nil && item.Version >= event.Version {
		return item.ID, "project", item.Version, "ignored_stale", nil
	}
	organizationID := item.OrganizationID
	if _, supplied := event.Payload["organizationExternalId"]; supplied {
		organizationID = ""
		if externalID := payloadString(event.Payload, "organizationExternalId"); externalID != "" {
			var organization GatewayOrganization
			if err := tx.First(&organization, "tenant_id = ? AND external_organization_id = ?", tenant.ID, externalID).Error; err != nil {
				return "", "project", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway organization projection is not available")
			}
			organizationID = organization.ID
		}
	}
	ownerID := item.OwnerPrincipalID
	if _, supplied := event.Payload["ownerExternalId"]; supplied {
		ownerID = ""
		if externalID := payloadString(event.Payload, "ownerExternalId"); externalID != "" {
			var owner GatewayPrincipal
			if err := tx.First(&owner, "tenant_id = ? AND external_principal_id = ?", tenant.ID, externalID).Error; err != nil {
				return "", "project", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway owner projection is not available")
			}
			ownerID = owner.ID
		}
	}
	if item.ID == "" {
		item.ID, item.TenantID, item.ExternalProjectID = NewID("gwj"), tenant.ID, event.AggregateID
	}
	item.OrganizationID, item.OwnerPrincipalID = organizationID, ownerID
	if name := payloadString(event.Payload, "name"); name != "" {
		item.Name = name
	}
	item.Status, item.DeletedAt = projectionStatus(event, item.Status, item.DeletedAt, now)
	item.Version, item.SyncedAt = event.Version, now
	if err := tx.Save(&item).Error; err != nil {
		return "", "project", 0, "", err
	}
	if err := syncGatewayServingProject(tx, item, now); err != nil {
		return "", "project", 0, "", err
	}
	return item.ID, "project", item.Version, "applied", nil
}

func syncGatewayServingProject(tx *gorm.DB, projection GatewayProject, now time.Time) error {
	var project Project
	err := tx.First(&project, "id = ?", projection.ID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		project.ID = projection.ID
		project.CreatedAt = now
	}
	project.Name = projection.Name
	project.Status = StatusDisabled
	if projection.Status == StatusActive {
		project.Status = StatusActive
	}
	project.UpdatedAt = now
	return tx.Save(&project).Error
}

func rejectGatewayManagedProjectMutation(db *gorm.DB, projectID string) error {
	var count int64
	if err := db.Model(&GatewayProject{}).Where("id = ?", projectID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return NewHTTPError(http.StatusConflict, "integration_managed_project_read_only", "Integration-managed project is read-only")
	}
	return nil
}

func applyGatewayWorkload(tx *gorm.DB, event GatewayIntegrationEvent, now time.Time) (string, string, int64, string, error) {
	tenant, err := requireGatewayTenant(tx, event.TenantID)
	if err != nil {
		return "", "workload", 0, "", err
	}
	externalProjectID := payloadString(event.Payload, "projectExternalId")
	if externalProjectID == "" {
		return "", "workload", 0, "", NewHTTPError(http.StatusBadRequest, "invalid_integration_event", "Workload event requires projectExternalId")
	}
	var project GatewayProject
	if err := tx.First(&project, "tenant_id = ? AND external_project_id = ?", tenant.ID, externalProjectID).Error; err != nil {
		return "", "workload", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway project projection is not available")
	}
	var item GatewayWorkload
	err = tx.First(&item, "tenant_id = ? AND external_workload_id = ?", tenant.ID, event.AggregateID).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "workload", 0, "", err
	}
	if err == nil && item.Version >= event.Version {
		return item.ID, "workload", item.Version, "ignored_stale", nil
	}
	ownerID := item.OwnerPrincipalID
	if _, supplied := event.Payload["ownerExternalId"]; supplied {
		ownerID = ""
		if externalID := payloadString(event.Payload, "ownerExternalId"); externalID != "" {
			var owner GatewayPrincipal
			if err := tx.First(&owner, "tenant_id = ? AND external_principal_id = ?", tenant.ID, externalID).Error; err != nil {
				return "", "workload", 0, "", NewHTTPError(http.StatusConflict, "integration_dependency_missing", "Gateway owner projection is not available")
			}
			ownerID = owner.ID
		}
	}
	if item.ID == "" {
		item.ID, item.TenantID, item.ExternalWorkloadID = NewID("gww"), tenant.ID, event.AggregateID
	}
	item.ProjectID, item.OwnerPrincipalID = project.ID, ownerID
	if name := payloadString(event.Payload, "name"); name != "" {
		item.Name = name
	}
	if workloadType := payloadString(event.Payload, "workloadType"); workloadType != "" {
		item.WorkloadType = workloadType
	}
	if environment := payloadString(event.Payload, "environment"); environment != "" {
		item.Environment = environment
	}
	item.Status, item.DeletedAt = projectionStatus(event, item.Status, item.DeletedAt, now)
	item.Version, item.SyncedAt = event.Version, now
	if err := tx.Save(&item).Error; err != nil {
		return "", "workload", 0, "", err
	}
	if item.DeletedAt != nil {
		if err := revokeGatewayWorkloadModelAccessKeys(tx, tenant.ExternalTenantID, item.ExternalWorkloadID); err != nil {
			return "", "workload", 0, "", err
		}
	}
	return item.ID, "workload", item.Version, "applied", nil
}
