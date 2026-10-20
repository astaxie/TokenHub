package server

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// UsageGroup contains aggregate metrics and only the attribution fields needed
// to resolve a dimension. It never carries request payloads or individual rows.
type UsageGroup struct {
	ID               string
	ProjectID        string
	APIKeyID         string
	AttributedUserID string
	UsageSummary
}

type UsageDay struct {
	Date string
	UsageSummary
}

const usageGroupMetrics = "COUNT(*) AS usage_record_count, " +
	"COALESCE(SUM(input_tokens), 0) AS input_tokens, " +
	"COALESCE(SUM(cached_input_tokens), 0) AS cached_input_tokens, " +
	"COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens, " +
	"COALESCE(SUM(output_tokens), 0) AS output_tokens, " +
	"COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens, " +
	"COALESCE(SUM(total_tokens), 0) AS total_tokens, " +
	"COALESCE(SUM(cost_usd), 0) AS estimated_cost_usd"

// StreamUsageGroups aggregates each requested dimension in SQL. Streaming the
// grouped rows also bounds temporary memory when attribution has many values.
func (s *GormStore) StreamUsageGroups(ctx context.Context, scope UsageSummaryScope, dimensions []string, visit func(string, UsageGroup)) error {
	read := func(tx *gorm.DB) error {
		snapshot := *s
		snapshot.analyticsDB = tx
		for _, dimension := range dimensions {
			query, err := snapshot.usageGroupQuery(ctx, scope, dimension)
			if err != nil {
				return err
			}
			if err := snapshot.streamUsageGroupRows(ctx, query, func(group UsageGroup) { visit(dimension, group) }); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	return s.withAnalyticsReadSnapshot(ctx, read)
}

func (s *GormStore) usageGroupQuery(ctx context.Context, scope UsageSummaryScope, dimension string) (*gorm.DB, error) {
	columns := map[string]string{
		"projects": "project_id", "models": "model_name", "providers": "provider_id",
		"provider_resources": "provider_resource_id", "api_keys": "api_key_id",
		"members": "project_id, api_key_id, attributed_user_id",
	}
	column, ok := columns[dimension]
	if !ok {
		return nil, fmt.Errorf("unsupported usage dimension %q", dimension)
	}
	selection := column + " AS id"
	if dimension == "members" {
		selection = column
	}
	query, err := applyUsageSummaryScope(s.analyticsDB.WithContext(ctx).Model(&UsageRecord{}), s.dbDriver, scope)
	if err != nil {
		return nil, err
	}
	return query.Select(selection + ", " + usageGroupMetrics).Group(column), nil
}

func (s *GormStore) streamUsageGroupRows(ctx context.Context, query *gorm.DB, visit func(UsageGroup)) (resultErr error) {
	rows, err := query.Rows()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var group UsageGroup
		if err := s.analyticsDB.ScanRows(rows, &group); err != nil {
			return err
		}
		visit(group)
	}
	return errors.Join(rows.Err(), ctx.Err())
}

func (s *GormStore) usageTimeseriesQuery(ctx context.Context, scope UsageSummaryScope) (*gorm.DB, error) {
	query, err := applyUsageSummaryScope(s.analyticsDB.WithContext(ctx).Model(&UsageRecord{}), s.dbDriver, scope)
	if err != nil {
		return nil, err
	}
	day := "strftime('%Y-%m-%d', created_at)"
	if s.dbDriver == "postgres" {
		day = "to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')"
	}
	return query.Select(day + " AS date, " + usageGroupMetrics).Group(day).Order("date"), nil
}

func (s *GormStore) QueryUsageTimeseries(ctx context.Context, scope UsageSummaryScope) ([]UsageDay, error) {
	query, err := s.usageTimeseriesQuery(ctx, scope)
	if err != nil {
		return nil, err
	}
	var days []UsageDay
	err = query.Scan(&days).Error
	return days, err
}
