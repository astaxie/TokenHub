package server

import (
	"context"
	"time"
)

func (s *Server) usageBreakdownForUser(ctx context.Context, user AdminUser) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, maximumUsageQueryDuration)
	defer cancel()
	s = s.usageQueryServer(ctx)
	return s.usageBreakdownForScope(ctx, user, s.usageSummaryQueryForUser(user).UsageRecords, false, false)
}

func (s *Server) usageBreakdownForScope(ctx context.Context, user AdminUser, scope UsageSummaryScope, includeAPIKeys, redactProviders bool) (map[string]any, error) {
	aggregate := s.newUsageReportAggregate(user, "all", time.Time{}, time.UTC)
	dimensions := []string{"projects", "models", "members"}
	if includeAPIKeys {
		dimensions = append(dimensions, "api_keys")
	}
	if !redactProviders || isPlatformAdminRole(user.Role) {
		dimensions = append(dimensions, "providers", "provider_resources")
	}
	if err := s.store.StreamUsageGroups(ctx, scope, dimensions, aggregate.addGroup); err != nil {
		return nil, err
	}
	breakdown := aggregate.breakdownForVisibility(redactProviders)
	if !includeAPIKeys {
		delete(breakdown, "api_keys")
	}
	return breakdown, nil
}

func (a *usageReportAggregate) addGroup(dimension string, group UsageGroup) {
	key := group.ID
	if dimension == "members" {
		key = usageReportMemberID(a.user, UsageRecord{
			ProjectID: group.ProjectID, APIKeyID: group.APIKeyID, AttributedUserID: group.AttributedUserID,
		}, a.keysByID, a.projectsByID, a.usersByID)
		if a.usedKeysByMember[key] == nil {
			a.usedKeysByMember[key] = map[string]bool{}
		}
		if group.APIKeyID != "" {
			a.usedKeysByMember[key][group.APIKeyID] = true
		}
	}
	if dimension == "projects" {
		costCenter := a.costCenters[group.ID]
		if costCenter == "" {
			costCenter = "unknown"
		}
		mergeUsageReportSummary(usageReportBucket(a.dimensions["cost_centers"], costCenter), group.UsageSummary)
	}
	if key == "" {
		key = "unknown"
	}
	mergeUsageReportSummary(usageReportBucket(a.dimensions[dimension], key), group.UsageSummary)
}

func (s *Server) usageTimeseriesForUser(ctx context.Context, user AdminUser, days int, now time.Time) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, maximumUsageQueryDuration)
	defer cancel()
	s = s.usageQueryServer(ctx)
	if days <= 0 {
		days = 31
	}
	if days > 90 {
		days = 90
	}
	now = now.UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	scope := s.usageSummaryQueryForUser(user).UsageRecords
	// Preserve the legacy rolling cutoff on the oldest UTC calendar day.
	scope.CreatedAtFrom = now.AddDate(0, 0, -days+1)
	scope.CreatedAtBefore = dayStart.AddDate(0, 0, 1)
	groups, err := s.store.QueryUsageTimeseries(ctx, scope)
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]UsageSummary, len(groups))
	for _, group := range groups {
		byDay[group.Date] = group.UsageSummary
	}
	series := make([]map[string]any, 0, days)
	for index := days - 1; index >= 0; index-- {
		day := dayStart.AddDate(0, 0, -index).Format("2006-01-02")
		summary := byDay[day]
		series = append(series, map[string]any{
			"date": day, "request_count": summary.UsageRecordCount, "input_tokens": summary.InputTokens,
			"cached_input_tokens": summary.CachedInputTokens, "output_tokens": summary.OutputTokens,
			"total_tokens": summary.TotalTokens, "estimated_cost_usd": summary.EstimatedCostUSD,
		})
	}
	return series, nil
}
