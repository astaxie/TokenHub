package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUsageReportCalendarRangesUseHalfOpenWindow(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 10, 30, 0, 0, location).UTC()
	for _, test := range []struct {
		rangeName   string
		days        int
		granularity string
	}{
		{rangeName: "today", days: 1, granularity: "hour"},
		{rangeName: "7d", days: 7, granularity: "day"},
		{rangeName: "30d", days: 30, granularity: "day"},
	} {
		t.Run(test.rangeName, func(t *testing.T) {
			store := NewMemoryStore()
			store.CreateResource("settings", AdminResource{ID: gatewaySettingsID, Status: StatusActive, Fields: map[string]any{dashboardTimezoneField: location.String()}})
			start := time.Date(2026, 10, 8, 0, 0, 0, 0, location).AddDate(0, 0, 1-test.days).UTC()
			for index, timestamp := range []time.Time{start.Add(-time.Second), start, now.Add(-time.Second), now, now.Add(time.Second)} {
				id := []string{"before", "start", "inside", "end", "after"}[index]
				record := usageSummaryRecord(id, "project_report", "key_report", "", 10, timestamp)
				record.ModelName = "report-model"
				if err := store.db.Create(&record).Error; err != nil {
					t.Fatal(err)
				}
				log := usageSummaryLog(id, "project_report", "key_report", "", http.StatusOK, timestamp)
				if err := store.db.Create(&log).Error; err != nil {
					t.Fatal(err)
				}
			}
			logs := []RequestLog{
				usageSummaryLog("failed_only", "project_report", "key_report", "", http.StatusBadGateway, now.Add(-time.Second)),
				usageSummaryLog("playground", "admin_playground", "key_report", "", http.StatusBadGateway, now.Add(-time.Second)),
			}
			if err := store.db.Create(&logs).Error; err != nil {
				t.Fatal(err)
			}
			report, err := New(store).usageReportForUser(t.Context(), AdminUser{Role: "admin"}, test.rangeName, now)
			if err != nil {
				t.Fatal(err)
			}
			if report["window_start"] != start.UTC().Format(time.RFC3339) || report["window_end"] != now.UTC().Format(time.RFC3339) {
				t.Fatalf("report window = %v - %v, want %v - %v", report["window_start"], report["window_end"], start, now)
			}
			if report["granularity"] != test.granularity || report["timezone"] != location.String() || report["range"] != test.rangeName {
				t.Fatalf("report metadata = %#v", report)
			}
			summary := report["summary"].(map[string]any)
			if summary["request_count"] != int64(3) || summary["usage_record_count"] != int64(2) || summary["errors"] != int64(1) || summary["total_tokens"] != int64(30) {
				t.Fatalf("summary = %#v, want 3 calls, 2 usage records, 1 error, and 30 tokens", summary)
			}
			models := report["breakdown"].(map[string]any)["models"].([]map[string]any)
			if len(models) != 1 || models[0]["total_tokens"] != int64(30) {
				t.Fatalf("model breakdown = %#v, want only the selected window", models)
			}
			series := report["timeseries"].([]map[string]any)
			wantBuckets := test.days
			if test.rangeName == "today" {
				wantBuckets = 11
			}
			if len(series) != wantBuckets {
				t.Fatalf("bucket count = %d, want %d", len(series), wantBuckets)
			}
			assertUsageReportSeriesMatchesSummary(t, report)
		})
	}
}

