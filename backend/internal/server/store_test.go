package server

import (
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestPriceUsageAppliesConfiguredCacheReadPrice(t *testing.T) {
	model := Model{
		Modality:               "chat",
		InputPriceUSDPer1M:     2,
		CacheReadPriceUSDPer1M: 0.5,
		OutputPriceUSDPer1M:    8,
	}
	usage := priceUsage(model, Usage{
		PromptTokens:      1000,
		CachedInputTokens: 400,
		CompletionTokens:  100,
	})

	if math.Abs(usage.CostUSD-0.0022) > 1e-12 {
		t.Fatalf("cost = %.12f, want 0.0022", usage.CostUSD)
	}
	if usage.TotalTokens != 1100 {
		t.Fatalf("total tokens = %d, want 1100", usage.TotalTokens)
	}
}

// TestPriceUsageClampsNegativeUpstreamUsage guards the quota ledger against
// upstreams that report negative counts: priceUsage is the single choke point
// before addUsage, so anything negative surviving it would shrink the day and
// month quota counters and let a key spend past its configured limits.
func TestPriceUsageClampsNegativeUpstreamUsage(t *testing.T) {
	model := Model{
		Modality:            "chat",
		InputPriceUSDPer1M:  2,
		OutputPriceUSDPer1M: 8,
	}
	cases := []struct {
		name           string
		usage          Usage
		wantPrompt     int64
		wantCompletion int64
		wantTotal      int64
		wantCost       float64
	}{
		{
			name:  "negative total only",
			usage: Usage{TotalTokens: -1_000_000},
		},
		{
			name:           "negative prompt keeps completion",
			usage:          Usage{PromptTokens: -500, CompletionTokens: 200},
			wantCompletion: 200,
			wantTotal:      200,
			wantCost:       200 * 8.0 / 1_000_000,
		},
		{
			name:           "negative total rebuilt from parts",
			usage:          Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: -3},
			wantPrompt:     100,
			wantCompletion: 50,
			wantTotal:      150,
			wantCost:       (100*2.0 + 50*8.0) / 1_000_000,
		},
		{
			name:  "negative prompt does not drag cached tokens negative",
			usage: Usage{PromptTokens: -100, CachedInputTokens: 50, CompletionTokens: -5},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := priceUsage(model, tt.usage)
			if got.PromptTokens != tt.wantPrompt || got.CompletionTokens != tt.wantCompletion || got.TotalTokens != tt.wantTotal {
				t.Fatalf("clamped tokens = prompt %d completion %d total %d, want prompt %d completion %d total %d",
					got.PromptTokens, got.CompletionTokens, got.TotalTokens, tt.wantPrompt, tt.wantCompletion, tt.wantTotal)
			}
			if got.CachedInputTokens < 0 {
				t.Fatalf("cached input tokens = %d, want >= 0", got.CachedInputTokens)
			}
			if math.Abs(got.CostUSD-tt.wantCost) > 1e-12 {
				t.Fatalf("cost = %.12f, want %.12f", got.CostUSD, tt.wantCost)
			}
		})
	}
}

