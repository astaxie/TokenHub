package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func setupDefaultUserQuotaTest(t *testing.T, limits map[string]any) (*GormStore, Project, APIKey, APIKey) {
	t.Helper()
	store, project, keyA, keyB := setupUserQuotaTest(t, limits)
	t.Cleanup(func() { _ = store.Close() })
	policy, err := New(store).findResource("quota-policies", "quota_user_quota")
	if err != nil {
		t.Fatal(err)
	}
	policy.Fields["scope_id"] = allUsersQuotaScopeID
	if _, err := store.UpdateResource("quota-policies", policy.ID, policy); err != nil {
		t.Fatal(err)
	}
	return store, project, keyA, keyB
}

func TestDefaultUserQuotaAggregatesKeysAndIsolatesNewUsers(t *testing.T) {
	store, project, keyA, keyB := setupDefaultUserQuotaTest(t, map[string]any{"monthly_tokens": 10})
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !call.UserQuotaEnabled || call.UserQuotaID != userQuotaBucketKey(keyA.OwnerUserID) {
		t.Fatalf("template did not resolve to the attributed user: %+v", call)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "default-user-quota-test")
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 1); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("second key should share the exhausted user allowance: %v", err)
	}
	otherProject := store.CreateProject(Project{Name: "Another project", OwnerUserID: keyA.OwnerUserID, Status: StatusActive})
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "other-project-key", OwnerUserID: keyA.OwnerUserID, Status: StatusActive}, "thk_default_quota_other_project")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), otherProject, otherKey, "user-quota-model", 1); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("another project must not give the same user a fresh allowance: %v", err)
	}

	newUser, err := store.CreateAdminUser(AdminUser{Username: "new-quota-user", Email: "new-quota-user@example.test", Role: "user", Status: StatusActive}, "NewUserQuotaPass123!")
	if err != nil {
		t.Fatal(err)
	}
	newKey, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "new-user-key", OwnerUserID: newUser.ID, Status: StatusActive}, "thk_default_quota_new_user")
	if err != nil {
		t.Fatal(err)
	}
	newCall, err := store.StartCall(context.Background(), project, newKey, "user-quota-model", 10)
	if err != nil {
		t.Fatalf("new user should receive an independent allowance: %v", err)
	}
	if newCall.UserQuotaID != userQuotaBucketKey(newUser.ID) {
		t.Fatalf("new user received another user's quota bucket: %q", newCall.UserQuotaID)
	}
	store.FinishCall(newCall, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "default-user-quota-test")
	if _, err := store.StartCall(context.Background(), project, newKey, "user-quota-model", 1); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("new user should automatically receive the template cap: %v", err)
	}
	var templateBuckets int64
	if err := store.db.Model(&QuotaBucket{}).Where("key_id = ?", userQuotaBucketKey(allUsersQuotaScopeID)).Count(&templateBuckets).Error; err != nil {
		t.Fatal(err)
	}
	if templateBuckets != 0 {
		t.Fatalf("template created %d shared buckets", templateBuckets)
	}
}

func TestDefaultUserQuotaIncludesHistoricalUsage(t *testing.T) {
	store, project, keyA, keyB := setupUserQuotaTest(t, map[string]any{})
	t.Cleanup(func() { _ = store.Close() })
	call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 10}, http.StatusOK, "", "127.0.0.1", "default-user-quota-test")
	if _, err := store.UpdateResource("quota-policies", "quota_user_quota", AdminResource{
		Fields: map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "monthly_tokens": 10},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 1); err == nil || AsHTTPError(err).Code != "quota_exceeded" {
		t.Fatalf("template should include usage settled before activation: %v", err)
	}
	usage, supported, err := store.GetQuotaPolicyUsage("user", keyA.OwnerUserID)
	if err != nil || !supported || usage.Monthly.TotalTokens != 10 {
		t.Fatalf("attributed usage should remain available: usage=%+v supported=%v err=%v", usage, supported, err)
	}
	_, supported, err = store.GetQuotaPolicyUsage("user", allUsersQuotaScopeID)
	if err != nil || supported {
		t.Fatalf("a template has no single usage bucket: supported=%v err=%v", supported, err)
	}
}

