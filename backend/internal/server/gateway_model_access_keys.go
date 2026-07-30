package server

import (
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
	gatewayModelAccessKeyManagedBy     = "integration"
	gatewayModelAccessKeyStatusExpired = "expired"
)

type GatewayModelAccessKeyCreateInput struct {
	RequestID           string      `json:"request_id"`
	TenantExternalID    string      `json:"tenant_id"`
	ProjectExternalID   string      `json:"project_id"`
	PrincipalType       string      `json:"principal_type"`
	PrincipalExternalID string      `json:"principal_id"`
	Name                string      `json:"name"`
	Group               string      `json:"group,omitempty"`
	Environment         string      `json:"environment,omitempty"`
	AllowedModels       []string    `json:"allowed_models,omitempty"`
	IPAllowlist         []string    `json:"ip_allowlist,omitempty"`
	Limits              QuotaLimits `json:"limits,omitempty"`
	ExpiresAt           *time.Time  `json:"expires_at,omitempty"`
	RequestedBy         string      `json:"requested_by,omitempty"`
}

type GatewayModelAccessKeyCreateResult struct {
	Key     APIKey
	Secret  string
	Created bool
}

type GatewayModelAccessKeyFilter struct {
	TenantExternalID    string
	ProjectExternalID   string
	PrincipalType       string
	PrincipalExternalID string
	Name                string
	Status              string
	Page                int
	PageSize            int
}

type GatewayModelAccessKeyPage struct {
	Items    []APIKey `json:"data"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
	Total    int64    `json:"total"`
}

type gatewayModelAccessKeyCreateResponse struct {
	Data                 APIKey `json:"data"`
	APIKey               string `json:"api_key,omitempty"`
	PlainTextVisibleOnce bool   `json:"plain_text_visible_once"`
	IdempotentReplay     bool   `json:"idempotent_replay,omitempty"`
}

type gatewayModelAccessKeyRevealResponse struct {
	APIKey string `json:"api_key"`
}

func (s *Server) handleGatewayModelAccessKeys(w http.ResponseWriter, r *http.Request) {
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, err := positiveQueryInt(r.URL.Query().Get("page"), 1, 10_000)
		if err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_model_access_key_query", "Page is invalid"))
			return
		}
		pageSize, err := positiveQueryInt(r.URL.Query().Get("page_size"), 20, 100)
		if err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_model_access_key_query", "Page size is invalid"))
			return
		}
		filter := GatewayModelAccessKeyFilter{
			TenantExternalID:    r.URL.Query().Get("tenant_id"),
			ProjectExternalID:   r.URL.Query().Get("project_id"),
			PrincipalType:       r.URL.Query().Get("principal_type"),
			PrincipalExternalID: r.URL.Query().Get("principal_id"),
			Name:                r.URL.Query().Get("name"),
			Status:              r.URL.Query().Get("status"),
			Page:                page,
			PageSize:            pageSize,
		}
		result, err := s.store.ListGatewayModelAccessKeys(filter)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		var input GatewayModelAccessKeyCreateInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
			return
		}
		result, err := s.store.CreateGatewayModelAccessKey(input)
		if err != nil {
			writeError(w, r, err)
			return
		}
		status := http.StatusCreated
		if !result.Created {
			status = http.StatusOK
		}
		writeJSON(w, status, gatewayModelAccessKeyCreateResponse{
			Data:                 result.Key,
			APIKey:               result.Secret,
			PlainTextVisibleOnce: result.Created,
			IdempotentReplay:     !result.Created,
		})
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleGatewayModelAccessKeyItem(w http.ResponseWriter, r *http.Request) {
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/internal/model-access-keys/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "revoke" && parts[1] != "reveal") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	var input struct {
		TenantExternalID string `json:"tenant_id"`
		PrincipalType    string `json:"principal_type,omitempty"`
		PrincipalID      string `json:"principal_id,omitempty"`
		Reason           string `json:"reason,omitempty"`
		RequestedBy      string `json:"requested_by,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error()))
		return
	}
	if parts[1] == "reveal" {
		secret, err := s.store.RevealGatewayModelAccessKey(parts[0], input.TenantExternalID, input.PrincipalType, input.PrincipalID, input.RequestedBy)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, gatewayModelAccessKeyRevealResponse{APIKey: secret})
		return
	}
	key, err := s.store.RevokeGatewayModelAccessKey(parts[0], input.TenantExternalID, input.PrincipalType, input.PrincipalID, input.Reason, input.RequestedBy)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": key})
}