func TestEffectiveCacheReadPriceUsesCategoryEstimateWhenUnconfigured(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		want  float64
	}{
		{
			name:  "default ten percent",
			model: Model{Name: "gpt-test", Category: "openai", InputPriceUSDPer1M: 2},
			want:  0.2,
		},
		{
			name:  "deepseek two percent",
			model: Model{Name: "deepseek-test", Category: "deepseek", InputPriceUSDPer1M: 2},
			want:  0.04,
		},
		{
			name:  "deepseek v4 pro current ratio",
			model: Model{Name: "deepseek-v4-pro", Category: "deepseek", InputPriceUSDPer1M: 2},
			want:  2.0 / 120,
		},
		{
			name: "legacy metadata remains supported",
			model: Model{
				Name:               "legacy",
				InputPriceUSDPer1M: 2,
				Metadata:           map[string]string{"cached_input_price_usd_per_1m": "0.3"},
			},
			want: 0.3,
		},
		{
			name: "embedding cache price is unavailable",
			model: Model{
				Name:                   "embedding",
				Modality:               "embedding",
				InputPriceUSDPer1M:     2,
				CacheReadPriceUSDPer1M: 0.3,
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveCacheReadPriceUSDPer1M(tt.model); math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("effective cache price = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestEmbeddingModelDoesNotStoreCacheReadPrice(t *testing.T) {
	store := NewMemoryStore()
	created := store.AddModel(Model{
		Name:                   "embedding-cache-price",
		Modality:               "embedding",
		CacheReadPriceUSDPer1M: 0.3,
		EmbeddingPriceUSDPer1M: 0.5,
	})
	if created.CacheReadPriceUSDPer1M != 0 {
		t.Fatalf("created embedding cache read price = %v, want 0", created.CacheReadPriceUSDPer1M)
	}

	updated, err := store.UpdateModel(created.Name, Model{
		Modality:               "embedding",
		CacheReadPriceUSDPer1M: 0.4,
		EmbeddingPriceUSDPer1M: 0.6,
	})
	if err != nil {
		t.Fatalf("update embedding model: %v", err)
	}
	if updated.CacheReadPriceUSDPer1M != 0 {
		t.Fatalf("updated embedding cache read price = %v, want 0", updated.CacheReadPriceUSDPer1M)
	}
}

func TestFinishCallPersistsCachedInputTokensInUsageAggregates(t *testing.T) {
	store := NewMemoryStore()
	call := CallContext{
		RequestID: "req_cached_usage",
		Project:   Project{ID: "project_cached_usage"},
		Key:       APIKey{ID: "key_cached_usage", OwnerUserID: "user_cached_usage"},
		Model: Model{
			Name:                   "cached-chat",
			Modality:               "chat",
			InputPriceUSDPer1M:     2,
			CacheReadPriceUSDPer1M: 0.5,
			OutputPriceUSDPer1M:    8,
		},
		StartedAt: time.Now(),
	}
	route := RouteSelection{Provider: Provider{ID: "provider_cached_usage"}}

	store.FinishCall(call, route, Usage{
		PromptTokens:             1000,
		CachedInputTokens:        400,
		CacheWriteInputTokens:    25,
		InputAudioTokens:         10,
		CompletionTokens:         100,
		ReasoningOutputTokens:    30,
		OutputAudioTokens:        5,
		AcceptedPredictionTokens: 7,
		RejectedPredictionTokens: 8,
	}, 200, "", "127.0.0.1", "store-test")

	records := store.ListUsageRecords()
	if len(records) != 1 {
		t.Fatalf("usage records = %d, want 1", len(records))
	}
	if records[0].CachedInputTokens != 400 {
		t.Fatalf("persisted cached input tokens = %d, want 400", records[0].CachedInputTokens)
	}
	if records[0].CacheWriteTokens != 25 || records[0].InputAudioTokens != 10 {
		t.Fatalf("persisted input details = %+v, want cache write 25 and audio 10", records[0])
	}
	if records[0].ReasoningTokens != 30 || records[0].OutputAudioTokens != 5 ||
		records[0].AcceptedPredictionTokens != 7 || records[0].RejectedPredictionTokens != 8 {
		t.Fatalf("persisted output details = %+v", records[0])
	}
	if records[0].AttributedUserID != "user_cached_usage" {
		t.Fatalf("persisted attributed user = %q, want user_cached_usage", records[0].AttributedUserID)
	}
	requestLogs := store.ListRequestLogs()
	if len(requestLogs) != 1 || requestLogs[0].AttributedUserID != "user_cached_usage" {
		t.Fatalf("request log attributed user = %#v, want user_cached_usage", requestLogs)
	}
	if math.Abs(records[0].CostUSD-0.0022) > 1e-12 {
		t.Fatalf("persisted cost = %.12f, want 0.0022", records[0].CostUSD)
	}

	summary := summarizeUsage(store.ListUsageRecords(), store.ListRequestLogs())
	if got := summary["cached_input_tokens"]; got != int64(400) {
		t.Fatalf("summary cached input tokens = %#v, want 400", got)
	}

	breakdown := New(store).usageBreakdownFromRecords(store.ListUsageRecords(), indexProjectsByID(store.ListProjects()))
	models, ok := breakdown["models"].([]map[string]any)
	if !ok || len(models) != 1 {
		t.Fatalf("model breakdown = %#v, want one row", breakdown["models"])
	}
	if got := models[0]["cached_input_tokens"]; got != int64(400) {
		t.Fatalf("breakdown cached input tokens = %#v, want 400", got)
	}
}

// TestFinishCallNegativeUsageDoesNotShrinkQuotaCounters exercises the full
// FinishCall path: a completion carrying negative upstream usage must leave
// the persisted day quota counters untouched instead of subtracting from them.
func TestFinishCallNegativeUsageDoesNotShrinkQuotaCounters(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Negative Usage", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "negative-usage",
		Allowed: []string{"clamped-chat"},
		Status:  StatusActive,
	}, "thk_negative_usage")
	if err != nil {
		t.Fatal(err)
	}
	model := Model{
		Name:                "clamped-chat",
		Modality:            "chat",
		InputPriceUSDPer1M:  2,
		OutputPriceUSDPer1M: 8,
	}
	newCall := func(requestID string) CallContext {
		return CallContext{
			RequestID: requestID,
			Project:   project,
			Key:       key,
			Model:     model,
			StartedAt: time.Now(),
		}
	}
	route := RouteSelection{Provider: Provider{ID: "provider_negative_usage"}}

	store.FinishCall(newCall("req_negative_usage_baseline"), route, Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
	}, 200, "", "127.0.0.1", "store-test")
	store.FinishCall(newCall("req_negative_usage_hostile"), route, Usage{
		PromptTokens:     -900,
		CompletionTokens: -90,
		TotalTokens:      -990,
	}, 200, "", "127.0.0.1", "store-test")

	var bucket QuotaBucket
	if err := store.db.Where("key_id = ? AND scope = ?", key.ID, "day").First(&bucket).Error; err != nil {
		t.Fatalf("read day quota bucket: %v", err)
	}
	if bucket.PromptTokens != 1000 || bucket.CompletionTokens != 100 || bucket.TotalTokens != 1100 {
		t.Fatalf("day counters shrank after negative usage: %+v", bucket.QuotaCounter)
	}
	wantCost := (1000*2.0 + 100*8.0) / 1_000_000
	if math.Abs(bucket.CostUSD-wantCost) > 1e-12 {
		t.Fatalf("day cost = %.12f, want %.12f", bucket.CostUSD, wantCost)
	}
}

