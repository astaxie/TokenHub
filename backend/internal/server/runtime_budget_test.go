package server

import (
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"
)

func addRuntimeBudgetUsage(t *testing.T, store *MemoryStore, projectID string, cost float64, at time.Time) {
	t.Helper()
	if err := store.db.Create(&UsageRecord{
		ID:        NewID("usage"),
		RequestID: NewID("req"),
		ProjectID: projectID,
		ModelName: "gpt-4.1-mini",
		CostUSD:   cost,
		CreatedAt: at,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func addRuntimeBudgetResource(t *testing.T, store *MemoryStore, name string, fields map[string]any) {
	t.Helper()
	budget := store.CreateResource("budgets", AdminResource{Name: name, Status: StatusActive, Fields: fields})
	if budget.ID == "" {
		t.Fatalf("failed to create budget %s", name)
	}
}

func addRuntimeBudgetProject(t *testing.T, store *MemoryStore, project Project) Project {
	t.Helper()
	created := store.CreateProject(project)
	if created.ID == "" {
		t.Fatalf("failed to create project %s", project.Name)
	}
	return created
}

// runtimeBudgetNow returns the reading each test uses for both its usage
// fixtures and the enforcement call. checkRuntimeBudget takes the clock from
// its caller, so pinning one value keeps the two in the same billing period
// even when the test runs across a period rollover.
func runtimeBudgetNow(t *testing.T) time.Time {
	t.Helper()
	return time.Now().UTC()
}

// countStoreStatements counts the statements a store operation sends to the
// database. Finder calls run the query callbacks while aggregates scanned with
// Scan run the row callbacks, so both are needed for a complete count.
func countStoreStatements(t *testing.T, store *MemoryStore, fn func()) int {
	t.Helper()
	suffix := NewID("callback")
	queryName := "test:count-query:" + suffix
	rowName := "test:count-row:" + suffix
	count := 0
	record := func(*gorm.DB) { count++ }
	if err := store.db.Callback().Query().Before("gorm:query").Register(queryName, record); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Callback().Row().Before("gorm:row").Register(rowName, record); err != nil {
		t.Fatal(err)
	}
	fn()
	if err := store.db.Callback().Query().Remove(queryName); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Callback().Row().Remove(rowName); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestCheckRuntimeBudgetScopes pins the spend each budget scope attributes to a
// call, including the cost center fallback chain and the records the scopes
// deliberately ignore.
func TestCheckRuntimeBudgetScopes(t *testing.T) {
	now := runtimeBudgetNow(t)
	previousMonth := periodStart(now.Format("2006-01")).Add(-time.Hour)

	for _, test := range []struct {
		name        string
		setup       func(t *testing.T, store *MemoryStore) Project
		budgets     func(project Project) []map[string]any
		wantBlocked bool
	}{
		{
			name: "project scope below amount admits the call",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Under"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID, "amount_usd": 1}}
			},
		},
		{
			name: "project scope at amount rejects the call",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Over"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.6, now)
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID, "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "project scope ignores other projects",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Isolated"})
				other := addRuntimeBudgetProject(t, store, Project{Name: "Noisy Neighbour"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.5, now)
				addRuntimeBudgetUsage(t, store, other.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID, "amount_usd": 1}}
			},
		},
		{
			name: "global scope counts usage left by deleted projects",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Global Member"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, "prj_deleted", 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "global", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "team scope sums the projects sharing the primary team",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_alpha", Name: "Alpha", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Alpha One", TeamID: "team_alpha"})
				sibling := addRuntimeBudgetProject(t, store, Project{Name: "Alpha Two", TeamID: "team_alpha"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, sibling.ID, 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "team", "scope_id": "team_alpha", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "team scope ignores secondary team membership",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_alpha", Name: "Alpha", Status: StatusActive})
				store.CreateResource("teams", AdminResource{ID: "team_beta", Name: "Beta", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Alpha One", TeamID: "team_alpha"})
				shared := addRuntimeBudgetProject(t, store, Project{Name: "Beta One", TeamID: "team_beta"})
				if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: shared.ID, TeamID: "team_alpha", Role: "viewer"}); err != nil {
					t.Fatal(err)
				}
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, shared.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "team", "scope_id": "team_alpha", "amount_usd": 1}}
			},
		},
		{
			name: "team scope ignores usage left by deleted projects",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_alpha", Name: "Alpha", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Alpha One", TeamID: "team_alpha"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, "prj_deleted", 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "team", "scope_id": "team_alpha", "amount_usd": 1}}
			},
		},
		{
			name: "team scope counts disabled project usage",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_alpha", Name: "Alpha", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Alpha One", TeamID: "team_alpha"})
				retired := addRuntimeBudgetProject(t, store, Project{Name: "Alpha Retired", TeamID: "team_alpha", Status: StatusDisabled})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, retired.ID, 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "team", "scope_id": "team_alpha", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope reads the project field first",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_cc", Name: "CC Team", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-TEAM"}})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Direct", TeamID: "team_cc", CostCenter: "CC-DIRECT"})
				peer := addRuntimeBudgetProject(t, store, Project{Name: "Direct Peer", CostCenter: "CC-DIRECT"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, peer.ID, 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "CC-DIRECT", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope falls back to the team field",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_cc", Name: "CC Team", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-TEAM"}})
				store.CreateResource("quota-policies", AdminResource{ID: "quota_cc", Name: "CC Quota", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-QUOTA"}})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Team Sourced", TeamID: "team_cc", DefaultQuotaRef: "quota_cc"})
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "CC-TEAM", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope falls back to the quota policy field",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("quota-policies", AdminResource{ID: "quota_cc", Name: "CC Quota", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-QUOTA"}})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Quota Sourced", DefaultQuotaRef: "quota_cc"})
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "CC-QUOTA", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope falls back to the team id",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_plain", Name: "Plain", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Team Id Sourced", TeamID: "team_plain"})
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "team_plain", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope falls back to the project id",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Project Id Sourced"})
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "project:" + project.ID, "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope counts disabled project usage",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "CC Active", CostCenter: "CC-MIXED"})
				retired := addRuntimeBudgetProject(t, store, Project{Name: "CC Retired", CostCenter: "CC-MIXED", Status: StatusDisabled})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, retired.ID, 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "CC-MIXED", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "cost center scope ignores usage left by deleted projects",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "CC Only", CostCenter: "CC-ONLY"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, "prj_deleted", 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "CC-ONLY", "amount_usd": 1}}
			},
		},
		{
			name: "cost center scope resolves peer projects through a disabled team",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_cc", Name: "CC Team", Status: StatusDisabled, Fields: map[string]any{"cost_center": "CC-TEAM"}})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Team Sourced", TeamID: "team_cc"})
				peer := addRuntimeBudgetProject(t, store, Project{Name: "Team Sourced Peer", TeamID: "team_cc"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, peer.ID, 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost_center", "scope_id": "CC-TEAM", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "organization scope behaves like global scope",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Organization Member"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.4, now)
				addRuntimeBudgetUsage(t, store, "prj_deleted", 0.7, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "organization", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "hyphenated cost-center scope is recognised",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Hyphenated", CostCenter: "CC-HYPHEN"})
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "cost-center", "scope_id": "CC-HYPHEN", "amount_usd": 1}}
			},
			wantBlocked: true,
		},
		{
			name: "legacy scope id fields are honoured",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_legacy", Name: "Legacy", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Legacy Fields", TeamID: "team_legacy", CostCenter: "CC-LEGACY"})
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{
					{"scope": "project", "project_id": project.ID, "amount_usd": 100},
					{"scope": "team", "team_id": "team_legacy", "amount_usd": 100},
					{"scope": "cost_center", "cost_center": "CC-LEGACY", "amount_usd": 1},
				}
			},
			wantBlocked: true,
		},
		{
			name: "budgets scoped elsewhere leave the project aggregate narrowed",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_mine", Name: "Mine", Status: StatusActive})
				store.CreateResource("teams", AdminResource{ID: "team_other", Name: "Other", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Narrowed", TeamID: "team_mine", CostCenter: "CC-MINE"})
				other := addRuntimeBudgetProject(t, store, Project{Name: "Elsewhere", TeamID: "team_other", CostCenter: "CC-OTHER"})
				addRuntimeBudgetUsage(t, store, project.ID, 0.5, now)
				addRuntimeBudgetUsage(t, store, other.ID, 100, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{
					{"scope": "project", "scope_id": project.ID, "amount_usd": 1},
					{"scope": "team", "scope_id": "team_other", "amount_usd": 0.01},
					{"scope": "cost_center", "scope_id": "CC-OTHER", "amount_usd": 0.01},
				}
			},
		},
		{
			name: "previous period usage stays outside the window",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Fresh Month"})
				addRuntimeBudgetUsage(t, store, project.ID, 5, previousMonth)
				addRuntimeBudgetUsage(t, store, project.ID, 0.1, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID, "amount_usd": 1}}
			},
		},
		{
			name: "budget for another period is skipped",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Other Period"})
				addRuntimeBudgetUsage(t, store, project.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{
					"scope":      "project",
					"scope_id":   project.ID,
					"period_ref": previousMonth.Format("2006-01"),
					"amount_usd": 1,
				}}
			},
		},
		{
			name: "warn enforcement never blocks",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Warn Only"})
				addRuntimeBudgetUsage(t, store, project.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID, "amount_usd": 1, "enforcement": "warn"}}
			},
		},
		{
			name: "monitor enforcement never blocks",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Monitor Only"})
				addRuntimeBudgetUsage(t, store, project.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID, "amount_usd": 1, "enforcement": "monitor"}}
			},
		},
		{
			name: "budget without an amount never blocks",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "No Amount"})
				addRuntimeBudgetUsage(t, store, project.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": project.ID}}
			},
		},
		{
			name: "budget for another project never blocks",
			setup: func(t *testing.T, store *MemoryStore) Project {
				project := addRuntimeBudgetProject(t, store, Project{Name: "Unbudgeted"})
				addRuntimeBudgetUsage(t, store, project.ID, 5, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{{"scope": "project", "scope_id": "prj_elsewhere", "amount_usd": 1}}
			},
		},
		{
			name: "any exceeded budget among several blocks",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_alpha", Name: "Alpha", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Many Budgets", TeamID: "team_alpha", CostCenter: "CC-MANY"})
				addRuntimeBudgetUsage(t, store, project.ID, 2, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{
					{"scope": "project", "scope_id": project.ID, "amount_usd": 100},
					{"scope": "team", "scope_id": "team_alpha", "amount_usd": 100},
					{"scope": "cost_center", "scope_id": "CC-MANY", "amount_usd": 1},
					{"scope": "global", "amount_usd": 100},
				}
			},
			wantBlocked: true,
		},
		{
			name: "several budgets all below their amounts admit the call",
			setup: func(t *testing.T, store *MemoryStore) Project {
				store.CreateResource("teams", AdminResource{ID: "team_alpha", Name: "Alpha", Status: StatusActive})
				project := addRuntimeBudgetProject(t, store, Project{Name: "Many Budgets", TeamID: "team_alpha", CostCenter: "CC-MANY"})
				addRuntimeBudgetUsage(t, store, project.ID, 2, now)
				return project
			},
			budgets: func(project Project) []map[string]any {
				return []map[string]any{
					{"scope": "project", "scope_id": project.ID, "amount_usd": 100},
					{"scope": "team", "scope_id": "team_alpha", "amount_usd": 100},
					{"scope": "cost_center", "scope_id": "CC-MANY", "amount_usd": 100},
					{"scope": "global", "amount_usd": 100},
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			project := test.setup(t, store)
			for index, fields := range test.budgets(project) {
				addRuntimeBudgetResource(t, store, "budget-"+strconv.Itoa(index), fields)
			}
			err := store.checkRuntimeBudget(store.db, project, now)
			if test.wantBlocked && err != ErrBudgetExceeded {
				t.Fatalf("expected budget_exceeded, got %v", err)
			}
			if !test.wantBlocked && err != nil {
				t.Fatalf("expected the call to be admitted, got %v", err)
			}
		})
	}
}