func TestUsageReportAllIncludesOldRecordsAndFailureOnlyMonths(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	firstLog := time.Date(2025, 11, 20, 1, 0, 0, 0, time.UTC)
	log := usageSummaryLog("old_failure", "project_report", "key_report", "", http.StatusBadGateway, firstLog)
	if err := store.db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	for index, timestamp := range []time.Time{firstLog.AddDate(0, 1, 0), now.Add(-time.Hour)} {
		record := usageSummaryRecord([]string{"old_record", "current_record"}[index], "project_report", "key_report", "", 10, timestamp)
		if err := store.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	report, err := New(store).usageReportForUser(t.Context(), AdminUser{Role: "admin"}, "all", now)
	if err != nil {
		t.Fatal(err)
	}
	if report["window_start"] != firstLog.Format(time.RFC3339) || report["granularity"] != "month" {
		t.Fatalf("all-time metadata = %#v, want the earliest log and monthly buckets", report)
	}
	series := report["timeseries"].([]map[string]any)
	if len(series) != 12 || series[0]["date"] != "2025-11-01" || series[11]["date"] != "2026-10-01" {
		t.Fatalf("all-time timeseries = %#v, want all 12 months", series)
	}
	if series[0]["errors"] != int64(1) || series[0]["total_tokens"] != int64(0) || series[1]["total_tokens"] != int64(15) {
		t.Fatalf("first months = %#v, want the failure-only month and old tokens", series[:2])
	}
	assertUsageReportSeriesMatchesSummary(t, report)
}

func TestUsageReportAllRetainsMoreThanThirtyOneDailyBuckets(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{ID: gatewaySettingsID, Status: StatusActive, Fields: map[string]any{dashboardTimezoneField: "Asia/Shanghai"}})
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	firstDay := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	record := usageSummaryRecord("old_daily", "project_report", "key_report", "", 10, firstDay.Add(30*time.Minute))
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	report, err := New(store).usageReportForUser(t.Context(), AdminUser{Role: "admin"}, "all", now)
	if err != nil {
		t.Fatal(err)
	}
	series := report["timeseries"].([]map[string]any)
	if report["granularity"] != "day" || len(series) != 40 || series[0]["date"] != "2026-08-30" || series[39]["date"] != "2026-10-08" {
		t.Fatalf("daily all-time report = %#v, want 40 dashboard-local days", report)
	}
	assertUsageReportSeriesMatchesSummary(t, report)
}

func TestUsageReportHourlyBucketsPreserveDSTCalls(t *testing.T) {
	for _, test := range []struct {
		name       string
		zone       string
		now        string
		calls      []string
		wantLength int
	}{
		{name: "spring forward", zone: "America/Chicago", now: "2026-03-09T04:30:00Z", calls: []string{"2026-03-08T07:30:00Z", "2026-03-08T08:30:00Z"}, wantLength: 23},
		{name: "fall back", zone: "America/Chicago", now: "2026-11-02T05:30:00Z", calls: []string{"2026-11-01T06:30:00Z", "2026-11-01T07:30:00Z"}, wantLength: 25},
		{name: "half hour fall back", zone: "Australia/Lord_Howe", now: "2026-04-05T13:15:00Z", calls: []string{"2026-04-04T14:45:00Z", "2026-04-04T15:15:00Z"}, wantLength: 25},
		{name: "midnight spring forward", zone: "America/Santiago", now: "2026-09-07T02:30:00Z", calls: []string{"2026-09-06T04:30:00Z", "2026-09-06T05:30:00Z"}, wantLength: 23},
		{name: "midnight fall back", zone: "America/Havana", now: "2026-11-02T04:30:00Z", calls: []string{"2026-11-01T04:30:00Z", "2026-11-01T05:30:00Z"}, wantLength: 25},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.CreateResource("settings", AdminResource{ID: gatewaySettingsID, Status: StatusActive, Fields: map[string]any{dashboardTimezoneField: test.zone}})
			for index, timestamp := range test.calls {
				log := usageSummaryLog([]string{"first", "second"}[index], "project_report", "key_report", "", http.StatusBadGateway, mustParseUsageReportTime(t, timestamp))
				if err := store.db.Create(&log).Error; err != nil {
					t.Fatal(err)
				}
			}
			report, err := New(store).usageReportForUser(t.Context(), AdminUser{Role: "admin"}, "today", mustParseUsageReportTime(t, test.now))
			if err != nil {
				t.Fatal(err)
			}
			series := report["timeseries"].([]map[string]any)
			if len(series) != test.wantLength {
				t.Fatalf("hour count = %d, want %d", len(series), test.wantLength)
			}
			populated := 0
			for _, point := range series {
				if point["request_count"] == int64(1) {
					populated++
				}
			}
			if populated != 2 {
				t.Fatalf("separate call buckets = %d, want 2: %#v", populated, series)
			}
			assertUsageReportSeriesMatchesSummary(t, report)
		})
	}
}