// TestFinishCallSettlesUsageInTheAdmissionPeriod pins quota settlement to the
// database clock reading admission took. Deriving the buckets from the local
// completion clock instead counted a request in one period and charged its
// tokens to another whenever the two disagreed across a day boundary.
func TestFinishCallSettlesUsageInTheAdmissionPeriod(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Admission Period", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "admission-period",
		Allowed: []string{"period-chat"},
		Status:  StatusActive,
	}, "thk_admission_period")
	if err != nil {
		t.Fatal(err)
	}
	admittedAt := time.Now().UTC().Add(-48 * time.Hour)
	store.FinishCall(CallContext{
		RequestID: "req_admission_period",
		Project:   project,
		Key:       key,
		Model:     Model{Name: "period-chat", Modality: "chat", InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 8},
		StartedAt: admittedAt,
	}, RouteSelection{Provider: Provider{ID: "provider_admission_period"}}, Usage{
		PromptTokens:     1000,
		CompletionTokens: 100,
	}, 200, "", "127.0.0.1", "store-test")

	var admitted QuotaBucket
	if err := store.db.Where("key_id = ? AND scope = ? AND bucket = ?", key.ID, "day", dayBucket(admittedAt)).First(&admitted).Error; err != nil {
		t.Fatalf("read the admission day bucket %s: %v", dayBucket(admittedAt), err)
	}
	if admitted.TotalTokens != 1100 {
		t.Fatalf("admission day bucket total tokens = %d, want 1100", admitted.TotalTokens)
	}
	var completion QuotaBucket
	err = store.db.Where("key_id = ? AND scope = ? AND bucket = ?", key.ID, "day", dayBucket(time.Now().UTC())).First(&completion).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("usage settled in the completion day bucket %s: %+v (err %v)", dayBucket(time.Now().UTC()), completion.QuotaCounter, err)
	}
	var month QuotaBucket
	if err := store.db.Where("key_id = ? AND scope = ? AND bucket = ?", key.ID, "month", monthBucket(admittedAt)).First(&month).Error; err != nil {
		t.Fatalf("read the admission month bucket %s: %v", monthBucket(admittedAt), err)
	}
	if month.TotalTokens != 1100 {
		t.Fatalf("admission month bucket total tokens = %d, want 1100", month.TotalTokens)
	}
}