func (s *GormStore) CreateGatewayModelAccessKey(input GatewayModelAccessKeyCreateInput) (GatewayModelAccessKeyCreateResult, error) {
	normalized, digest, err := normalizeGatewayModelAccessKeyCreateInput(input)
	if err != nil {
		return GatewayModelAccessKeyCreateResult{}, err
	}
	controlRequestID := gatewayModelAccessKeyControlRequestID(normalized.TenantExternalID, normalized.RequestID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if result, found, err := findGatewayModelAccessKeyRequest(s.db, controlRequestID, digest); found || err != nil {
		return result, err
	}
	if err := validateNewGatewayModelAccessKeyInput(normalized, time.Now().UTC()); err != nil {
		return GatewayModelAccessKeyCreateResult{}, err
	}

	rawSecret := s.generateAPIKeySecret()
	prefix, suffix := PrefixSuffix(rawSecret)
	keyCiphertext := s.encryptSecret(rawSecret)
	if !strings.HasPrefix(keyCiphertext, "enc:v1:") {
		return GatewayModelAccessKeyCreateResult{}, NewHTTPError(http.StatusInternalServerError, "model_access_key_encryption_failed", "Model access key encryption failed")
	}
	now := time.Now().UTC()
	created := false
	key := APIKey{
		ID:                   NewID("key"),
		TenantExternalID:     normalized.TenantExternalID,
		ProjectExternalID:    normalized.ProjectExternalID,
		PrincipalType:        normalized.PrincipalType,
		PrincipalExternalID:  normalized.PrincipalExternalID,
		Environment:          normalized.Environment,
		ManagedBy:            gatewayModelAccessKeyManagedBy,
		ControlRequestID:     &controlRequestID,
		ControlRequestDigest: digest,
		Name:                 normalized.Name,
		Group:                normalized.Group,
		KeyHash:              HashSecret(rawSecret),
		KeyCiphertext:        keyCiphertext,
		KeyPrefix:            prefix,
		KeySuffix:            suffix,
		Allowed:              normalized.AllowedModels,
		IPAllowlist:          normalized.IPAllowlist,
		Limits:               normalized.Limits,
		Status:               StatusActive,
		ExpiresAt:            normalized.ExpiresAt,
		CreatedAt:            now,
		Metadata: map[string]string{
			"managed_by":   gatewayModelAccessKeyManagedBy,
			"requested_by": normalized.RequestedBy,
		},
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if result, found, findErr := findGatewayModelAccessKeyRequest(tx, controlRequestID, digest); found || findErr != nil {
			if findErr != nil {
				return findErr
			}
			key = result.Key
			return nil
		}

		tenant, project, resolveErr := resolveGatewayModelAccessKeyScope(tx, normalized)
		if resolveErr != nil {
			return resolveErr
		}
		key.ProjectID = project.ID
		if err := validateGatewayModelAccessKeyPrincipal(tx, tenant, project, normalized); err != nil {
			return err
		}
		if err := syncGatewayServingProject(tx, project, now); err != nil {
			return err
		}
		if err := tx.Create(&key).Error; err != nil {
			return writeConflict(err, "model_access_key_conflict", "Model access key request conflicts with an existing request")
		}
		created = true
		return tx.Create(&AuditEvent{
			ID:           NewID("audit"),
			ActorUserID:  normalized.RequestedBy,
			Action:       "gateway_model_access_key_create",
			ResourceType: "api_key",
			ResourceID:   key.ID,
			Status:       "success",
			Message:      "Model access key created through the integration API",
			CreatedAt:    now,
		}).Error
	})
	if err != nil {
		if result, found, findErr := findGatewayModelAccessKeyRequest(s.db, controlRequestID, digest); found || findErr != nil {
			return result, findErr
		}
		return GatewayModelAccessKeyCreateResult{}, err
	}
	if !created {
		return GatewayModelAccessKeyCreateResult{Key: publicKey(key), Created: false}, nil
	}
	return GatewayModelAccessKeyCreateResult{Key: publicKey(key), Secret: rawSecret, Created: true}, nil
}