// TestCheckRuntimeBudgetSkipsLookupsWithoutApplicableBudgets keeps admission free
// for deployments whose budgets cannot block the call.
func TestCheckRuntimeBudgetSkipsLookupsWithoutApplicableBudgets(t *testing.T) {
	now := runtimeBudgetNow(t)
	previousPeriod := periodStart(now.Format("2006-01")).Add(-time.Hour).Format("2006-01")

	for _, test := range []struct {
		name    string
		budgets []map[string]any
	}{
		{name: "no budgets at all"},
		{
			name: "only budgets that cannot enforce",
			budgets: []map[string]any{
				{"scope": "global", "amount_usd": 1, "enforcement": "warn"},
				{"scope": "global", "amount_usd": 1, "enforcement": "monitor"},
				{"scope": "global", "amount_usd": 0},
				{"scope": "global", "amount_usd": 1, "period_ref": previousPeriod},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			// The project resolves its cost center through a team and a quota
			// policy, which must stay unread while no budget can enforce.
			store.CreateResource("teams", AdminResource{ID: "team_free", Name: "Free", Status: StatusActive})
			store.CreateResource("quota-policies", AdminResource{ID: "quota_free", Name: "Free Quota", Status: StatusActive, Fields: map[string]any{"cost_center": "CC-FREE"}})
			project := addRuntimeBudgetProject(t, store, Project{Name: "Budget Free", TeamID: "team_free", DefaultQuotaRef: "quota_free"})
			for index := 0; index < 20; index++ {
				addRuntimeBudgetUsage(t, store, project.ID, 1, now)
			}
			for index, fields := range test.budgets {
				addRuntimeBudgetResource(t, store, "budget-"+strconv.Itoa(index), fields)
			}
			var err error
			statements := countStoreStatements(t, store, func() {
				err = store.checkRuntimeBudget(store.db, project, now)
			})
			if err != nil {
				t.Fatalf("expected the call to be admitted, got %v", err)
			}
			if statements != 1 {
				t.Fatalf("expected only the budget lookup, got %d statements", statements)
			}
		})
	}
}

