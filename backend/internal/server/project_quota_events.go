package server

import (
	"context"
	"net/http"
	"time"
)

type ProjectQuotaEvents struct {
	Count  int64  `json:"count"`
	Latest string `json:"latest"`
}

func (s *Server) handleProjectQuotaEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "project", r.Method)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), maximumUsageQueryDuration)
	defer cancel()
	projects := s.filterProjectsForUser(user, s.store.ListProjects())
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	result, err := s.store.QueryProjectQuotaEvents(ctx, ids, s.usageQueryServer(ctx).usageSummaryQueryForUser(user).RequestLogs, s.canViewGlobalOperations(user))
	if err != nil {
		writeError(w, r, NewHTTPError(503, "quota_events_unavailable", "Quota events are temporarily unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *GormStore) QueryProjectQuotaEvents(ctx context.Context, ids []string, requestScope UsageSummaryScope, includeAlerts bool) (map[string]ProjectQuotaEvents, error) {
	result := make(map[string]ProjectQuotaEvents)
	if len(ids) == 0 {
		return result, nil
	}
	for _, source := range []struct{ table, project, condition string }{
		{"request_logs", "project_id", "(error_code = 'quota_exceeded' OR status_code = 429)"},
		{"alert_events", "scope_id", "LOWER(code) LIKE '%quota%'"},
	} {
		if source.table == "alert_events" && !includeAlerts {
			continue
		}
		membership, args, err := usageSummaryMembershipCondition(s.dbDriver, source.project, ids)
		if err != nil {
			return nil, err
		}
		query := s.analyticsDB.WithContext(ctx).Table(source.table).Where(membership, args...).Where(source.condition)
		if source.table == "request_logs" {
			query, err = applyUsageSummaryScope(query, s.dbDriver, requestScope)
			if err != nil {
				return nil, err
			}
		}
		var rows []struct {
			ID     string
			Count  int64
			Latest string
		}
		// Cast MAX to text so both drivers return a stable timestamp value.
		if err := query.Select(source.project + " AS id, COUNT(*) AS count, CAST(MAX(created_at) AS TEXT) AS latest").Group(source.project).Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			value := result[row.ID]
			value.Count += row.Count
			latest := normalizeQuotaEventTime(row.Latest)
			candidate, candidateErr := time.Parse(time.RFC3339Nano, latest)
			previous, _ := time.Parse(time.RFC3339Nano, value.Latest)
			if candidateErr == nil && candidate.After(previous) {
				value.Latest = latest
			}
			result[row.ID] = value
		}
	}
	return result, nil
}

func normalizeQuotaEventTime(value string) string {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999+00:00", "2006-01-02 15:04:05.999999999"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC().Format(time.RFC3339Nano)
		}
	}
	return value
}