const gatewayModelAccessKeyEffectiveStatusExpression = `(CASE
	WHEN api_keys.status <> 'active' THEN api_keys.status
	WHEN api_keys.expires_at IS NOT NULL AND api_keys.expires_at <= ? THEN 'expired'
	WHEN NOT EXISTS (
		SELECT 1 FROM gateway_tenants gt
		WHERE gt.external_tenant_id = api_keys.tenant_external_id AND gt.status = 'active'
	) THEN 'disabled'
	WHEN NOT EXISTS (
		SELECT 1 FROM gateway_projects gp
		JOIN gateway_tenants gt ON gt.id = gp.tenant_id
		WHERE gp.id = api_keys.project_id AND gp.status = 'active'
			AND gt.external_tenant_id = api_keys.tenant_external_id AND gt.status = 'active'
	) THEN 'disabled'
	WHEN api_keys.principal_type = 'user' AND NOT EXISTS (
		SELECT 1 FROM gateway_principals gpr
		JOIN gateway_tenants gt ON gt.id = gpr.tenant_id
		WHERE gpr.external_principal_id = api_keys.principal_external_id AND gpr.status = 'active'
			AND gt.external_tenant_id = api_keys.tenant_external_id AND gt.status = 'active'
	) THEN 'disabled'
	WHEN api_keys.principal_type <> 'user' AND NOT EXISTS (
		SELECT 1 FROM gateway_workloads gw
		JOIN gateway_tenants gt ON gt.id = gw.tenant_id
		WHERE gw.external_workload_id = api_keys.principal_external_id
			AND gw.project_id = api_keys.project_id AND gw.status = 'active'
			AND gw.workload_type = api_keys.principal_type
			AND gt.external_tenant_id = api_keys.tenant_external_id AND gt.status = 'active'
	) THEN 'disabled'
	ELSE 'active'
END)`

func (s *GormStore) ListGatewayModelAccessKeys(filter GatewayModelAccessKeyFilter) (GatewayModelAccessKeyPage, error) {
	tenantID := strings.TrimSpace(filter.TenantExternalID)
	if tenantID == "" {
		return GatewayModelAccessKeyPage{}, NewHTTPError(http.StatusBadRequest, "tenant_required", "Tenant ID is required")
	}
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 || page > 10_000 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	query := s.db.Model(&APIKey{}).Where("managed_by = ? AND tenant_external_id = ?", gatewayModelAccessKeyManagedBy, tenantID)
	if projectID := strings.TrimSpace(filter.ProjectExternalID); projectID != "" {
		query = query.Where("project_external_id = ?", projectID)
	}
	if principalType := strings.TrimSpace(filter.PrincipalType); principalType != "" {
		query = query.Where("principal_type = ?", principalType)
	}
	if principalID := strings.TrimSpace(filter.PrincipalExternalID); principalID != "" {
		query = query.Where("principal_external_id = ?", principalID)
	}
	if name := strings.TrimSpace(filter.Name); name != "" {
		query = query.Where("LOWER(name) LIKE ? ESCAPE '\\'", gatewayModelAccessKeyNamePattern(name))
	}
	now := time.Now().UTC()
	if statusFilter := strings.TrimSpace(filter.Status); statusFilter != "" {
		query = query.Where(gatewayModelAccessKeyEffectiveStatusExpression+" = ?", now, statusFilter)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return GatewayModelAccessKeyPage{}, err
	}
	var items []APIKey
	if err := query.Order("created_at desc, id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return GatewayModelAccessKeyPage{}, err
	}
	if err := hydrateGatewayModelAccessKeyEffectiveStatuses(s.db, items, now); err != nil {
		return GatewayModelAccessKeyPage{}, err
	}
	for i := range items {
		hydrateAPIKey(&items[i])
		items[i] = publicKey(items[i])
	}
	return GatewayModelAccessKeyPage{Items: items, Page: page, PageSize: pageSize, Total: total}, nil
}

