package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type GatewayRequestLogFilter struct {
	TenantExternalID       string
	RequestID              string
	Principal              string
	ProjectExternalID      string
	OrganizationExternalID string
	Model                  string
	Outcome                string
	DateFrom               *time.Time
	DateTo                 *time.Time
	Page                   int
	PageSize               int
}

type GatewayRequestLogItem struct {
	RequestID              string    `json:"request_id"`
	PrincipalType          string    `json:"principal_type"`
	PrincipalExternalID    string    `json:"principal_id"`
	PrincipalName          string    `json:"principal_name,omitempty"`
	ProjectExternalID      string    `json:"project_id"`
	ProjectName            string    `json:"project_name,omitempty"`
	OrganizationExternalID string    `json:"organization_id,omitempty"`
	OrganizationName       string    `json:"organization_name,omitempty"`
	Model                  string    `json:"model"`
	ProviderID             string    `json:"provider_id,omitempty"`
	InputTokens            int64     `json:"input_tokens"`
	OutputTokens           int64     `json:"output_tokens"`
	TotalTokens            int64     `json:"total_tokens"`
	EstimatedCostUSD       float64   `json:"estimated_cost_usd"`
	StatusCode             int       `json:"status_code"`
	ErrorCode              string    `json:"error_code,omitempty"`
	LatencyMS              int64     `json:"latency_ms"`
	CreatedAt              time.Time `json:"created_at"`
}

type GatewayRequestLogPage struct {
	Items    []GatewayRequestLogItem `json:"items"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Total    int64                   `json:"total"`
}

func (s *Server) handleGatewayRequestLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	query := r.URL.Query()
	page, err := positiveQueryInt(query.Get("page"), 1, 10_000)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request_log_query", "Page is invalid"))
		return
	}
	pageSize, err := positiveQueryInt(query.Get("page_size"), 20, 100)
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request_log_query", "Page size is invalid"))
		return
	}
	dateFrom, err := optionalRFC3339(query.Get("date_from"))
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request_log_query", "Date from is invalid"))
		return
	}
	dateTo, err := optionalRFC3339(query.Get("date_to"))
	if err != nil || dateFrom != nil && dateTo != nil && dateFrom.After(*dateTo) {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request_log_query", "Date to is invalid"))
		return
	}
	filter := GatewayRequestLogFilter{
		TenantExternalID:       query.Get("tenant_id"),
		RequestID:              query.Get("request_id"),
		Principal:              query.Get("principal"),
		ProjectExternalID:      query.Get("project_id"),
		OrganizationExternalID: query.Get("organization_id"),
		Model:                  query.Get("model"),
		Outcome:                query.Get("outcome"),
		DateFrom:               dateFrom,
		DateTo:                 dateTo,
		Page:                   page,
		PageSize:               pageSize,
	}
	if filter.Outcome != "" && filter.Outcome != "success" && filter.Outcome != "error" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_request_log_query", "Outcome is invalid"))
		return
	}
	result, err := s.store.ListGatewayRequestLogs(filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func positiveQueryInt(value string, fallback int, maximum int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, NewHTTPError(http.StatusBadRequest, "invalid_pagination", "Pagination value is invalid")
	}
	return parsed, nil
}

func optionalRFC3339(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (s *GormStore) ListGatewayRequestLogs(filter GatewayRequestLogFilter) (GatewayRequestLogPage, error) {
	tenantID := strings.TrimSpace(filter.TenantExternalID)
	if tenantID == "" {
		return GatewayRequestLogPage{}, NewHTTPError(http.StatusBadRequest, "tenant_required", "Tenant ID is required")
	}
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	base := s.db.Table("request_logs AS rl").
		Joins("JOIN api_keys AS ak ON ak.id = rl.api_key_id AND ak.managed_by = ? AND ak.tenant_external_id = ?", gatewayModelAccessKeyManagedBy, tenantID).
		Joins("LEFT JOIN gateway_projects AS gp ON gp.id = rl.project_id").
		Joins("LEFT JOIN gateway_organizations AS go ON go.id = gp.organization_id").
		Joins("LEFT JOIN gateway_principals AS gpr ON ak.principal_type = ? AND gpr.tenant_id = gp.tenant_id AND gpr.external_principal_id = ak.principal_external_id", "user").
		Joins("LEFT JOIN gateway_workloads AS gw ON ak.principal_type <> ? AND gw.tenant_id = gp.tenant_id AND gw.external_workload_id = ak.principal_external_id", "user")

	if value := strings.TrimSpace(filter.RequestID); value != "" {
		base = base.Where("LOWER(rl.request_id) LIKE ? ESCAPE '\\'", gatewayRequestLogPattern(value))
	}
	if value := strings.TrimSpace(filter.Principal); value != "" {
		pattern := gatewayRequestLogPattern(value)
		base = base.Where("LOWER(ak.principal_external_id) LIKE ? ESCAPE '\\' OR LOWER(COALESCE(gpr.display_name, gw.name, '')) LIKE ? ESCAPE '\\'", pattern, pattern)
	}
	if value := strings.TrimSpace(filter.ProjectExternalID); value != "" {
		base = base.Where("ak.project_external_id = ?", value)
	}
	if value := strings.TrimSpace(filter.OrganizationExternalID); value != "" {
		base = base.Where("go.external_organization_id = ?", value)
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		base = base.Where("LOWER(rl.model_name) LIKE ? ESCAPE '\\'", gatewayRequestLogPattern(value))
	}
	switch strings.TrimSpace(filter.Outcome) {
	case "success":
		base = base.Where("rl.status_code < 400")
	case "error":
		base = base.Where("rl.status_code >= 400")
	}
	if filter.DateFrom != nil {
		base = base.Where("rl.created_at >= ?", *filter.DateFrom)
	}
	if filter.DateTo != nil {
		base = base.Where("rl.created_at <= ?", *filter.DateTo)
	}

	var total int64
	if err := base.Distinct("rl.id").Count(&total).Error; err != nil {
		return GatewayRequestLogPage{}, err
	}
	items := make([]GatewayRequestLogItem, 0)
	err := base.
		Joins("LEFT JOIN usage_records AS ur ON ur.request_id = rl.request_id").
		Select(`rl.request_id,
			ak.principal_type,
			ak.principal_external_id,
			COALESCE(gpr.display_name, gw.name, '') AS principal_name,
			ak.project_external_id,
			COALESCE(gp.name, '') AS project_name,
			COALESCE(go.external_organization_id, '') AS organization_external_id,
			COALESCE(go.name, '') AS organization_name,
			rl.model_name AS model,
			rl.provider_id,
			COALESCE(SUM(ur.input_tokens), 0) AS input_tokens,
			COALESCE(SUM(ur.output_tokens), 0) AS output_tokens,
			COALESCE(SUM(ur.total_tokens), 0) AS total_tokens,
			COALESCE(SUM(ur.cost_usd), 0) AS estimated_cost_usd,
			rl.status_code,
			rl.error_code,
			rl.latency_ms,
			rl.created_at`).
		Group("rl.id, rl.request_id, ak.principal_type, ak.principal_external_id, gpr.display_name, gw.name, ak.project_external_id, gp.name, go.external_organization_id, go.name, rl.model_name, rl.provider_id, rl.status_code, rl.error_code, rl.latency_ms, rl.created_at").
		Order("rl.created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&items).Error
	if err != nil {
		return GatewayRequestLogPage{}, err
	}
	return GatewayRequestLogPage{Items: items, Page: page, PageSize: pageSize, Total: total}, nil
}

func gatewayRequestLogPattern(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(strings.TrimSpace(value)))
	return "%" + escaped + "%"
}
