//go:build integration

package server

import (
	"context"
	"testing"
	"time"
)

func testPostgresUsageGroupsAndUTCTimeseries(t *testing.T) {
	t.Helper()
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_usage_groups_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := VerifySchemaSemantics(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_usage_records_api_key_created", "idx_usage_records_attributed_user_created"} {
		if !store.db.Migrator().HasIndex(&UsageRecord{}, index) {
			t.Fatalf("missing usage window index %s", index)
		}
	}
	now := time.Date(2026, 10, 20, 1, 0, 0, 0, time.FixedZone("UTC+08", 8*60*60))
	before := usageSummaryRecord("pg_usage_before", "project_groups", "key_groups", "member_groups", 10, now)
	before.ModelName = "model_groups"
	if err := store.db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	scope := UsageSummaryScope{AttributedUserIDs: []string{"member_groups"}, CreatedAtFrom: now.Add(-time.Hour), CreatedAtBefore: now.Add(time.Hour)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	seen := map[string]int64{}
	err = store.StreamUsageGroups(ctx, scope, []string{"projects", "models", "members"}, func(dimension string, group UsageGroup) {
		seen[dimension] += group.UsageRecordCount
		if dimension == "projects" {
			after := before
			after.ID, after.RequestID = "pg_usage_after", "pg_request_after"
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
			t.Fatalf("%s saw %d usage rows, want one repeatable snapshot", dimension, count)
		}
	}
	days, err := store.QueryUsageTimeseries(ctx, scope)
	if err != nil || len(days) != 1 {
		t.Fatalf("UTC timeseries = %+v, %v", days, err)
	}
	if days[0].Date != "2026-10-19" || days[0].UsageRecordCount != 2 || days[0].TotalTokens != 30 {
		t.Fatalf("UTC day = %+v, want two usage rows on 2026-10-19", days[0])
	}
}