func hydrateGatewayModelAccessKeyEffectiveStatuses(db *gorm.DB, items []APIKey, now time.Time) error {
	if len(items) == 0 {
		return nil
	}
	tenantExternalIDs := make([]string, 0, len(items))
	projectIDs := make([]string, 0, len(items))
	principalExternalIDs := make([]string, 0, len(items))
	workloadExternalIDs := make([]string, 0, len(items))
	for _, key := range items {
		tenantExternalIDs = append(tenantExternalIDs, key.TenantExternalID)
		projectIDs = append(projectIDs, key.ProjectID)
		if key.PrincipalType == "user" {
			principalExternalIDs = append(principalExternalIDs, key.PrincipalExternalID)
		} else {
			workloadExternalIDs = append(workloadExternalIDs, key.PrincipalExternalID)
		}
	}

	var tenants []GatewayTenant
	if err := db.Where("external_tenant_id IN ?", normalizedUniqueStrings(tenantExternalIDs)).Find(&tenants).Error; err != nil {
		return err
	}
	var projects []GatewayProject
	if err := db.Where("id IN ?", normalizedUniqueStrings(projectIDs)).Find(&projects).Error; err != nil {
		return err
	}
	var principals []GatewayPrincipal
	if len(principalExternalIDs) > 0 {
		if err := db.Where("external_principal_id IN ?", normalizedUniqueStrings(principalExternalIDs)).Find(&principals).Error; err != nil {
			return err
		}
	}
	var workloads []GatewayWorkload
	if len(workloadExternalIDs) > 0 {
		if err := db.Where("external_workload_id IN ?", normalizedUniqueStrings(workloadExternalIDs)).Find(&workloads).Error; err != nil {
			return err
		}
	}

	tenantByExternalID := make(map[string]GatewayTenant, len(tenants))
	for _, tenant := range tenants {
		tenantByExternalID[tenant.ExternalTenantID] = tenant
	}
	projectByID := make(map[string]GatewayProject, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	principalByScope := make(map[string]GatewayPrincipal, len(principals))
	for _, principal := range principals {
		principalByScope[gatewayModelAccessKeyScopeID(principal.TenantID, principal.ExternalPrincipalID)] = principal
	}
	workloadByScope := make(map[string]GatewayWorkload, len(workloads))
	for _, workload := range workloads {
		workloadByScope[gatewayModelAccessKeyScopeID(workload.TenantID, workload.ExternalWorkloadID)] = workload
	}

	for i := range items {
		key := &items[i]
		if key.Status != StatusActive {
			continue
		}
		if key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
			key.Status = gatewayModelAccessKeyStatusExpired
			continue
		}
		tenant, ok := tenantByExternalID[key.TenantExternalID]
		if !ok || tenant.Status != StatusActive {
			key.Status = StatusDisabled
			continue
		}
		project, ok := projectByID[key.ProjectID]
		if !ok || project.TenantID != tenant.ID || project.Status != StatusActive {
			key.Status = StatusDisabled
			continue
		}
		if key.PrincipalType == "user" {
			principal, found := principalByScope[gatewayModelAccessKeyScopeID(tenant.ID, key.PrincipalExternalID)]
			if !found || principal.Status != StatusActive {
				key.Status = StatusDisabled
			}
			continue
		}
		workload, found := workloadByScope[gatewayModelAccessKeyScopeID(tenant.ID, key.PrincipalExternalID)]
		if !found || workload.ProjectID != project.ID || workload.Status != StatusActive || workload.WorkloadType != key.PrincipalType {
			key.Status = StatusDisabled
		}
	}
	return nil
}

func gatewayModelAccessKeyScopeID(parentID string, externalID string) string {
	return parentID + "\x00" + externalID
}

func gatewayModelAccessKeyNamePattern(name string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(strings.ToLower(strings.TrimSpace(name)))
	return "%" + escaped + "%"
}

func (s *GormStore) RevealGatewayModelAccessKey(id string, tenantExternalID string, principalType string, principalID string, requestedBy string) (string, error) {
	id = strings.TrimSpace(id)
	tenantExternalID = strings.TrimSpace(tenantExternalID)
	principalType = strings.TrimSpace(principalType)
	principalID = strings.TrimSpace(principalID)
	requestedBy = strings.TrimSpace(requestedBy)
	if id == "" || tenantExternalID == "" || principalType == "" || principalID == "" {
		return "", NewHTTPError(http.StatusBadRequest, "invalid_request", "Key, tenant, and principal are required")
	}

	var secret string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var key APIKey
		if err := tx.First(&key, "id = ? AND managed_by = ? AND tenant_external_id = ? AND principal_type = ? AND principal_external_id = ?", id, gatewayModelAccessKeyManagedBy, tenantExternalID, principalType, principalID).Error; err != nil {
			return notFound(err, "model_access_key_not_found", "Model access key not found")
		}
		if gatewayModelAccessKeyEffectiveStatus(tx, key) != StatusActive {
			return NewHTTPError(http.StatusConflict, "model_access_key_unavailable", "Model access key is not active")
		}
		if !strings.HasPrefix(key.KeyCiphertext, "enc:v1:") {
			return NewHTTPError(http.StatusConflict, "model_access_key_secret_unavailable", "Model access key secret is unavailable")
		}
		secret = s.decryptSecret(key.KeyCiphertext)
		if strings.TrimSpace(secret) == "" {
			return NewHTTPError(http.StatusConflict, "model_access_key_secret_unavailable", "Model access key secret is unavailable")
		}
		return tx.Create(&AuditEvent{
			ID:           NewID("audit"),
			ActorUserID:  requestedBy,
			Action:       "gateway_model_access_key_reveal",
			ResourceType: "api_key",
			ResourceID:   key.ID,
			Status:       "success",
			Message:      "Model access key revealed through the integration API",
			CreatedAt:    time.Now().UTC(),
		}).Error
	})
	return secret, err
}