func TestDefaultUserQuotaMergesStrictestLimitsWithoutBecomingKeyLimits(t *testing.T) {
	store, project, key, _ := setupDefaultUserQuotaTest(t, map[string]any{
		"rate_limit_rpm": 10, "token_limit_tpm": 100, "daily_requests": 10, "monthly_requests": 100,
		"daily_tokens": 100, "monthly_tokens": 1000, "daily_cost_usd": 10, "monthly_cost_usd": 100, "max_concurrency": 10,
	})
	store.CreateResource("quota-policies", AdminResource{Name: "Individual limits", Status: StatusActive, Fields: map[string]any{
		"scope": "user", "scope_id": key.OwnerUserID,
		"rate_limit_rpm": 5, "token_limit_tpm": 200, "daily_requests": 5, "monthly_requests": 200,
		"daily_tokens": 50, "monthly_tokens": 2000, "daily_cost_usd": 5, "monthly_cost_usd": 200, "max_concurrency": 0,
	}})
	limits, _, userPolicy, err := quotaPolicyLimits(store.db, project, key)
	if err != nil {
		t.Fatal(err)
	}
	want := QuotaLimits{RateLimitRPM: 5, TokenLimitTPM: 100, DailyRequests: 5, MonthlyRequests: 100,
		DailyTokens: 50, MonthlyTokens: 1000, DailyCostUSD: 5, MonthlyCostUSD: 100, MaxConcurrency: 10}
	if limits != (QuotaLimits{}) || userPolicy.UserID != key.OwnerUserID || userPolicy.Limits != want {
		t.Fatalf("unexpected merged limits: key=%+v user=%+v want=%+v", limits, userPolicy, want)
	}
}

func TestDefaultUserQuotaDisableAndDeletePreserveUsage(t *testing.T) {
	store, project, keyA, keyB := setupDefaultUserQuotaTest(t, map[string]any{"daily_requests": 1})
	for _, status := range []string{StatusDisabled, StatusActive} {
		if _, err := store.UpdateResource("quota-policies", "quota_user_quota", AdminResource{Status: status}); err != nil {
			t.Fatal(err)
		}
		call, err := store.StartCall(context.Background(), project, keyA, "user-quota-model", 0)
		if status == StatusActive {
			if err == nil || AsHTTPError(err).Code != "quota_exceeded" {
				t.Fatalf("reenabling the template should retain historical usage: %v", err)
			}
			continue
		}
		if err != nil || call.UserQuotaEnabled {
			t.Fatalf("disabled template should not enforce user limits: enabled=%v err=%v", call.UserQuotaEnabled, err)
		}
		store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "default-user-quota-test")
	}
	if err := store.DeleteResource("quota-policies", "quota_user_quota"); err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, keyB, "user-quota-model", 0)
	if err != nil || call.UserQuotaEnabled {
		t.Fatalf("deleted template should stop enforcing user limits: enabled=%v err=%v", call.UserQuotaEnabled, err)
	}
	store.FinishCall(call, RouteSelection{}, Usage{}, http.StatusOK, "", "127.0.0.1", "default-user-quota-test")
}