// TestCheckRuntimeBudgetUsesConstantQueries pins the aggregation to a fixed
// number of statements no matter how many budgets, projects, or usage records
// the period holds.
func TestCheckRuntimeBudgetUsesConstantQueries(t *testing.T) {
	now := runtimeBudgetNow(t)
	build := func(t *testing.T, projects int, records int, budgetsPerScope int) (*MemoryStore, Project) {
		t.Helper()
		store := NewMemoryStore()
		store.CreateResource("teams", AdminResource{ID: "team_scale", Name: "Scale", Status: StatusActive})
		project := addRuntimeBudgetProject(t, store, Project{Name: "Scale Primary", TeamID: "team_scale", CostCenter: "CC-SCALE"})
		for index := 0; index < projects; index++ {
			peer := addRuntimeBudgetProject(t, store, Project{
				Name:       "Scale Peer " + strconv.Itoa(index),
				TeamID:     "team_scale",
				CostCenter: "CC-SCALE",
			})
			for record := 0; record < records; record++ {
				addRuntimeBudgetUsage(t, store, peer.ID, 0.01, now)
			}
		}
		for index := 0; index < records; index++ {
			addRuntimeBudgetUsage(t, store, project.ID, 0.01, now)
		}
		for index := 0; index < budgetsPerScope; index++ {
			suffix := strconv.Itoa(index)
			addRuntimeBudgetResource(t, store, "project-"+suffix, map[string]any{"scope": "project", "scope_id": project.ID, "amount_usd": 1e9})
			addRuntimeBudgetResource(t, store, "team-"+suffix, map[string]any{"scope": "team", "scope_id": "team_scale", "amount_usd": 1e9})
			addRuntimeBudgetResource(t, store, "cost-center-"+suffix, map[string]any{"scope": "cost_center", "scope_id": "CC-SCALE", "amount_usd": 1e9})
			addRuntimeBudgetResource(t, store, "global-"+suffix, map[string]any{"scope": "global", "amount_usd": 1e9})
		}
		return store, project
	}

	smallStore, smallProject := build(t, 1, 1, 1)
	largeStore, largeProject := build(t, 20, 10, 5)
	var smallErr, largeErr error
	smallStatements := countStoreStatements(t, smallStore, func() {
		smallErr = smallStore.checkRuntimeBudget(smallStore.db, smallProject, now)
	})
	largeStatements := countStoreStatements(t, largeStore, func() {
		largeErr = largeStore.checkRuntimeBudget(largeStore.db, largeProject, now)
	})
	if smallErr != nil || largeErr != nil {
		t.Fatalf("expected both calls to be admitted, got %v and %v", smallErr, largeErr)
	}
	if smallStatements != largeStatements {
		t.Fatalf("statement count grew with rows: small=%d large=%d", smallStatements, largeStatements)
	}
	// Budgets, teams with quota policies, the spend aggregate, and projects.
	if largeStatements > 4 {
		t.Fatalf("expected at most one statement per lookup, got %d", largeStatements)
	}
}

// TestCheckRuntimeBudgetResolvesPeriodFromCallerClock keeps budget enforcement
// on the same reading admission uses for the quota buckets. Reading the local
// clock instead measured spend against a different month than the one the call
// was admitted into whenever the database host disagreed across a rollover.
func TestCheckRuntimeBudgetResolvesPeriodFromCallerClock(t *testing.T) {
	now := runtimeBudgetNow(t)
	previousPeriod := periodStart(now.Format("2006-01")).Add(-time.Hour)

	store := NewMemoryStore()
	project := addRuntimeBudgetProject(t, store, Project{Name: "Period Clock"})
	addRuntimeBudgetUsage(t, store, project.ID, 10, previousPeriod)
	addRuntimeBudgetResource(t, store, "global-budget", map[string]any{"scope": "global", "amount_usd": 5})

	if err := store.checkRuntimeBudget(store.db, project, previousPeriod); err != ErrBudgetExceeded {
		t.Fatalf("spend inside the supplied period should block the call, got %v", err)
	}
	if err := store.checkRuntimeBudget(store.db, project, now); err != nil {
		t.Fatalf("spend outside the supplied period should admit the call, got %v", err)
	}
}