func (s *GormStore) RevokeGatewayModelAccessKey(id string, tenantExternalID string, principalType string, principalID string, reason string, requestedBy string) (APIKey, error) {
	id = strings.TrimSpace(id)
	tenantExternalID = strings.TrimSpace(tenantExternalID)
	principalType = strings.TrimSpace(principalType)
	principalID = strings.TrimSpace(principalID)
	requestedBy = strings.TrimSpace(requestedBy)
	if id == "" || tenantExternalID == "" {
		return APIKey{}, NewHTTPError(http.StatusBadRequest, "invalid_request", "Key ID and tenant ID are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var key APIKey
	now := time.Now().UTC()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("id = ? AND managed_by = ? AND tenant_external_id = ?", id, gatewayModelAccessKeyManagedBy, tenantExternalID)
		if principalType != "" || principalID != "" {
			if principalType == "" || principalID == "" {
				return NewHTTPError(http.StatusBadRequest, "invalid_request", "Principal type and principal ID must be supplied together")
			}
			query = query.Where("principal_type = ? AND principal_external_id = ?", principalType, principalID)
		}
		if err := query.First(&key).Error; err != nil {
			return notFound(err, "model_access_key_not_found", "Model access key not found")
		}
		if key.Status == StatusRevoked {
			return nil
		}
		key.Status = StatusRevoked
		key.GraceUntil = nil
		if key.Metadata == nil {
			key.Metadata = map[string]string{}
		}
		key.Metadata["revoked_reason"] = strings.TrimSpace(reason)
		if err := tx.Save(&key).Error; err != nil {
			return err
		}
		return tx.Create(&AuditEvent{
			ID:           NewID("audit"),
			ActorUserID:  requestedBy,
			Action:       "gateway_model_access_key_revoke",
			ResourceType: "api_key",
			ResourceID:   key.ID,
			Status:       "success",
			Message:      "Model access key revoked through the integration API",
			CreatedAt:    now,
		}).Error
	})
	return publicKey(key), err
}

func normalizeGatewayModelAccessKeyCreateInput(input GatewayModelAccessKeyCreateInput) (GatewayModelAccessKeyCreateInput, string, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.TenantExternalID = strings.TrimSpace(input.TenantExternalID)
	input.ProjectExternalID = strings.TrimSpace(input.ProjectExternalID)
	input.PrincipalType = strings.ToLower(strings.TrimSpace(input.PrincipalType))
	input.PrincipalExternalID = strings.TrimSpace(input.PrincipalExternalID)
	input.Name = strings.TrimSpace(input.Name)
	input.Group = strings.TrimSpace(input.Group)
	input.Environment = strings.TrimSpace(input.Environment)
	input.RequestedBy = strings.TrimSpace(input.RequestedBy)
	input.AllowedModels = normalizedUniqueStrings(input.AllowedModels)
	input.IPAllowlist = normalizedUniqueStrings(input.IPAllowlist)
	if input.Group == "" {
		input.Group = "default"
	}
	if input.RequestID == "" || input.TenantExternalID == "" || input.ProjectExternalID == "" || input.PrincipalExternalID == "" || input.Name == "" {
		return GatewayModelAccessKeyCreateInput{}, "", NewHTTPError(http.StatusBadRequest, "invalid_model_access_key", "Request, tenant, project, principal, and name are required")
	}
	switch input.PrincipalType {
	case "user", "application", "agent", "service_account":
	default:
		return GatewayModelAccessKeyCreateInput{}, "", NewHTTPError(http.StatusBadRequest, "invalid_model_access_key", "Principal type is not supported")
	}
	if input.ExpiresAt != nil {
		expiresAt := input.ExpiresAt.UTC()
		input.ExpiresAt = &expiresAt
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return GatewayModelAccessKeyCreateInput{}, "", err
	}
	digestBytes := sha256.Sum256(payload)
	return input, hex.EncodeToString(digestBytes[:]), nil
}

