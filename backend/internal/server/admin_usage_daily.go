package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (s *Server) usageDailyForUser(ctx context.Context, user AdminUser, now time.Time) (map[string]any, error) {
	timezone := s.dashboardTimezone()
	location, timezoneName, err := usageDailyLocation(timezone)
	if err != nil {
		return nil, err
	}
	localNow := now.In(location)
	startLocal := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	endLocal := startLocal.AddDate(0, 0, 1)
	startUTC := startLocal.UTC()
	endUTC := endLocal.UTC()
	summaryQuery := s.usageSummaryQueryForUser(user)
	summaryQuery.UsageRecords.CreatedAtFrom = startUTC
	summaryQuery.UsageRecords.CreatedAtBefore = endUTC
	summaryQuery.RequestLogs.CreatedAtFrom = startUTC
	summaryQuery.RequestLogs.CreatedAtBefore = endUTC
	summary, err := s.store.QueryUsageSummary(ctx, summaryQuery)
	if err != nil {
		return nil, err
	}

	records := make([]UsageRecord, 0)
	for _, record := range s.filterUsageRecordsForUser(user, s.store.ListUsageRecords()) {
		createdAt := record.CreatedAt.UTC()
		if !createdAt.Before(startUTC) && createdAt.Before(endUTC) {
			records = append(records, record)
		}
	}

	projectsByID := indexProjectsByID(s.store.ListProjects())
	breakdown := s.usageBreakdownFromRecords(records, projectsByID)
	breakdown["api_keys"] = aggregateUsage(records, func(record UsageRecord) string { return record.APIKeyID })
	breakdown["members"] = s.aggregateUsageByMember(user, records, projectsByID)
	if !isPlatformAdminRole(user.Role) {
		delete(breakdown, "providers")
		delete(breakdown, "provider_resources")
	}

	return map[string]any{
		"timezone":     timezoneName,
		"date":         startLocal.Format("2006-01-02"),
		"window_start": startUTC.Format(time.RFC3339),
		"window_end":   endUTC.Format(time.RFC3339),
		"summary":      usageSummaryPayload(summary),
		"breakdown":    breakdown,
	}, nil
}

func (s *Server) dashboardTimezone() string {
	for _, setting := range s.store.ListResources("settings") {
		if setting.ID != gatewaySettingsID || setting.Status != StatusActive {
			continue
		}
		if timezone := strings.TrimSpace(stringField(setting.Fields, dashboardTimezoneField)); timezone != "" {
			return timezone
		}
	}
	return "UTC"
}

func usageDailyLocation(timezone string) (*time.Location, string, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, "", NewHTTPError(http.StatusBadRequest, "invalid_usage_timezone", "timezone must be a valid IANA timezone")
	}
	return location, timezone, nil
}
