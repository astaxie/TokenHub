package server

import (
	"fmt"
	"testing"
	"time"
)

func TestUsageBreakdownQueryCountDoesNotGrowWithRecords(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{
		ID:         "prj_breakdown_query_count",
		Name:       "Breakdown query count",
		CostCenter: "CC-BREAKDOWN",
		Status:     StatusActive,
	})
	// Keep background response-job polling out of this query-count unit test.
	server := &Server{store: store}
	admin := AdminUser{ID: "usr_breakdown_admin", Role: "admin", Status: StatusActive}

	insertUsageRecords := func(start, count int) {
		t.Helper()
		records := make([]UsageRecord, count)
		for index := range records {
			number := start + index
			records[index] = UsageRecord{
				ID:        fmt.Sprintf("use_breakdown_%d", number),
				RequestID: fmt.Sprintf("req_breakdown_%d", number),
				ProjectID: project.ID,
				CostUSD:   1,
				CreatedAt: time.Now().UTC(),
			}
		}
		if err := store.db.Create(&records).Error; err != nil {
			t.Fatal(err)
		}
	}

	insertUsageRecords(0, 1)
	server.usageBreakdownForUser(admin)
	smallQueries := countStoreQueries(t, store, func() {
		server.usageBreakdownForUser(admin)
	})
	insertUsageRecords(1, 99)
	var breakdown map[string]any
	largeQueries := countStoreQueries(t, store, func() {
		breakdown = server.usageBreakdownForUser(admin)
	})

	if largeQueries > smallQueries {
		t.Fatalf("usage breakdown query count grew with records: small=%d large=%d", smallQueries, largeQueries)
	}
	costCenters, ok := breakdown["cost_centers"].([]map[string]any)
	if !ok || len(costCenters) != 1 {
		t.Fatalf("cost center breakdown = %#v, want one row", breakdown["cost_centers"])
	}
	if got := costCenters[0]["id"]; got != "CC-BREAKDOWN" {
		t.Fatalf("cost center = %#v, want CC-BREAKDOWN", got)
	}
	if got := costCenters[0]["request_count"]; got != int64(100) {
		t.Fatalf("cost center request count = %#v, want 100", got)
	}
}

func TestUsageBreakdownUsesUnknownForMissingProject(t *testing.T) {
	server := New(NewMemoryStore())
	breakdown := server.usageBreakdownFromRecords([]UsageRecord{{ProjectID: "prj_missing"}}, nil)
	costCenters, ok := breakdown["cost_centers"].([]map[string]any)
	if !ok || len(costCenters) != 1 || costCenters[0]["id"] != "unknown" {
		t.Fatalf("cost center breakdown = %#v, want one unknown row", breakdown["cost_centers"])
	}
}

func TestCostCenterForProjectPreservesFallbackOrder(t *testing.T) {
	teamsByID := map[string]AdminResource{
		"team_cost_center": {ID: "team_cost_center", Fields: map[string]any{"cost_center": "CC-TEAM"}},
		"team_fallback":    {ID: "team_fallback"},
	}
	quotasByID := map[string]AdminResource{
		"quota_cost_center": {ID: "quota_cost_center", Fields: map[string]any{"cost_center": "CC-QUOTA"}},
	}
	tests := []struct {
		name    string
		project Project
		want    string
	}{
		{
			name:    "project overrides team and quota",
			project: Project{ID: "prj_direct", TeamID: "team_cost_center", DefaultQuotaRef: "quota_cost_center", CostCenter: "CC-PROJECT"},
			want:    "CC-PROJECT",
		},
		{
			name:    "team overrides quota",
			project: Project{ID: "prj_team", TeamID: "team_cost_center", DefaultQuotaRef: "quota_cost_center"},
			want:    "CC-TEAM",
		},
		{
			name:    "quota follows team without cost center",
			project: Project{ID: "prj_quota", TeamID: "team_fallback", DefaultQuotaRef: "quota_cost_center"},
			want:    "CC-QUOTA",
		},
		{
			name:    "team id fallback",
			project: Project{ID: "prj_team_fallback", TeamID: "team_fallback"},
			want:    "team_fallback",
		},
		{
			name:    "project id fallback",
			project: Project{ID: "prj_fallback"},
			want:    "project:prj_fallback",
		},
		{
			name: "unknown fallback",
			want: "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := costCenterForProject(test.project, teamsByID, quotasByID); got != test.want {
				t.Fatalf("cost center = %q, want %q", got, test.want)
			}
		})
	}
}