func validateNewGatewayModelAccessKeyInput(input GatewayModelAccessKeyCreateInput, now time.Time) error {
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return NewHTTPError(http.StatusBadRequest, "invalid_model_access_key", "Expiration must be in the future")
	}
	return nil
}

func gatewayModelAccessKeyControlRequestID(tenantID string, requestID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(requestID)))
	return hex.EncodeToString(sum[:])
}

func normalizedUniqueStrings(values []string) []string {
	unique := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func findGatewayModelAccessKeyRequest(db *gorm.DB, controlRequestID string, digest string) (GatewayModelAccessKeyCreateResult, bool, error) {
	var existing APIKey
	err := db.First(&existing, "control_request_id = ?", controlRequestID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return GatewayModelAccessKeyCreateResult{}, false, nil
	}
	if err != nil {
		return GatewayModelAccessKeyCreateResult{}, false, err
	}
	if existing.ControlRequestDigest != digest {
		return GatewayModelAccessKeyCreateResult{}, true, NewHTTPError(http.StatusConflict, "model_access_key_request_conflict", "Request ID was reused with different content")
	}
	existing.Status = gatewayModelAccessKeyEffectiveStatus(db, existing)
	return GatewayModelAccessKeyCreateResult{Key: publicKey(existing), Created: false}, true, nil
}

func rejectGatewayManagedAPIKeyMutation(key APIKey) error {
	if key.ManagedBy == gatewayModelAccessKeyManagedBy {
		return NewHTTPError(http.StatusConflict, "integration_managed_key_read_only", "Integration-managed model access key is read-only")
	}
	return nil
}

func resolveGatewayModelAccessKeyScope(db *gorm.DB, input GatewayModelAccessKeyCreateInput) (GatewayTenant, GatewayProject, error) {
	var tenant GatewayTenant
	if err := db.First(&tenant, "external_tenant_id = ? AND status = ?", input.TenantExternalID, StatusActive).Error; err != nil {
		return GatewayTenant{}, GatewayProject{}, NewHTTPError(http.StatusConflict, "gateway_tenant_unavailable", "Gateway tenant projection is not active")
	}
	var project GatewayProject
	if err := db.First(&project, "tenant_id = ? AND external_project_id = ? AND status = ?", tenant.ID, input.ProjectExternalID, StatusActive).Error; err != nil {
		return GatewayTenant{}, GatewayProject{}, NewHTTPError(http.StatusConflict, "gateway_project_unavailable", "Gateway project projection is not active")
	}
	if project.OrganizationID != "" {
		var organization GatewayOrganization
		if err := db.First(&organization, "id = ? AND tenant_id = ? AND status = ?", project.OrganizationID, tenant.ID, StatusActive).Error; err != nil {
			return GatewayTenant{}, GatewayProject{}, NewHTTPError(http.StatusConflict, "gateway_organization_unavailable", "Gateway project organization is not active")
		}
	}
	return tenant, project, nil
}

func validateGatewayModelAccessKeyPrincipal(db *gorm.DB, tenant GatewayTenant, project GatewayProject, input GatewayModelAccessKeyCreateInput) error {
	if input.PrincipalType == "user" {
		var principal GatewayPrincipal
		if err := db.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ?", tenant.ID, input.PrincipalExternalID, StatusActive).Error; err != nil {
			return NewHTTPError(http.StatusConflict, "gateway_principal_unavailable", "Gateway principal projection is not active")
		}
		if project.OrganizationID != "" {
			var binding GatewayPrincipalOrganizationBinding
			if err := db.First(&binding, "tenant_id = ? AND principal_id = ? AND organization_id = ? AND status = ?", tenant.ID, principal.ID, project.OrganizationID, StatusActive).Error; err != nil {
				return NewHTTPError(http.StatusConflict, "gateway_organization_membership_unavailable", "Gateway principal is not active in the project organization")
			}
		}
		return nil
	}
	var workload GatewayWorkload
	if err := db.First(&workload, "tenant_id = ? AND external_workload_id = ? AND project_id = ? AND workload_type = ? AND status = ?", tenant.ID, input.PrincipalExternalID, project.ID, input.PrincipalType, StatusActive).Error; err != nil {
		return NewHTTPError(http.StatusConflict, "gateway_workload_unavailable", "Gateway workload projection is not active for the project")
	}
	return nil
}