func TestDefaultUserQuotaUsesExistingAttributionAndSkipsUnattributedCalls(t *testing.T) {
	for _, test := range []struct {
		name    string
		project Project
		key     APIKey
		want    string
	}{
		{name: "owner", project: Project{OwnerUserID: "usr_project"}, key: APIKey{OwnerUserID: "usr_owner", Metadata: map[string]string{"created_by": "usr_creator"}}, want: "usr_owner"},
		{name: "creator", project: Project{OwnerUserID: "usr_project"}, key: APIKey{Metadata: map[string]string{"created_by": "usr_creator"}}, want: "usr_creator"},
		{name: "project", project: Project{OwnerUserID: "usr_project"}, want: "usr_project"},
		{name: "unattributed"},
		{name: "unattributed sentinel", key: APIKey{OwnerUserID: unattributedQuotaUserID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			store.CreateResource("quota-policies", AdminResource{Name: "Default quota", Status: StatusActive,
				Fields: map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "daily_requests": 1}})
			_, _, policy, err := quotaPolicyLimits(store.db, test.project, test.key)
			if err != nil || policy.UserID != test.want || policy.Enabled() != (test.want != "") {
				t.Fatalf("unexpected template attribution: policy=%+v want=%q err=%v", policy, test.want, err)
			}
		})
	}
	if quotaPolicyApplies("user", "", Project{OwnerUserID: "usr_owner"}, APIKey{}) {
		t.Fatal("empty user target must not become a default template")
	}
}

func TestDefaultUserQuotaSkipsUnattributedAdmissions(t *testing.T) {
	store, _, _, _ := setupDefaultUserQuotaTest(t, map[string]any{"daily_tokens": 1})
	project := store.CreateProject(Project{Name: "Unattributed project", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "unattributed-key", Status: StatusActive}, "thk_default_quota_unattributed")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 2)
		if err != nil {
			t.Fatalf("unattributed call must not use the template: %v", err)
		}
		if call.UserQuotaEnabled {
			t.Fatalf("unattributed call has an aggregate user quota: %+v", call)
		}
		store.FinishCall(call, RouteSelection{}, Usage{TotalTokens: 2}, http.StatusOK, "", "127.0.0.1", "default-user-quota-test")
	}
}

