package server

import (
	"context"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AlertPageQuery struct {
	Page      int
	PageSize  int
	ProjectID string
	Severity  string
	Keyword   string
	From      time.Time
	Before    time.Time
}

type AlertPage struct {
	Data     []AlertEvent `json:"data"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Total    int64        `json:"total"`
}

func alertPageQuery(r *http.Request) (AlertPageQuery, error) {
	params := r.URL.Query()
	query := AlertPageQuery{Page: 1, PageSize: 50, ProjectID: params.Get("project_id"), Severity: params.Get("severity"), Keyword: strings.TrimSpace(params.Get("q"))}
	for _, field := range []struct {
		name   string
		target *int
	}{{"page", &query.Page}, {"page_size", &query.PageSize}} {
		if raw := r.URL.Query().Get(field.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 || value > 1000000 {
				return query, NewHTTPError(400, "invalid_pagination", "Invalid pagination")
			}
			*field.target = value
		}
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	for _, field := range []struct {
		name   string
		target *time.Time
	}{{"from", &query.From}, {"before", &query.Before}} {
		if raw := r.URL.Query().Get(field.name); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return query, NewHTTPError(400, "invalid_time_range", "Invalid time range")
			}
			*field.target = value.UTC()
		}
	}
	if !query.From.IsZero() && !query.Before.IsZero() && !query.From.Before(query.Before) {
		return query, NewHTTPError(400, "invalid_time_range", "Invalid time range")
	}
	return query, nil
}

func (s *GormStore) QueryAlertPage(ctx context.Context, query AlertPageQuery) (page AlertPage, err error) {
	err = s.withAnalyticsReadSnapshot(ctx, func(tx *gorm.DB) error {
		snapshot := *s
		snapshot.analyticsDB = tx
		var queryErr error
		page, queryErr = snapshot.queryAlertPageSnapshot(ctx, query)
		return queryErr
	})
	return page, err
}

func (s *GormStore) queryAlertPageSnapshot(ctx context.Context, query AlertPageQuery) (AlertPage, error) {
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 50
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	page := AlertPage{Data: []AlertEvent{}, Page: query.Page, PageSize: query.PageSize}
	db := s.analyticsDB.WithContext(ctx).Model(&AlertEvent{})
	if query.ProjectID != "" {
		budgetScope := "json_extract(b.fields, '$.scope')"
		if s.dbDriver == "postgres" {
			budgetScope = "(CAST(b.fields AS jsonb) ->> 'scope')"
		}
		db = db.Where(`(scope_id = ? AND COALESCE(scope_type, '') IN ('', 'project'))
			OR (resource_id = ? AND (scope_type IN ('api_key', 'user')
			OR (scope_type = 'budget' AND EXISTS (SELECT 1 FROM admin_resources b
				WHERE b.id = alert_events.scope_id AND b.kind = 'budgets' AND LOWER(TRIM(`+budgetScope+`)) = 'project'))))`,
			query.ProjectID, query.ProjectID)
	}
	if query.Keyword != "" {
		pattern := "%" + escapeLikePattern(strings.ToLower(query.Keyword)) + "%"
		columns := []string{"id", "code", "message", "scope_id", "scope_type", "resource_id", "severity", "CAST(created_at AS TEXT)"}
		conditions := make([]string, 0, len(columns))
		args := make([]any, 0, len(columns))
		for _, column := range columns {
			conditions = append(conditions, "LOWER("+column+") LIKE ? ESCAPE '!'")
			args = append(args, pattern)
		}
		db = db.Where(strings.Join(conditions, " OR "), args...)
	}
	if query.Severity != "" {
		db = db.Where("severity = ?", query.Severity)
	}
	if !query.From.IsZero() {
		db = db.Where("created_at >= ?", query.From)
	}
	if !query.Before.IsZero() {
		db = db.Where("created_at < ?", query.Before)
	}
	if err := db.Count(&page.Total).Error; err != nil {
		return page, err
	}
	err := db.Order("created_at DESC, id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&page.Data).Error
	return page, err
}

// ListRecentAlerts reads only the bounded preview, without a history count.
func (s *GormStore) ListRecentAlerts(ctx context.Context) ([]AlertEvent, error) {
	items := []AlertEvent{}
	err := s.analyticsDB.WithContext(ctx).Order("created_at DESC, id DESC").Limit(20).Find(&items).Error
	return items, err
}
