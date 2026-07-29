package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

type GatewayUsageFilter struct {
	TenantExternalID       string
	ProjectExternalID      string
	OrganizationExternalID string
	Principal              string
	Model                  string
	DateFrom               time.Time
	DateTo                 time.Time
	TimezoneOffsetMinutes  int
}

type GatewayUsageSummary struct {
	RequestCount     int64   `json:"request_count"`
	ErrorCount       int64   `json:"error_count"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type GatewayUsageTimeseriesItem struct {
	Date             string  `json:"date"`
	RequestCount     int64   `json:"request_count"`
	ErrorCount       int64   `json:"error_count"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type GatewayUsageBreakdownItem struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	RequestCount     int64   `json:"request_count"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

type GatewayUsageBreakdown struct {
	Organizations []GatewayUsageBreakdownItem `json:"organizations"`
	Projects      []GatewayUsageBreakdownItem `json:"projects"`
	Principals    []GatewayUsageBreakdownItem `json:"principals"`
	Models        []GatewayUsageBreakdownItem `json:"models"`
}

type GatewayUsageReport struct {
	Summary    GatewayUsageSummary          `json:"summary"`
	Timeseries []GatewayUsageTimeseriesItem `json:"timeseries"`
	Breakdown  GatewayUsageBreakdown        `json:"breakdown"`
}

func (s *Server) handleGatewayUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	query := r.URL.Query()
	dateFrom, err := optionalRFC3339(query.Get("date_from"))
	if err != nil || dateFrom == nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_usage_query", "Date from is required and must be RFC3339"))
		return
	}
	dateTo, err := optionalRFC3339(query.Get("date_to"))
	if err != nil || dateTo == nil || dateFrom.After(*dateTo) || dateTo.Sub(*dateFrom) > 90*24*time.Hour {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_usage_query", "Date range is invalid or exceeds 90 days"))
		return
	}
	timezoneOffsetMinutes, err := rfc3339OffsetMinutes(query.Get("date_from"))
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_usage_query", "Date from timezone is invalid"))
		return
	}
	report, err := s.store.GetGatewayUsage(GatewayUsageFilter{
		TenantExternalID:       query.Get("tenant_id"),
		ProjectExternalID:      query.Get("project_id"),
		OrganizationExternalID: query.Get("organization_id"),
		Principal:              query.Get("principal"),
		Model:                  query.Get("model"),
		DateFrom:               *dateFrom,
		DateTo:                 *dateTo,
		TimezoneOffsetMinutes:  timezoneOffsetMinutes,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": report})
}

func rfc3339OffsetMinutes(value string) (int, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	_, offsetSeconds := parsed.Zone()
	return offsetSeconds / 60, nil
}

func (s *GormStore) gatewayUsageScope(filter GatewayUsageFilter, table string) (*gorm.DB, error) {
	tenantID := strings.TrimSpace(filter.TenantExternalID)
	if tenantID == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "tenant_required", "Tenant ID is required")
	}
	prefix := "ur"
	if table == "request_logs" {
		prefix = "rl"
	}
	query := s.db.Table(table+" AS "+prefix).
		Joins("JOIN api_keys AS ak ON ak.id = "+prefix+".api_key_id AND ak.managed_by = ? AND ak.tenant_external_id = ?", gatewayModelAccessKeyManagedBy, tenantID).
		Joins("LEFT JOIN gateway_projects AS gp ON gp.id = "+prefix+".project_id").
		Joins("LEFT JOIN gateway_organizations AS go ON go.id = gp.organization_id").
		Joins("LEFT JOIN gateway_principals AS gpr ON ak.principal_type = ? AND gpr.tenant_id = gp.tenant_id AND gpr.external_principal_id = ak.principal_external_id", "user").
		Joins("LEFT JOIN gateway_workloads AS gw ON ak.principal_type <> ? AND gw.tenant_id = gp.tenant_id AND gw.external_workload_id = ak.principal_external_id", "user").
		Where(prefix+".created_at >= ? AND "+prefix+".created_at <= ?", filter.DateFrom, filter.DateTo)
	if value := strings.TrimSpace(filter.ProjectExternalID); value != "" {
		query = query.Where("ak.project_external_id = ?", value)
	}
	if value := strings.TrimSpace(filter.OrganizationExternalID); value != "" {
		query = query.Where("go.external_organization_id = ?", value)
	}
	if value := strings.TrimSpace(filter.Principal); value != "" {
		pattern := gatewayRequestLogPattern(value)
		query = query.Where("LOWER(ak.principal_external_id) LIKE ? ESCAPE '\\' OR LOWER(COALESCE(gpr.display_name, gw.name, '')) LIKE ? ESCAPE '\\'", pattern, pattern)
	}
	if value := strings.TrimSpace(filter.Model); value != "" {
		query = query.Where("LOWER("+prefix+".model_name) LIKE ? ESCAPE '\\'", gatewayRequestLogPattern(value))
	}
	return query, nil
}

func (s *GormStore) GetGatewayUsage(filter GatewayUsageFilter) (GatewayUsageReport, error) {
	usageScope, err := s.gatewayUsageScope(filter, "usage_records")
	if err != nil {
		return GatewayUsageReport{}, err
	}
	logScope, err := s.gatewayUsageScope(filter, "request_logs")
	if err != nil {
		return GatewayUsageReport{}, err
	}

	report := GatewayUsageReport{
		Timeseries: make([]GatewayUsageTimeseriesItem, 0),
		Breakdown: GatewayUsageBreakdown{
			Organizations: make([]GatewayUsageBreakdownItem, 0), Projects: make([]GatewayUsageBreakdownItem, 0),
			Principals: make([]GatewayUsageBreakdownItem, 0), Models: make([]GatewayUsageBreakdownItem, 0),
		},
	}
	if err := usageScope.Session(&gorm.Session{}).Select("COALESCE(SUM(ur.input_tokens), 0) AS input_tokens, COALESCE(SUM(ur.output_tokens), 0) AS output_tokens, COALESCE(SUM(ur.total_tokens), 0) AS total_tokens, COALESCE(SUM(ur.cost_usd), 0) AS estimated_cost_usd").Scan(&report.Summary).Error; err != nil {
		return GatewayUsageReport{}, err
	}
	var requestSummary struct {
		RequestCount int64
		ErrorCount   int64
	}
	if err := logScope.Session(&gorm.Session{}).Select("COUNT(DISTINCT rl.request_id) AS request_count, COALESCE(SUM(CASE WHEN rl.status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_count").Scan(&requestSummary).Error; err != nil {
		return GatewayUsageReport{}, err
	}
	report.Summary.RequestCount, report.Summary.ErrorCount = requestSummary.RequestCount, requestSummary.ErrorCount

	usageDateExpression := s.gatewayUsageDateExpression("ur.created_at", filter.TimezoneOffsetMinutes)
	usageDaily := make([]GatewayUsageTimeseriesItem, 0)
	if err := usageScope.Session(&gorm.Session{}).Select(usageDateExpression + " AS date, COALESCE(SUM(ur.total_tokens), 0) AS total_tokens, COALESCE(SUM(ur.cost_usd), 0) AS estimated_cost_usd").Group(usageDateExpression).Order("date ASC").Scan(&usageDaily).Error; err != nil {
		return GatewayUsageReport{}, err
	}
	logDateExpression := s.gatewayUsageDateExpression("rl.created_at", filter.TimezoneOffsetMinutes)
	logDaily := make([]GatewayUsageTimeseriesItem, 0)
	if err := logScope.Session(&gorm.Session{}).Select(logDateExpression + " AS date, COUNT(DISTINCT rl.request_id) AS request_count, COALESCE(SUM(CASE WHEN rl.status_code >= 400 THEN 1 ELSE 0 END), 0) AS error_count").Group(logDateExpression).Order("date ASC").Scan(&logDaily).Error; err != nil {
		return GatewayUsageReport{}, err
	}
	daily := make(map[string]GatewayUsageTimeseriesItem, len(usageDaily)+len(logDaily))
	for _, item := range usageDaily {
		daily[item.Date] = item
	}
	for _, item := range logDaily {
		current := daily[item.Date]
		current.Date, current.RequestCount, current.ErrorCount = item.Date, item.RequestCount, item.ErrorCount
		daily[item.Date] = current
	}
	offset := time.Duration(filter.TimezoneOffsetMinutes) * time.Minute
	dateFrom := filter.DateFrom.Add(offset)
	dateTo := filter.DateTo.Add(offset)
	for date := dateFrom; !date.After(dateTo); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		item := daily[key]
		item.Date = key
		report.Timeseries = append(report.Timeseries, item)
	}

	organizationID := "COALESCE(NULLIF(go.external_organization_id, ''), 'unassigned')"
	organizationName := "COALESCE(NULLIF(go.name, ''), 'Unassigned')"
	if report.Breakdown.Organizations, err = buildGatewayUsageBreakdown(usageScope, logScope, organizationID, organizationName, organizationID, organizationName); err != nil {
		return GatewayUsageReport{}, err
	}
	if report.Breakdown.Projects, err = buildGatewayUsageBreakdown(usageScope, logScope, "ak.project_external_id", "COALESCE(NULLIF(gp.name, ''), ak.project_external_id)", "ak.project_external_id", "COALESCE(NULLIF(gp.name, ''), ak.project_external_id)"); err != nil {
		return GatewayUsageReport{}, err
	}
	principalName := "COALESCE(NULLIF(gpr.display_name, ''), NULLIF(gw.name, ''), ak.principal_external_id)"
	if report.Breakdown.Principals, err = buildGatewayUsageBreakdown(usageScope, logScope, "ak.principal_external_id", principalName, "ak.principal_external_id", principalName); err != nil {
		return GatewayUsageReport{}, err
	}
	if report.Breakdown.Models, err = buildGatewayUsageBreakdown(usageScope, logScope, "ur.model_name", "ur.model_name", "rl.model_name", "rl.model_name"); err != nil {
		return GatewayUsageReport{}, err
	}
	return report, nil
}

func (s *GormStore) gatewayUsageDateExpression(column string, timezoneOffsetMinutes int) string {
	offset := fmt.Sprintf("%+d minutes", timezoneOffsetMinutes)
	if s.db.Dialector.Name() == "postgres" {
		return "TO_CHAR(" + column + " AT TIME ZONE 'UTC' + INTERVAL '" + offset + "', 'YYYY-MM-DD')"
	}
	return "STRFTIME('%Y-%m-%d', " + column + ", '" + offset + "')"
}

func buildGatewayUsageBreakdown(usageScope *gorm.DB, logScope *gorm.DB, usageID string, usageName string, logID string, logName string) ([]GatewayUsageBreakdownItem, error) {
	usageItems := make([]GatewayUsageBreakdownItem, 0)
	if err := usageScope.Session(&gorm.Session{}).Select(usageID + " AS id, " + usageName + " AS name, COALESCE(SUM(ur.total_tokens), 0) AS total_tokens, COALESCE(SUM(ur.cost_usd), 0) AS estimated_cost_usd").
		Group(usageID + ", " + usageName).
		Scan(&usageItems).Error; err != nil {
		return nil, err
	}
	requestItems := make([]GatewayUsageBreakdownItem, 0)
	if err := logScope.Session(&gorm.Session{}).Select(logID + " AS id, " + logName + " AS name, COUNT(DISTINCT rl.request_id) AS request_count").
		Group(logID + ", " + logName).
		Scan(&requestItems).Error; err != nil {
		return nil, err
	}

	byID := make(map[string]GatewayUsageBreakdownItem, len(usageItems)+len(requestItems))
	for _, item := range usageItems {
		byID[item.ID] = item
	}
	for _, item := range requestItems {
		current := byID[item.ID]
		current.ID, current.RequestCount = item.ID, item.RequestCount
		if item.Name != "" {
			current.Name = item.Name
		}
		byID[item.ID] = current
	}
	result := make([]GatewayUsageBreakdownItem, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EstimatedCostUSD != result[j].EstimatedCostUSD {
			return result[i].EstimatedCostUSD > result[j].EstimatedCostUSD
		}
		if result[i].TotalTokens != result[j].TotalTokens {
			return result[i].TotalTokens > result[j].TotalTokens
		}
		if result[i].RequestCount != result[j].RequestCount {
			return result[i].RequestCount > result[j].RequestCount
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}
