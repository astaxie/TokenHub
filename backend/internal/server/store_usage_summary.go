package server

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

type UsageSummaryScope struct {
	Global            bool
	AttributedUserIDs []string
	ProjectIDs        []string
	APIKeyIDs         []string
}

type UsageSummaryQuery struct {
	UsageRecords UsageSummaryScope
	RequestLogs  UsageSummaryScope
}

type UsageSummary struct {
	RequestCount      int64
	UsageRecordCount  int64
	InputTokens       int64
	CachedInputTokens int64
	CacheWriteTokens  int64
	OutputTokens      int64
	ReasoningTokens   int64
	TotalTokens       int64
	EstimatedCostUSD  float64
	Errors            int64
}

func (s *GormStore) QueryUsageSummary(ctx context.Context, query UsageSummaryQuery) (UsageSummary, error) {
	var usage struct {
		UsageRecordCount  int64
		InputTokens       int64
		CachedInputTokens int64
		CacheWriteTokens  int64
		OutputTokens      int64
		ReasoningTokens   int64
		TotalTokens       int64
		EstimatedCostUSD  float64
	}
	usageQuery, err := applyUsageSummaryScope(s.db.WithContext(ctx).Model(&UsageRecord{}), s.dbDriver, query.UsageRecords)
	if err != nil {
		return UsageSummary{}, err
	}
	if err := usageQuery.Select(
		"COUNT(*) AS usage_record_count, " +
			"COALESCE(SUM(input_tokens), 0) AS input_tokens, " +
			"COALESCE(SUM(cached_input_tokens), 0) AS cached_input_tokens, " +
			"COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, " +
			"COALESCE(SUM(output_tokens), 0) AS output_tokens, " +
			"COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens, " +
			"COALESCE(SUM(total_tokens), 0) AS total_tokens, " +
			"COALESCE(SUM(cost_usd), 0) AS estimated_cost_usd",
	).Scan(&usage).Error; err != nil {
		return UsageSummary{}, err
	}

	var requests struct {
		RequestCount int64
		Errors       int64
	}
	requestQuery, err := applyUsageSummaryScope(s.db.WithContext(ctx).Model(&RequestLog{}), s.dbDriver, query.RequestLogs)
	if err != nil {
		return UsageSummary{}, err
	}
	if err := requestQuery.Select(
		"COALESCE(SUM(CASE WHEN COALESCE(project_id, '') <> ? THEN 1 ELSE 0 END), 0) AS request_count, "+
			"COALESCE(SUM(CASE WHEN COALESCE(project_id, '') <> ? AND status_code >= 400 THEN 1 ELSE 0 END), 0) AS errors",
		"admin_playground",
		"admin_playground",
	).Scan(&requests).Error; err != nil {
		return UsageSummary{}, err
	}

	return UsageSummary{
		RequestCount:      requests.RequestCount,
		UsageRecordCount:  usage.UsageRecordCount,
		InputTokens:       usage.InputTokens,
		CachedInputTokens: usage.CachedInputTokens,
		CacheWriteTokens:  usage.CacheWriteTokens,
		OutputTokens:      usage.OutputTokens,
		ReasoningTokens:   usage.ReasoningTokens,
		TotalTokens:       usage.TotalTokens,
		EstimatedCostUSD:  usage.EstimatedCostUSD,
		Errors:            requests.Errors,
	}, nil
}

func applyUsageSummaryScope(db *gorm.DB, driver string, scope UsageSummaryScope) (*gorm.DB, error) {
	if scope.Global {
		return db, nil
	}
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 4)
	if len(scope.AttributedUserIDs) > 0 {
		condition, conditionArgs, err := usageSummaryMembershipCondition(driver, "attributed_user_id", scope.AttributedUserIDs)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
	}
	if len(scope.ProjectIDs) > 0 {
		condition, conditionArgs, err := usageSummaryMembershipCondition(driver, "project_id", scope.ProjectIDs)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
	}
	if len(scope.APIKeyIDs) > 0 {
		condition, conditionArgs, err := usageSummaryMembershipCondition(driver, "api_key_id", scope.APIKeyIDs)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, condition)
		args = append(args, conditionArgs...)
	}
	if len(conditions) == 0 {
		return db.Where("1 = 0"), nil
	}
	return db.Where("("+strings.Join(conditions, " OR ")+")", args...), nil
}

func usageSummaryMembershipCondition(driver string, column string, values []string) (string, []any, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", nil, err
	}
	jsonValues := string(encoded)
	if driver == "postgres" {
		return column + " IN (SELECT value FROM jsonb_array_elements_text(CAST(? AS jsonb)))", []any{jsonValues}, nil
	}
	return column + " IN (SELECT value FROM json_each(?))", []any{jsonValues}, nil
}