func TestDeleteAdminUserProtectsLastActivePlatformAdmin(t *testing.T) {
	store := NewMemoryStore()
	admin := createTestAdminUser(t, store, "only-admin", "admin")
	member := createTestAdminUser(t, store, "member", "user")

	if err := store.DeleteAdminUser(admin.ID); AsHTTPError(err).Code != "last_admin_user" {
		t.Fatalf("expected last admin deletion to be rejected, got %v", err)
	}
	if err := store.DeleteAdminUser(member.ID); err != nil {
		t.Fatalf("expected ordinary user deletion to remain allowed, got %v", err)
	}
}

func TestUpdateAdminUserProtectsLastActivePlatformAdmin(t *testing.T) {
	tests := []struct {
		name  string
		patch AdminUser
	}{
		{name: "disable", patch: AdminUser{Status: StatusDisabled}},
		{name: "demote", patch: AdminUser{Role: "user"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			admin := createTestAdminUser(t, store, "only-admin-"+tt.name, "system_admin")
			createTestAdminUser(t, store, "member-"+tt.name, "user")

			if _, err := store.UpdateAdminUser(admin.ID, tt.patch, ""); AsHTTPError(err).Code != "last_admin_user" {
				t.Fatalf("expected last admin update to be rejected, got %v", err)
			}
		})
	}
}

func TestAdminUserChangesAllowedWhenAnotherAdminRemains(t *testing.T) {
	store := NewMemoryStore()
	first := createTestAdminUser(t, store, "first-admin", "admin")
	createTestAdminUser(t, store, "second-admin", "system_admin")

	updated, err := store.UpdateAdminUser(first.ID, AdminUser{Role: "user"}, "")
	if err != nil {
		t.Fatalf("expected demotion with another active admin to succeed, got %v", err)
	}
	if updated.Role != "user" {
		t.Fatalf("expected demoted user role, got %q", updated.Role)
	}
}

