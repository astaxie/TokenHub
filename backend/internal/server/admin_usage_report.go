package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleAdminUsageReport(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "usage", r.Method)
	if !ok {
		return
	}
	report, err := s.usageReportForUser(r.Context(), user, r.URL.Query().Get("range"), time.Now())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) usageReportForUser(ctx context.Context, user AdminUser, selectedRange string, now time.Time) (map[string]any, error) {
	selectedRange = strings.TrimSpace(selectedRange)
	if selectedRange == "" {
		selectedRange = "today"
	}
	if selectedRange != "today" && selectedRange != "7d" && selectedRange != "30d" && selectedRange != "all" {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_usage_range", "range must be today, 7d, 30d, or all")
	}
	location, timezone, err := usageDailyLocation(s.dashboardTimezone())
	if err != nil {
		return nil, err
	}
	localNow := now.In(location)
	calendarDay := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 12, 0, 0, 0, location)
	start := usageReportDayStart(calendarDay, location)
	switch selectedRange {
	case "7d":
		start = usageReportDayStart(calendarDay.AddDate(0, 0, -6), location)
	case "30d":
		start = usageReportDayStart(calendarDay.AddDate(0, 0, -29), location)
	case "all":
		start = time.Time{}
	}
	query := s.usageSummaryQueryForUser(user)
	query.UsageRecords.CreatedAtFrom, query.RequestLogs.CreatedAtFrom = start.UTC(), start.UTC()
	query.UsageRecords.CreatedAtBefore, query.RequestLogs.CreatedAtBefore = now.UTC(), now.UTC()
	aggregate := s.newUsageReportAggregate(user, selectedRange, now, location)
	if err := s.store.StreamUsageReportRecords(ctx, query, aggregate.addUsage, aggregate.addRequest); err != nil {
		return nil, err
	}
	if selectedRange == "all" {
		start = aggregate.earliest
	}
	windowStart := ""
	if !start.IsZero() {
		windowStart = start.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"range":        selectedRange,
		"timezone":     timezone,
		"window_start": windowStart,
		"window_end":   now.UTC().Format(time.RFC3339Nano),
		"granularity":  aggregate.granularity,
		"summary":      usageSummaryPayload(aggregate.summary),
		"breakdown":    aggregate.breakdown(),
		"timeseries":   usageReportTimeseries(aggregate.buckets, start, now, location, aggregate.granularity),
	}, nil
}

func usageReportGranularity(selectedRange string, start, end time.Time, location *time.Location) string {
	if selectedRange == "today" {
		return "hour"
	}
	if selectedRange == "all" && !start.IsZero() {
		localStart := start.In(location)
		afterNinetyDays := time.Date(localStart.Year(), localStart.Month(), localStart.Day()+90, 12, 0, 0, 0, location)
		if usageReportDayStart(afterNinetyDays, location).Before(end) {
			return "month"
		}
	}
	return "day"
}

func usageReportTimeseries(buckets map[string]*UsageSummary, start, end time.Time, location *time.Location, granularity string) []map[string]any {
	series := make([]map[string]any, 0)
	if start.IsZero() {
		return series
	}
	localStart := start.In(location)
	first := usageReportDayStart(localStart, location)
	if granularity == "month" {
		first = usageReportDayStart(time.Date(first.Year(), first.Month(), 1, 12, 0, 0, 0, location), location)
	}
	for cursor := first; cursor.Before(end); {
		key := usageReportBucketKey(cursor, location, granularity)
		summary := UsageSummary{}
		if bucket := buckets[key]; bucket != nil {
			summary = *bucket
		}
		point := usageSummaryPayload(summary)
		point["date"] = key
		series = append(series, point)
		switch granularity {
		case "hour":
			cursor = cursor.Add(time.Hour)
		case "month":
			cursor = usageReportDayStart(time.Date(cursor.Year(), cursor.Month()+1, 1, 12, 0, 0, 0, location), location)
		default:
			cursor = usageReportDayStart(time.Date(cursor.Year(), cursor.Month(), cursor.Day()+1, 12, 0, 0, 0, location), location)
		}
	}
	return series
}

func usageReportBucketKey(timestamp time.Time, location *time.Location, granularity string) string {
	local := timestamp.In(location)
	if granularity == "hour" {
		// Elapsed hours from the local day start remain distinct through DST,
		// including zones that change their clocks by a fraction of an hour.
		dayStart := usageReportDayStart(local, location)
		elapsed := local.Sub(dayStart) / time.Hour * time.Hour
		return dayStart.Add(elapsed).UTC().Format(time.RFC3339)
	}
	if granularity == "month" {
		return local.Format("2006-01") + "-01"
	}
	return local.Format("2006-01-02")
}

func usageReportDayStart(timestamp time.Time, location *time.Location) time.Time {
	local := timestamp.In(location)
	date := local.Format("2006-01-02")
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	if midnight.Format("2006-01-02") == date && midnight.Add(-time.Nanosecond).Format("2006-01-02") != date {
		return midnight
	}
	// A skipped or repeated midnight may normalize to the wrong side of the
	// boundary. Find the first instant of the actual local calendar date.
	lower, upper := local.Add(-48*time.Hour), local
	for upper.Sub(lower) > time.Nanosecond {
		middle := lower.Add(upper.Sub(lower) / 2)
		if middle.In(location).Format("2006-01-02") < date {
			lower = middle
		} else {
			upper = middle
		}
	}
	return upper.In(location)
}