func gatewayModelAccessKeyEffectiveStatus(db *gorm.DB, key APIKey) string {
	if key.Status != StatusActive || key.ManagedBy != gatewayModelAccessKeyManagedBy {
		return key.Status
	}
	if key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now().UTC()) {
		return gatewayModelAccessKeyStatusExpired
	}
	if validateGatewayModelAccessKeyEffectiveScope(db, key) != nil {
		return StatusDisabled
	}
	return StatusActive
}

func validateGatewayModelAccessKeyEffectiveScope(db *gorm.DB, key APIKey) error {
	var tenant GatewayTenant
	if err := db.First(&tenant, "external_tenant_id = ? AND status = ?", key.TenantExternalID, StatusActive).Error; err != nil {
		return ErrAPIKeyDisabled
	}
	var project GatewayProject
	if err := db.First(&project, "id = ? AND tenant_id = ? AND status = ?", key.ProjectID, tenant.ID, StatusActive).Error; err != nil {
		return ErrAPIKeyDisabled
	}
	if project.OrganizationID != "" {
		var organization GatewayOrganization
		if err := db.First(&organization, "id = ? AND tenant_id = ? AND status = ?", project.OrganizationID, tenant.ID, StatusActive).Error; err != nil {
			return ErrAPIKeyDisabled
		}
	}
	if key.PrincipalType == "user" {
		var principal GatewayPrincipal
		if err := db.First(&principal, "tenant_id = ? AND external_principal_id = ? AND status = ?", tenant.ID, key.PrincipalExternalID, StatusActive).Error; err != nil {
			return ErrAPIKeyDisabled
		}
		if project.OrganizationID != "" {
			var binding GatewayPrincipalOrganizationBinding
			if err := db.First(&binding, "tenant_id = ? AND principal_id = ? AND organization_id = ? AND status = ?", tenant.ID, principal.ID, project.OrganizationID, StatusActive).Error; err != nil {
				return ErrAPIKeyDisabled
			}
		}
		return nil
	}
	var workload GatewayWorkload
	if err := db.First(&workload, "tenant_id = ? AND external_workload_id = ? AND project_id = ? AND workload_type = ? AND status = ?", tenant.ID, key.PrincipalExternalID, project.ID, key.PrincipalType, StatusActive).Error; err != nil {
		return ErrAPIKeyDisabled
	}
	return nil
}

func revokeGatewayTenantModelAccessKeys(db *gorm.DB, tenantExternalID string) error {
	return revokeGatewayModelAccessKeyQuery(db.Model(&APIKey{}).
		Where("managed_by = ? AND tenant_external_id = ?", gatewayModelAccessKeyManagedBy, tenantExternalID))
}

func revokeGatewayPrincipalModelAccessKeys(db *gorm.DB, tenantExternalID string, principalExternalID string) error {
	return revokeGatewayModelAccessKeyQuery(db.Model(&APIKey{}).
		Where("managed_by = ? AND tenant_external_id = ? AND principal_type = ? AND principal_external_id = ?",
			gatewayModelAccessKeyManagedBy, tenantExternalID, "user", principalExternalID))
}

func revokeGatewayWorkloadModelAccessKeys(db *gorm.DB, tenantExternalID string, workloadExternalID string) error {
	return revokeGatewayModelAccessKeyQuery(db.Model(&APIKey{}).
		Where("managed_by = ? AND tenant_external_id = ? AND principal_type <> ? AND principal_external_id = ?",
			gatewayModelAccessKeyManagedBy, tenantExternalID, "user", workloadExternalID))
}

func revokeGatewayOrganizationModelAccessKeys(db *gorm.DB, tenantExternalID string, organizationID string) error {
	projects := db.Model(&GatewayProject{}).Select("id").Where("organization_id = ?", organizationID)
	return revokeGatewayModelAccessKeyQuery(db.Model(&APIKey{}).
		Where("managed_by = ? AND tenant_external_id = ? AND project_id IN (?)", gatewayModelAccessKeyManagedBy, tenantExternalID, projects))
}

func revokeGatewayModelAccessKeyQuery(query *gorm.DB) error {
	return query.Where("status <> ?", StatusRevoked).Update("status", StatusRevoked).Error
}
