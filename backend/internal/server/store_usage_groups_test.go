package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestUsageGroupsMatchLegacyBreakdown(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	store.CreateResource("teams", AdminResource{ID: "group_team", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-TEAM"}})
	leader := createUsageSummaryUser(t, store, AdminUser{ID: "group_leader", Role: "team_leader", TeamID: "group_team"})
	member := createUsageSummaryUser(t, store, AdminUser{ID: "group_member", Role: "user", TeamID: "group_team"})
	other := createUsageSummaryUser(t, store, AdminUser{ID: "group_other", Role: "user"})
	project := store.CreateProject(Project{ID: "group_project", Name: "Grouped project", TeamID: "group_team", OwnerUserID: member.ID})
	otherProject := store.CreateProject(Project{ID: "group_other_project", Name: "Other project", OwnerUserID: other.ID})
	key := createUsageSummaryKey(t, store, APIKey{ID: "group_key", ProjectID: project.ID, OwnerUserID: member.ID})
	otherKey := createUsageSummaryKey(t, store, APIKey{ID: "group_other_key", ProjectID: otherProject.ID, OwnerUserID: other.ID})
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	records := []UsageRecord{
		usageSummaryRecord("group_attribution", otherProject.ID, otherKey.ID, member.ID, 10, now),
		usageSummaryRecord("group_owner", project.ID, key.ID, "", 20, now),
		usageSummaryRecord("group_project_owner", project.ID, "deleted_key", "", 30, now),
		usageSummaryRecord("group_empty", "", "", "", 40, now),
		usageSummaryRecord("group_literal_unknown", "unknown", "unknown", "", 50, now),
		usageSummaryRecord("group_old", project.ID, key.ID, "", 60, now.AddDate(-1, 0, 0)),
	}
	for index := range records {
		records[index].ModelName = fmt.Sprintf("model_%d", index%2)
		records[index].ProviderID = "provider_group"
		records[index].ProviderResourceID = "resource_group"
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	for _, user := range []AdminUser{{Role: "admin"}, {Role: "security_admin"}, leader, member, other, {ID: "nobody", Role: "user"}} {
		t.Run(user.Role+"_"+user.ID, func(t *testing.T) {
			for _, windowed := range []bool{false, true} {
				scope := server.usageSummaryQueryForUser(user).UsageRecords
				visible := server.filterUsageRecordsForUser(user, records)
				if windowed {
					scope.CreatedAtFrom, scope.CreatedAtBefore = now, now.Add(time.Second)
					filtered := make([]UsageRecord, 0)
					for _, record := range visible {
						if !record.CreatedAt.Before(scope.CreatedAtFrom) && record.CreatedAt.Before(scope.CreatedAtBefore) {
							filtered = append(filtered, record)
						}
					}
					visible = filtered
				}
				projects := indexProjectsByID(store.ListProjects())
				expected := server.usageBreakdownFromRecords(visible, projects)
				expected["members"] = server.aggregateUsageByMember(user, visible, projects)
				expected["api_keys"] = aggregateUsage(visible, func(record UsageRecord) string { return record.APIKeyID })
				actual, err := server.usageBreakdownForScope(t.Context(), user, scope, true, false)
				if err != nil {
					t.Fatal(err)
				}
				for dimension, rows := range expected {
					want := usageRowsByID(rows.([]map[string]any))
					got := usageRowsByID(actual[dimension].([]map[string]any))
					for id, expectedRow := range want {
						actualRow := got[id]
						if actualRow == nil {
							t.Fatalf("windowed=%t %s missing %s", windowed, dimension, id)
						}
						if math.Abs(actualRow["estimated_cost_usd"].(float64)-expectedRow["estimated_cost_usd"].(float64)) > 1e-12 {
							t.Fatalf("windowed=%t %s %s cost = %v, want %v", windowed, dimension, id, actualRow["estimated_cost_usd"], expectedRow["estimated_cost_usd"])
						}
						delete(actualRow, "estimated_cost_usd")
						delete(expectedRow, "estimated_cost_usd")
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("windowed=%t %s = %#v, want %#v", windowed, dimension, got, want)
					}
				}
			}
		})
	}
}

func usageRowsByID(rows []map[string]any) map[string]map[string]any {
	result := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		result[row["id"].(string)] = row
	}
	return result
}

func TestUsageTimeseriesPreservesUTCRollingWindow(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	server := &Server{store: store}
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.FixedZone("UTC+08", 8*60*60))
	first := now.UTC().AddDate(0, 0, -2)
	end := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	timestamps := []time.Time{first.Add(-time.Nanosecond), first, first.Add(time.Hour), end.Add(-time.Second), end}
	for index, timestamp := range timestamps {
		record := usageSummaryRecord(fmt.Sprintf("timeseries_%d", index), "prj_series", "key_series", "", 10, timestamp)
		if err := store.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	series, err := server.usageTimeseriesForUser(t.Context(), AdminUser{Role: "admin"}, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 3 || series[0]["date"] != "2026-10-18" || series[2]["date"] != "2026-10-20" {
		t.Fatalf("UTC timeseries = %#v", series)
	}
	for index, count := range []int64{2, 0, 1} {
		if series[index]["request_count"] != count || series[index]["total_tokens"] != count*15 {
			t.Fatalf("day %d = %#v, want %d usage records", index, series[index], count)
		}
	}
}

func TestUsageWindowQueriesUseTimestampIndex(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, scope := range []UsageSummaryScope{
		{Global: true, CreatedAtFrom: start, CreatedAtBefore: start.AddDate(0, 0, 1)},
		{AttributedUserIDs: []string{"user_plan"}, ProjectIDs: []string{"project_plan"}, APIKeyIDs: []string{"key_plan"}, CreatedAtFrom: start, CreatedAtBefore: start.AddDate(0, 0, 1)},
	} {
		queries := map[string]*gorm.DB{}
		for _, dimension := range []string{"projects", "models", "members", "providers", "provider_resources", "api_keys"} {
			query, err := store.usageGroupQuery(t.Context(), scope, dimension)
			if err != nil {
				t.Fatal(err)
			}
			queries[dimension] = query
		}
		timeline, err := store.usageTimeseriesQuery(t.Context(), scope)
		if err != nil {
			t.Fatal(err)
		}
		queries["timeseries"] = timeline
		for name, query := range queries {
			t.Run(fmt.Sprintf("global_%t/%s", scope.Global, name), func(t *testing.T) {
				var rows []UsageGroup
				statement := query.Session(&gorm.Session{DryRun: true}).Find(&rows).Statement
				var plan []struct{ Detail string }
				if err := store.db.Raw("EXPLAIN QUERY PLAN "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
					t.Fatal(err)
				}
				var details []string
				for _, step := range plan {
					details = append(details, step.Detail)
				}
				joined := strings.Join(details, "\n")
				indexes := []string{"idx_usage_records_created_at (created_at>? AND created_at<?)"}
				if !scope.Global {
					indexes = []string{
						"idx_usage_records_api_key_created (api_key_id=? AND created_at>? AND created_at<?)",
						"idx_usage_records_project_created (project_id=? AND created_at>? AND created_at<?)",
						"idx_usage_records_attributed_user_created (attributed_user_id=? AND created_at>? AND created_at<?)",
					}
				}
				for _, index := range indexes {
					if !strings.Contains(joined, index) || strings.Contains(joined, "SCAN usage_records") {
						t.Fatalf("usage query must search indexed time windows using %s:\n%s", index, joined)
					}
				}
			})
		}
	}
}

func TestUsageQueriesStopAtCanceledContextAndReleaseReadPool(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	record := usageSummaryRecord("cancel_group", "project_cancel", "key_cancel", "", 1, time.Now().UTC())
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	count := 0
	err := store.StreamUsageGroups(ctx, UsageSummaryScope{Global: true}, []string{"projects", "models"}, func(string, UsageGroup) {
		count++
		cancel()
	})
	if !errors.Is(err, context.Canceled) || count != 1 {
		t.Fatalf("group cancellation count=%d error=%v, want one group and cancellation", count, err)
	}
	if _, err := store.QueryUsageTimeseries(ctx, UsageSummaryScope{Global: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("timeseries cancellation = %v", err)
	}
	if _, err := store.QueryUsageSummary(ctx, UsageSummaryQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("summary cancellation = %v", err)
	}
	pool, err := store.analyticsDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	// database/sql may win the cancellation rollback before GORM's deferred
	// Rollback. ErrTxDone then precedes the asynchronous connection release.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for stats := pool.Stats(); stats.InUse != 0; stats = pool.Stats() {
		select {
		case <-deadline.C:
			t.Fatalf("canceled usage queries retained read connections after rollback deadline: %+v", pool.Stats())
		case <-poll.C:
		}
	}
}

func TestUsageHandlersReportGroupAndTimeseriesFailures(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	app := New(failingUsageGroupsStore{Store: store}).Handler()
	for _, path := range []string{"/api/admin/usage/breakdown", "/api/admin/usage/daily", "/api/admin/usage/timeseries"} {
		response := doJSON(t, app, http.MethodGet, path, nil, "")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s returned %d, want 500: %s", path, response.Code, response.Body)
		}
	}
}

type failingUsageGroupsStore struct{ Store }

func (failingUsageGroupsStore) StreamUsageGroups(context.Context, UsageSummaryScope, []string, func(string, UsageGroup)) error {
	return errors.New("forced usage grouping failure")
}

func (failingUsageGroupsStore) QueryUsageTimeseries(context.Context, UsageSummaryScope) ([]UsageDay, error) {
	return nil, errors.New("forced usage timeseries failure")
}

func TestUsageQueriesUseIsolatedAnalyticsPool(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	record := usageSummaryRecord("isolated_usage", "project_isolated", "key_isolated", "", 10, now)
	if err := store.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	pool, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	scope := UsageSummaryScope{Global: true, CreatedAtFrom: now.Add(-time.Hour), CreatedAtBefore: now.Add(time.Hour)}
	summary, err := store.QueryUsageSummary(ctx, UsageSummaryQuery{UsageRecords: scope, RequestLogs: scope})
	if err != nil || summary.UsageRecordCount != 1 {
		t.Fatalf("summary with occupied writer pool = %+v, %v", summary, err)
	}
	groups := 0
	if err := store.StreamUsageGroups(ctx, scope, []string{"projects", "models", "members"}, func(string, UsageGroup) { groups++ }); err != nil || groups != 3 {
		t.Fatalf("groups with occupied writer pool = %d, %v", groups, err)
	}
	if days, err := store.QueryUsageTimeseries(ctx, scope); err != nil || len(days) != 1 {
		t.Fatalf("timeseries with occupied writer pool = %+v, %v", days, err)
	}
	if err := store.StreamUsageReportRecords(ctx, UsageSummaryQuery{UsageRecords: scope, RequestLogs: scope}, func(UsageRecord) {}, func(UsageReportRequest) {}); err != nil {
		t.Fatalf("report with occupied writer pool: %v", err)
	}
}

func TestUsageGroupDimensionsShareReadSnapshot(t *testing.T) {
	store, err := NewSQLiteStore("sqlite://" + filepath.Join(t.TempDir(), "usage-snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	before := usageSummaryRecord("before_snapshot", "project_snapshot", "key_snapshot", "", 10, now)
	before.ModelName = "model_snapshot"
	if err := store.db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	seen := map[string]int64{}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = store.StreamUsageGroups(ctx, UsageSummaryScope{Global: true}, []string{"projects", "models", "members"}, func(dimension string, group UsageGroup) {
		seen[dimension] += group.UsageRecordCount
		if dimension == "projects" {
			after := usageSummaryRecord("after_snapshot", "project_snapshot", "key_snapshot", "", 10, now)
			after.ModelName = "model_snapshot"
			if err := store.db.Create(&after).Error; err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for dimension, count := range seen {
		if count != 1 {
			t.Fatalf("%s saw %d usage rows, want original snapshot only", dimension, count)
		}
	}
}

func TestUsageHelperDeadlineIncludesMetadataReads(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	pool, err := store.criticalReadDatabase().DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	connection, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	server := &Server{store: store}
	for _, name := range []string{"summary", "breakdown", "daily", "timeseries", "report"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			user := AdminUser{ID: "member_timeout", Role: "user"}
			var err error
			switch name {
			case "summary":
				_, err = server.usageSummaryForUser(ctx, user)
			case "breakdown":
				_, err = server.usageBreakdownForUser(ctx, user)
			case "daily":
				_, err = server.usageDailyForUser(ctx, user, time.Now())
			case "timeseries":
				_, err = server.usageTimeseriesForUser(ctx, user, 31, time.Now())
			case "report":
				_, err = server.usageReportForUser(ctx, user, "today", time.Now())
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("metadata deadline error = %v, want deadline exceeded", err)
			}
		})
	}
}