func TestUsageReportDailyBucketsCrossMidnightDSTTransition(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("settings", AdminResource{ID: gatewaySettingsID, Status: StatusActive, Fields: map[string]any{dashboardTimezoneField: "America/Santiago"}})
	now := mustParseUsageReportTime(t, "2026-09-10T12:00:00Z")
	for index, timestamp := range []string{"2026-09-05T04:30:00Z", "2026-09-06T04:30:00Z", "2026-09-07T03:30:00Z"} {
		log := usageSummaryLog([]string{"before_dst", "dst_day", "after_dst"}[index], "project_report", "key_report", "", http.StatusOK, mustParseUsageReportTime(t, timestamp))
		if err := store.db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	report, err := New(store).usageReportForUser(t.Context(), AdminUser{Role: "admin"}, "7d", now)
	if err != nil {
		t.Fatal(err)
	}
	series := report["timeseries"].([]map[string]any)
	if len(series) != 7 || series[0]["date"] != "2026-09-04" || series[6]["date"] != "2026-09-10" {
		t.Fatalf("daily buckets = %#v, want seven unique calendar dates", series)
	}
	assertUsageReportSeriesMatchesSummary(t, report)
}

func TestUsageReportEmptyRangesAndDefault(t *testing.T) {
	server := New(NewMemoryStore())
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, selectedRange := range []string{"", "today", "7d", "30d", "all"} {
		report, err := server.usageReportForUser(t.Context(), AdminUser{Role: "admin"}, selectedRange, now)
		if err != nil {
			t.Fatal(err)
		}
		if selectedRange == "all" && (report["window_start"] != "" || len(report["timeseries"].([]map[string]any)) != 0) {
			t.Fatalf("empty all-time report = %#v, want no lower boundary and empty series", report)
		}
		if selectedRange == "" && report["range"] != "today" {
			t.Fatalf("default range = %#v, want today", report["range"])
		}
		assertUsageReportSeriesMatchesSummary(t, report)
	}
}

func TestUsageReportEndpointRejectsInvalidRangeAndReportsReadFailure(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	for _, selectedRange := range []string{"yesterday", "31d", "-1", "TODAY"} {
		response := doJSON(t, app, http.MethodGet, "/api/admin/usage/report?range="+selectedRange, nil, "")
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "invalid_usage_range") {
			t.Fatalf("invalid range response = %d %s, want invalid_usage_range", response.Code, response.Body)
		}
	}
	if err := store.db.Migrator().DropTable(&RequestLog{}); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app, http.MethodGet, "/api/admin/usage/report", nil, "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failed report response = %d %s, want 500", response.Code, response.Body)
	}
}

func TestUsageReportRedactsProviderBreakdownsForNonPlatformRoles(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	record := UsageRecord{ID: "report_sensitive", AttributedUserID: "report_user", ProviderID: "sensitive_provider", ProviderResourceID: "sensitive_resource", CreatedAt: now.Add(-time.Hour)}
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"admin", "security_admin", "team_leader", "user"} {
		report, err := New(store).usageReportForUser(t.Context(), AdminUser{ID: "report_user", Role: role}, "all", now)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		containsProvider := strings.Contains(string(body), "sensitive_provider")
		containsResource := strings.Contains(string(body), "sensitive_resource")
		if containsProvider != (role == "admin") || containsResource != (role == "admin") {
			t.Fatalf("provider disclosure for %s = %s", role, body)
		}
	}
}

func mustParseUsageReportTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func assertUsageReportSeriesMatchesSummary(t *testing.T, report map[string]any) {
	t.Helper()
	summary := report["summary"].(map[string]any)
	series := report["timeseries"].([]map[string]any)
	for metric, expected := range summary {
		var actual float64
		for _, point := range series {
			actual += usageSummaryNumber(t, point[metric])
		}
		if actual != usageSummaryNumber(t, expected) {
			t.Fatalf("series %s = %v, summary = %v", metric, actual, expected)
		}
	}
}