func TestAdminAPIManagesDefaultUserQuotaPolicies(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name": "Default user cap", "status": StatusActive,
		"fields": map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "monthly_cost_usd": 10},
	}, "dev_admin_token")
	if created.Code != http.StatusCreated {
		t.Fatalf("create default policy: %d %s", created.Code, created.Body)
	}
	var policy AdminResource
	if err := json.Unmarshal([]byte(created.Body), &policy); err != nil {
		t.Fatal(err)
	}
	updated := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+policy.ID, map[string]any{
		"fields": map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "monthly_cost_usd": 20},
	}, "dev_admin_token")
	if updated.Code != http.StatusOK {
		t.Fatalf("update default policy: %d %s", updated.Code, updated.Body)
	}
	if err := json.Unmarshal([]byte(updated.Body), &policy); err != nil {
		t.Fatal(err)
	}
	if !isDefaultUserQuotaPolicy(policy.Fields) || float64Field(policy.Fields, "monthly_cost_usd") != 20 {
		t.Fatalf("update lost the template target: %+v", policy)
	}
	renamed := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+policy.ID, map[string]any{
		"name": "Renamed default user cap",
	}, "dev_admin_token")
	if renamed.Code != http.StatusOK || json.Unmarshal([]byte(renamed.Body), &policy) != nil || !isDefaultUserQuotaPolicy(policy.Fields) || float64Field(policy.Fields, "monthly_cost_usd") != 20 {
		t.Fatalf("partial name update lost template fields: %d %s", renamed.Code, renamed.Body)
	}
	listed := doJSON(t, app, http.MethodGet, "/api/admin/resources/quota-policies", nil, "dev_admin_token")
	var collection struct {
		Data []AdminResource `json:"data"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal([]byte(listed.Body), &collection) != nil || len(collection.Data) != 1 || collection.Data[0].CurrentUsage != nil {
		t.Fatalf("template list should omit misleading shared usage: %d %s", listed.Code, listed.Body)
	}
	for _, scopeID := range []string{"", "   ", "usr_missing"} {
		invalid := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
			"name": "Invalid target", "fields": map[string]any{"scope": "user", "scope_id": scopeID},
		}, "dev_admin_token")
		want := http.StatusBadRequest
		if scopeID == "usr_missing" {
			want = http.StatusNotFound
		}
		if invalid.Code != want {
			t.Fatalf("invalid target %q: %d %s", scopeID, invalid.Code, invalid.Body)
		}
	}
	disabled := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+policy.ID, map[string]any{"status": StatusDisabled}, "dev_admin_token")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable template: %d %s", disabled.Code, disabled.Body)
	}
	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/resources/quota-policies/"+policy.ID, nil, "dev_admin_token")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete template: %d %s", deleted.Code, deleted.Body)
	}
}

func TestDefaultUserQuotaPolicyRequiresPlatformAdministrator(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	team := store.CreateResource("teams", AdminResource{Name: "Quota team", Status: StatusActive})
	template := store.CreateResource("quota-policies", AdminResource{Name: "Protected default quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "daily_tokens": 10}})
	app := New(store).Handler()
	for _, role := range []string{"team_leader", "user", "security_admin"} {
		t.Run(role, func(t *testing.T) {
			user, err := store.CreateAdminUser(AdminUser{Username: "quota-" + role, Email: "quota-" + role + "@example.test", Role: role, TeamID: team.ID, Status: StatusActive}, "QuotaRolePass123!")
			if err != nil {
				t.Fatal(err)
			}
			_, session, err := store.CreateAdminSession(user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			own := store.CreateResource("quota-policies", AdminResource{Name: "Individual quota", Status: StatusActive,
				Fields: map[string]any{"scope": "user", "scope_id": user.ID, "daily_tokens": 20}})
			// A manageable project's default reference must not grant template control.
			store.CreateProject(Project{Name: "Quota project", OwnerUserID: user.ID, TeamID: team.ID, DefaultQuotaRef: template.ID, Status: StatusActive})
			for _, attempt := range []struct {
				name, method, id string
				body             any
			}{
				{name: "create", method: http.MethodPost, body: map[string]any{"name": "Forbidden default", "fields": template.Fields}},
				{name: "change individual target", method: http.MethodPatch, id: own.ID, body: map[string]any{"fields": map[string]any{"scope_id": allUsersQuotaScopeID}}},
				{name: "partial limit update", method: http.MethodPatch, id: template.ID, body: map[string]any{"fields": map[string]any{"daily_tokens": 30}}},
				{name: "change template target", method: http.MethodPatch, id: template.ID, body: map[string]any{"fields": map[string]any{"scope_id": user.ID}}},
				{name: "disable", method: http.MethodPatch, id: template.ID, body: map[string]any{"status": StatusDisabled}},
				{name: "delete", method: http.MethodDelete, id: template.ID},
			} {
				path := "/api/admin/resources/quota-policies"
				if attempt.id != "" {
					path += "/" + attempt.id
				}
				response := doJSON(t, app, attempt.method, path, attempt.body, session.Token)
				if response.Code != http.StatusForbidden {
					t.Fatalf("%s: %d %s", attempt.name, response.Code, response.Body)
				}
			}
			if role == "team_leader" {
				listed := doJSON(t, app, http.MethodGet, "/api/admin/resources/quota-policies", nil, session.Token)
				if listed.Code != http.StatusOK || strings.Contains(listed.Body, template.ID) {
					t.Fatalf("leader list exposed global template: %d %s", listed.Code, listed.Body)
				}
				updated := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+own.ID, map[string]any{"fields": map[string]any{"daily_tokens": 15}}, session.Token)
				if updated.Code != http.StatusOK {
					t.Fatalf("leader lost individual quota access: %d %s", updated.Code, updated.Body)
				}
			}
		})
	}
	unchanged, err := New(store).findResource("quota-policies", template.ID)
	if err != nil || unchanged.Status != StatusActive || snapshotJSON(unchanged.Fields) != snapshotJSON(template.Fields) {
		t.Fatalf("forbidden mutations changed template: %+v err=%v", unchanged, err)
	}
}