func TestLegacyProjectTeamMigrationPreservesAccess(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE projects (
		id text primary key,
		name text,
		team_id text,
		owner_user_id text,
		cost_center text,
		status text,
		created_at datetime,
		updated_at datetime,
		default_quota_ref text
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, team_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"prj_legacy_team", "Legacy Team Project", "team_legacy", StatusActive, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	project, ok := store.GetProject("prj_legacy_team")
	if !ok || len(project.Teams) != 1 {
		t.Fatalf("legacy project team was not migrated: %+v", project)
	}
	if link := project.Teams[0]; link.TeamID != "team_legacy" || link.Role != "team_leader" || !link.IsPrimary {
		t.Fatalf("unexpected migrated project team: %+v", link)
	}
	legacyTeams := store.ListResources("teams")
	if len(legacyTeams) != 1 || legacyTeams[0].ID != "team_legacy" || legacyTeams[0].Status != StatusActive {
		t.Fatalf("legacy project team resource was not migrated as active: %+v", legacyTeams)
	}
	server := New(store)
	if server.canAccessProject(AdminUser{ID: "usr_member", Role: "user", TeamID: "team_legacy"}, project) {
		t.Fatal("legacy migration must not grant new access to ordinary same-team users")
	}
	if !server.canManageProject(AdminUser{ID: "usr_leader", Role: "team_leader", TeamID: "team_legacy"}, project) {
		t.Fatal("legacy team leader should retain project management access")
	}
}

func TestLegacyUserTeamMigrationPreservesLogin(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy-user.db")
	store, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash := HashSecret("legacy-password")
	if err := store.db.Exec(`INSERT INTO admin_users
		(id, username, name, email, role, team_id, status, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"usr_legacy_login", "legacy-login", "Legacy Login", "legacy-login@example.com", "admin",
		"team_legacy_login", StatusActive, passwordHash, time.Now().UTC(), time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore("sqlite://" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := reopened.AuthenticateAdminUser("legacy-login", "legacy-password", time.Hour)
	if err != nil {
		t.Fatalf("legacy user should still authenticate after team migration: %v", err)
	}
	if len(user.TeamIDs) != 1 || user.TeamIDs[0] != "team_legacy_login" {
		t.Fatalf("legacy user teams were not normalized as JSON: %+v", user.TeamIDs)
	}
}

func TestConcurrentProjectTeamLinkIsUnique(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_primary", Name: "Primary Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_shared", Name: "Shared Team", Status: StatusActive})
	project := store.CreateProject(Project{Name: "Concurrent Team Project", TeamID: "team_primary", Status: StatusActive})

	const attempts = 12
	results := make(chan error, attempts)
	var group sync.WaitGroup
	for index := 0; index < attempts; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_shared", Role: "viewer"})
			results <- err
		}()
	}
	group.Wait()
	close(results)

	succeeded := 0
	conflicted := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if AsHTTPError(err).Code == "project_team_conflict" {
			conflicted++
			continue
		}
		t.Fatalf("unexpected concurrent link error: %v", err)
	}
	if succeeded != 1 || conflicted != attempts-1 {
		t.Fatalf("concurrent uniqueness result succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	links, total, err := store.ListProjectTeams(project.ID, 0, 100)
	if err != nil || total != 2 || len(links) != 2 {
		t.Fatalf("expected primary plus one unique shared link, total=%d links=%+v err=%v", total, links, err)
	}
}

func TestConcurrentProjectTeamRemovalAcrossStoresPreservesLastLink(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "shared-removal.db")
	storeA, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, teamID := range []string{"team_one", "team_two"} {
		storeA.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
	}
	project := storeA.CreateProject(Project{Name: "Cross Store Removal", Status: StatusActive})
	for _, teamID := range []string{"team_one", "team_two"} {
		if _, err := storeA.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: teamID, Role: "viewer"}); err != nil {
			t.Fatal(err)
		}
	}

	results := make(chan error, 2)
	start := make(chan struct{})
	for index, store := range []*GormStore{storeA, storeB} {
		teamID := []string{"team_one", "team_two"}[index]
		go func(store *GormStore, teamID string) {
			<-start
			results <- store.RemoveProjectTeam(project.ID, teamID)
		}(store, teamID)
	}
	close(start)
	firstErr := <-results
	secondErr := <-results

	succeeded := 0
	blocked := 0
	for _, err := range []error{firstErr, secondErr} {
		if err == nil {
			succeeded++
		} else if AsHTTPError(err).Code == "project_last_team" {
			blocked++
		} else {
			t.Fatalf("unexpected concurrent removal error: %v", err)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatalf("expected one removal and one last-link block, succeeded=%d blocked=%d", succeeded, blocked)
	}
	links, total, err := storeA.ListProjectTeams(project.ID, 0, 10)
	if err != nil || total != 1 || len(links) != 1 {
		t.Fatalf("expected one surviving link, total=%d links=%+v err=%v", total, links, err)
	}
}

func TestConcurrentTeamDeletionAndLinkAcrossStoresCannotCreateOrphan(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "shared-team-delete.db")
	storeA, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeA.CreateResource("teams", AdminResource{ID: "team_race", Name: "Race Team", Status: StatusActive})
	project := storeA.CreateProject(Project{Name: "Team Delete Race", Status: StatusActive})

	start := make(chan struct{})
	linkResult := make(chan error, 1)
	deleteResult := make(chan error, 1)
	go func() {
		<-start
		_, err := storeA.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_race", Role: "viewer"})
		linkResult <- err
	}()
	go func() {
		<-start
		deleteResult <- storeB.DeleteTeam("team_race")
	}()
	close(start)
	linkErr := <-linkResult
	deleteErr := <-deleteResult

	links, total, err := storeA.ListProjectTeams(project.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	teamExists := false
	for _, team := range storeA.ListResources("teams") {
		teamExists = teamExists || team.ID == "team_race"
	}
	switch {
	case linkErr == nil:
		if AsHTTPError(deleteErr).Code != "team_has_projects" || !teamExists || total != 1 || len(links) != 1 {
			t.Fatalf("successful link must block deletion: link=%v delete=%v team=%v links=%+v", linkErr, deleteErr, teamExists, links)
		}
	case deleteErr == nil:
		if AsHTTPError(linkErr).Code != "team_not_found" || teamExists || total != 0 || len(links) != 0 {
			t.Fatalf("successful deletion must block linking: link=%v delete=%v team=%v links=%+v", linkErr, deleteErr, teamExists, links)
		}
	default:
		t.Fatalf("expected either linking or deletion to succeed: link=%v delete=%v", linkErr, deleteErr)
	}
}

func TestConcurrentTeamDeletionAndPrimaryAssignmentAcrossStoresCannotCreateOrphan(t *testing.T) {
	for _, mode := range []string{"create", "update"} {
		t.Run(mode, func(t *testing.T) {
			databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "shared-primary-assignment.db")
			storeA, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			storeB, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			storeA.CreateResource("teams", AdminResource{ID: "team_race", Name: "Race Team", Status: StatusActive})
			projectID := "prj_primary_race"
			if mode == "update" {
				storeA.CreateProject(Project{ID: projectID, Name: "Primary Update Race", Status: StatusActive})
			}

			start := make(chan struct{})
			assignmentResult := make(chan error, 1)
			deleteResult := make(chan error, 1)
			go func() {
				<-start
				if mode == "create" {
					_, err := storeA.CreateProjectChecked(Project{ID: projectID, Name: "Primary Create Race", TeamID: "team_race", Status: StatusActive})
					assignmentResult <- err
					return
				}
				_, err := storeA.UpdateProject(projectID, Project{Name: "Primary Update Race", TeamID: "team_race", Status: StatusActive})
				assignmentResult <- err
			}()
			go func() {
				<-start
				deleteResult <- storeB.DeleteTeam("team_race")
			}()
			close(start)
			assignmentErr := <-assignmentResult
			deleteErr := <-deleteResult

			project, projectExists := storeA.GetProject(projectID)
			teamExists := false
			for _, team := range storeA.ListResources("teams") {
				teamExists = teamExists || team.ID == "team_race"
			}
			switch {
			case assignmentErr == nil:
				if AsHTTPError(deleteErr).Code != "team_has_projects" || !teamExists || !projectExists || project.TeamID != "team_race" {
					t.Fatalf("successful primary assignment must block deletion: assign=%v delete=%v team=%v project=%+v", assignmentErr, deleteErr, teamExists, project)
				}
				if link, ok := projectTeamByID(project.Teams, "team_race"); !ok || !link.IsPrimary {
					t.Fatalf("successful primary assignment must create a primary link: %+v", project.Teams)
				}
			case deleteErr == nil:
				if AsHTTPError(assignmentErr).Code != "team_not_found" || teamExists {
					t.Fatalf("successful deletion must block primary assignment: assign=%v delete=%v team=%v", assignmentErr, deleteErr, teamExists)
				}
				if mode == "create" && projectExists {
					t.Fatalf("failed checked create must not leave a project: %+v", project)
				}
				if mode == "update" && (!projectExists || project.TeamID != "" || len(project.Teams) != 0) {
					t.Fatalf("failed primary update must leave the project teamless: %+v", project)
				}
			default:
				t.Fatalf("expected either primary assignment or deletion to succeed: assign=%v delete=%v", assignmentErr, deleteErr)
			}
		})
	}
}

func TestConcurrentTeamDeletionAndUserAssignmentAcrossStoresCannotCreateOrphan(t *testing.T) {
	for _, mode := range []string{"create", "update"} {
		t.Run(mode, func(t *testing.T) {
			databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "shared-user-assignment.db")
			storeA, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			storeB, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			for _, teamID := range []string{"team_home", "team_race"} {
				storeA.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
			}
			userID := "usr_team_race"
			if mode == "update" {
				if _, err := storeA.CreateAdminUser(AdminUser{
					ID: userID, Username: "team-race-user", Name: "Team Race User", Email: "team-race-user@tokenhub.local",
					Role: "user", TeamID: "team_home", Status: StatusActive,
				}, "user123456"); err != nil {
					t.Fatal(err)
				}
			}

			start := make(chan struct{})
			assignmentResult := make(chan error, 1)
			deleteResult := make(chan error, 1)
			go func() {
				<-start
				assignment := AdminUser{TeamID: "team_home", TeamIDs: []string{"team_home", "team_race"}}
				if mode == "create" {
					assignment.ID = userID
					assignment.Username = "team-race-user"
					assignment.Name = "Team Race User"
					assignment.Email = "team-race-user@tokenhub.local"
					assignment.Role = "user"
					assignment.Status = StatusActive
					_, err := storeA.CreateAdminUser(assignment, "user123456")
					assignmentResult <- err
					return
				}
				_, err := storeA.UpdateAdminUser(userID, assignment, "")
				assignmentResult <- err
			}()
			go func() {
				<-start
				deleteResult <- storeB.DeleteTeam("team_race")
			}()
			close(start)
			assignmentErr := <-assignmentResult
			deleteErr := <-deleteResult

			var user AdminUser
			userExists := false
			for _, item := range storeA.ListAdminUsers() {
				if item.ID == userID {
					user = item
					userExists = true
					break
				}
			}
			teamExists := false
			for _, team := range storeA.ListResources("teams") {
				teamExists = teamExists || team.ID == "team_race"
			}
			switch {
			case assignmentErr == nil:
				if AsHTTPError(deleteErr).Code != "team_has_users" || !teamExists || !userExists || !userHasTeam(user, "team_race") {
					t.Fatalf("successful user assignment must block deletion: assign=%v delete=%v team=%v user=%+v", assignmentErr, deleteErr, teamExists, user)
				}
			case deleteErr == nil:
				if AsHTTPError(assignmentErr).Code != "team_not_found" || teamExists {
					t.Fatalf("successful deletion must block user assignment: assign=%v delete=%v team=%v", assignmentErr, deleteErr, teamExists)
				}
				if mode == "create" && userExists {
					t.Fatalf("failed user creation must not leave a user: %+v", user)
				}
				if mode == "update" && (!userExists || userHasTeam(user, "team_race") || !userHasTeam(user, "team_home")) {
					t.Fatalf("failed user update must preserve existing teams: %+v", user)
				}
			default:
				t.Fatalf("expected either user assignment or deletion to succeed: assign=%v delete=%v", assignmentErr, deleteErr)
			}
		})
	}
}

func createTestAdminUser(t *testing.T, store *GormStore, username string, role string) AdminUser {
	t.Helper()
	user, err := store.CreateAdminUser(AdminUser{
		Username: username,
		Email:    username + "@example.com",
		Role:     role,
		Status:   StatusActive,
	}, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	return user
}
