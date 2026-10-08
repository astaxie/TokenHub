package server

import (
	"context"
	"time"
)

type UsageReportRequest struct {
	CreatedAt  time.Time
	StatusCode int
}

// StreamUsageReportRecords applies permissions and the half-open window in SQL.
// Visitors consume one projected row at a time without retaining the raw history.
func (s *GormStore) StreamUsageReportRecords(ctx context.Context, query UsageSummaryQuery, usage func(UsageRecord), request func(UsageReportRequest)) error {
	if err := s.streamUsageReportUsage(ctx, query.UsageRecords, usage); err != nil {
		return err
	}
	return s.streamUsageReportRequests(ctx, query.RequestLogs, request)
}

func (s *GormStore) streamUsageReportUsage(ctx context.Context, scope UsageSummaryScope, visit func(UsageRecord)) error {
	query, err := applyUsageSummaryScope(s.db.WithContext(ctx).Model(&UsageRecord{}), s.dbDriver, scope)
	if err != nil {
		return err
	}
	rows, err := query.Select("project_id, api_key_id, attributed_user_id, model_name, provider_id, provider_resource_id, input_tokens, cached_input_tokens, cache_write_tokens, output_tokens, reasoning_tokens, total_tokens, cost_usd, created_at").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var record UsageRecord
		if err := s.db.ScanRows(rows, &record); err != nil {
			return err
		}
		visit(record)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *GormStore) streamUsageReportRequests(ctx context.Context, scope UsageSummaryScope, visit func(UsageReportRequest)) error {
	query, err := applyUsageSummaryScope(s.db.WithContext(ctx).Model(&RequestLog{}), s.dbDriver, scope)
	if err != nil {
		return err
	}
	rows, err := query.Where("COALESCE(project_id, '') <> ?", "admin_playground").Select("created_at, status_code").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var log UsageReportRequest
		if err := s.db.ScanRows(rows, &log); err != nil {
			return err
		}
		visit(log)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return ctx.Err()
}
