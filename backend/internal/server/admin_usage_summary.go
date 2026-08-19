package server

import "context"

func (s *Server) usageSummaryForUser(ctx context.Context, user AdminUser) (map[string]any, error) {
	summary, err := s.store.QueryUsageSummary(ctx, s.usageSummaryQueryForUser(user))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"request_count":            summary.RequestCount,
		"usage_record_count":       summary.UsageRecordCount,
		"input_tokens":             summary.InputTokens,
		"cached_input_tokens":      summary.CachedInputTokens,
		"cache_write_input_tokens": summary.CacheWriteTokens,
		"output_tokens":            summary.OutputTokens,
		"reasoning_output_tokens":  summary.ReasoningTokens,
		"total_tokens":             summary.TotalTokens,
		"estimated_cost_usd":       summary.EstimatedCostUSD,
		"errors":                   summary.Errors,
	}, nil
}

func (s *Server) usageSummaryQueryForUser(user AdminUser) UsageSummaryQuery {
	if s.canViewGlobalOperations(user) {
		global := UsageSummaryScope{Global: true}
		return UsageSummaryQuery{UsageRecords: global, RequestLogs: global}
	}

	apiKeyIDs := trueMapKeys(s.visibleAPIKeyIDSet(user))
	usageScope := UsageSummaryScope{
		AttributedUserIDs: usageAttributedUserIDs(user, s.store.ListAdminUsers()),
		APIKeyIDs:         apiKeyIDs,
	}
	requestScope := UsageSummaryScope{APIKeyIDs: apiKeyIDs}
	if normalizeAdminRole(user.Role) == "team_leader" {
		projectIDs := trueMapKeys(s.visibleProjectIDSet(user))
		usageScope.ProjectIDs = projectIDs
		requestScope.ProjectIDs = projectIDs
	}
	return UsageSummaryQuery{UsageRecords: usageScope, RequestLogs: requestScope}
}

func usageAttributedUserIDs(user AdminUser, users []AdminUser) []string {
	if normalizeAdminRole(user.Role) != "team_leader" {
		if user.ID == "" {
			return nil
		}
		return []string{user.ID}
	}
	usersByID := make(map[string]AdminUser, len(users))
	for _, item := range users {
		usersByID[item.ID] = item
	}
	ids := make([]string, 0, len(users))
	for _, item := range users {
		if canAttributeUsageToMember(user, usersByID, item.ID) {
			ids = append(ids, item.ID)
		}
	}
	return ids
}
