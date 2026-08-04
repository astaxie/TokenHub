package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestAdminCreatesAPIKeyUnderDefaultProject(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+defaultProjectID+"/keys", map[string]any{
		"name":           "Default Project Key",
		"group":          "default",
		"allowed_models": []string{"gpt-4.1-mini"},
		"limits": map[string]any{
			"daily_requests":   1000,
			"monthly_requests": 30000,
			"daily_tokens":     1000000,
			"monthly_tokens":   20000000,
			"daily_cost_usd":   100,
			"monthly_cost_usd": 2000,
			"max_concurrency":  20,
		},
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"project_id":"`+defaultProjectID+`"`) || !strings.Contains(resp.Body, `"api_key"`) {
		t.Fatalf("expected issued key under default project: %s", resp.Body)
	}

	keys := store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 {
		t.Fatalf("expected one default project key, got %d", len(keys))
	}
	if keys[0].ProjectID != defaultProjectID {
		t.Fatalf("expected key project %s, got %s", defaultProjectID, keys[0].ProjectID)
	}
}

func TestAdminCanClearAllAPIKeyLimits(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.CreateAPIKey(defaultProjectID, APIKey{
		ID:   "key_clear_limits",
		Name: "Clear Limits",
		Limits: QuotaLimits{
			DailyRequests:   1000,
			MonthlyRequests: 30000,
			DailyTokens:     1000000,
			MonthlyTokens:   20000000,
			DailyCostUSD:    100,
			MonthlyCostUSD:  2000,
			MaxConcurrency:  20,
		},
		Status: StatusActive,
	}, "thk_clear_limits")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/key_clear_limits", map[string]any{
		"name": "Renamed Key",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	var updated APIKey
	if err := json.Unmarshal([]byte(resp.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Limits.DailyRequests != 1000 {
		t.Fatalf("expected omitted limits to be preserved, got %+v", updated.Limits)
	}

	resp = doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/key_clear_limits", map[string]any{
		"limits": QuotaLimits{},
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if err := json.Unmarshal([]byte(resp.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Limits != (QuotaLimits{}) {
		t.Fatalf("expected all limits to be cleared, got %+v", updated.Limits)
	}
}

func TestAdminCanClearAllAPIKeyLimitsAfterApproval(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	key, _, err := store.CreateAPIKey(defaultProjectID, APIKey{
		Name:   "Clear Limits After Approval",
		Limits: QuotaLimits{DailyRequests: 1000},
		Status: StatusActive,
	}, "thk_clear_limits_after_approval")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("approval-flows", AdminResource{
		Name:   "Quota Increase",
		Status: StatusActive,
		Fields: map[string]any{"trigger": "quota_increase", "approver_role": "admin"},
	})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+key.ID, map[string]any{
		"limits": QuotaLimits{},
	}, "")
	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected approval response, got %d: %s", resp.Code, resp.Body)
	}
	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvals[0].Trigger != "quota_increase" {
		t.Fatalf("expected quota increase approval, got %+v", approvals)
	}
	keys := store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 || keys[0].Limits.DailyRequests != 1000 {
		t.Fatalf("expected limits to remain unchanged before approval, got %+v", keys)
	}

	resp = doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected approval to apply, got %d: %s", resp.Code, resp.Body)
	}
	keys = store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 || keys[0].Limits != (QuotaLimits{}) {
		t.Fatalf("expected approved limits to be cleared, got %+v", keys)
	}
}

func TestUserCreatesPersonalAPIKeyWithoutProjectMembership(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{
		Username: "personal-key-user",
		Name:     "Personal Key User",
		Email:    "personal-key-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Username,
		"password": "user123456",
	}, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &session); err != nil {
		t.Fatal(err)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/api-keys", map[string]any{
		"name": "Personal Key",
	}, session.Token)
	if created.Code != http.StatusCreated {
		t.Fatalf("ordinary user should create a personal key without project membership, got %d: %s", created.Code, created.Body)
	}
	if !strings.Contains(created.Body, `"project_id":"`+defaultProjectID+`"`) || !strings.Contains(created.Body, `"api_key"`) {
		t.Fatalf("personal key should fall back to the default project: %s", created.Body)
	}
	keys := store.ListProjectKeys(defaultProjectID)
	if len(keys) != 1 || keys[0].Metadata["created_by"] != user.ID {
		t.Fatalf("personal key should remain attributable to its creator: %+v", keys)
	}

	assignedProject := store.CreateProject(Project{Name: "Assigned Project", Status: StatusActive})
	store.CreateResource("project-members", AdminResource{
		Name:   "Personal Key User Membership",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": assignedProject.ID,
			"user_id":    user.ID,
			"role":       "developer",
		},
	})
	assigned := doJSON(t, app, http.MethodPost, "/api/admin/api-keys", map[string]any{
		"name": "Assigned Project Key",
	}, session.Token)
	if assigned.Code != http.StatusCreated || !strings.Contains(assigned.Body, `"project_id":"`+assignedProject.ID+`"`) {
		t.Fatalf("personal key should prefer an assigned project, got %d: %s", assigned.Code, assigned.Body)
	}
}

func TestUserCanReadRoutedAdminModels(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_platform", Name: "Platform Team", Status: StatusActive})
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "model.viewer",
		Email:    "model.viewer@tokenhub.local",
		Role:     "user",
		TeamID:   "team_platform",
		Status:   StatusActive,
	}, "viewer123456"); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "text-embedding-3-small", Modality: "embedding", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: "provider_mock", ProviderModel: "mock-chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "text-embedding-3-small", ProviderID: "provider_mock", ProviderModel: "mock-embedding", Status: StatusDisabled})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "model.viewer@tokenhub.local",
		"password": "viewer123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", login.Code, login.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}

	models := doJSON(t, app, http.MethodGet, "/api/admin/models", nil, payload.Token)
	if models.Code != http.StatusOK {
		t.Fatalf("expected user to read accessible models, got %d: %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, `"name":"gpt-4.1-mini"`) || strings.Contains(models.Body, `"name":"text-embedding-3-small"`) {
		t.Fatalf("expected only active routed models: %s", models.Body)
	}
	overview := doJSON(t, app, http.MethodGet, "/api/admin/overview", nil, payload.Token)
	if overview.Code != http.StatusOK || !strings.Contains(overview.Body, `"name":"gpt-4.1-mini"`) {
		t.Fatalf("expected overview to include accessible models, got %d: %s", overview.Code, overview.Body)
	}
	create := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":   "viewer-created-model",
		"status": StatusActive,
	}, payload.Token)
	if create.Code != http.StatusForbidden {
		t.Fatalf("expected user model create to be forbidden, got %d: %s", create.Code, create.Body)
	}
}

func TestAdminCannotDeleteOwnAccount(t *testing.T) {
	store := NewMemoryStore()
	actor, err := store.CreateAdminUser(AdminUser{
		Username: "platform.admin",
		Email:    "platform.admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456")
	if err != nil {
		t.Fatal(err)
	}
	victim, err := store.CreateAdminUser(AdminUser{
		Username: "ordinary.user",
		Email:    "ordinary.user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "platform.admin@tokenhub.local",
		"password": "admin123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", login.Code, login.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}

	self := doJSON(t, app, http.MethodDelete, "/api/admin/users/"+actor.ID, nil, payload.Token)
	if self.Code != http.StatusBadRequest {
		t.Fatalf("expected self deletion to be rejected with 400, got %d: %s", self.Code, self.Body)
	}
	if !strings.Contains(self.Body, "cannot_delete_self") {
		t.Fatalf("expected cannot_delete_self error, got %s", self.Body)
	}
	stillExists := false
	for _, user := range store.ListAdminUsers() {
		if user.ID == actor.ID {
			stillExists = true
			break
		}
	}
	if !stillExists {
		t.Fatalf("expected actor account to survive self deletion attempt")
	}

	other := doJSON(t, app, http.MethodDelete, "/api/admin/users/"+victim.ID, nil, payload.Token)
	if other.Code != http.StatusNoContent {
		t.Fatalf("expected deleting another user to succeed, got %d: %s", other.Code, other.Body)
	}
}

func TestBootstrapBaseDataSeedsGovernanceResources(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}

	policies := store.ListResources("security-policies")
	var found AdminResource
	for _, policy := range policies {
		if policy.ID == "sec_ip_allowlist" {
			found = policy
			break
		}
	}
	if found.ID == "" {
		t.Fatalf("expected seeded security policy, got %+v", policies)
	}
	if found.Name != "Production IP Allowlist Policy" || found.Status != StatusActive {
		t.Fatalf("unexpected security policy metadata: %+v", found)
	}
	if stringField(found.Fields, "error_passthrough") != "sanitized" || !strings.Contains(stringField(found.Fields, "ip_allowlist"), "127.0.0.1/32") {
		t.Fatalf("unexpected security policy fields: %+v", found.Fields)
	}

	settings := store.ListResources("settings")
	if len(settings) != 1 || settings[0].ID != "cfg_gateway" {
		t.Fatalf("expected gateway system setting, got %+v", settings)
	}
	if stringField(settings[0].Fields, "public_base_url") == "" || stringField(settings[0].Fields, "audit_retention") == "" {
		t.Fatalf("expected configurable system setting fields, got %+v", settings[0].Fields)
	}

	roles := store.ListResources("role-configs")
	if len(roles) != 3 {
		t.Fatalf("expected three role configs, got %+v", roles)
	}
	roleKeys := map[string]bool{}
	for _, role := range roles {
		roleKeys[stringField(role.Fields, "role_key")] = true
		if role.Status != StatusActive || stringField(role.Fields, "display_name") == "" {
			t.Fatalf("unexpected role config: %+v", role)
		}
	}
	for _, key := range []string{"user", "team_leader", "admin"} {
		if !roleKeys[key] {
			t.Fatalf("expected seeded role key %s, got %+v", key, roleKeys)
		}
	}

	identityProviders := store.ListResources("identity-providers")
	if len(identityProviders) != 1 || identityProviders[0].ID != "idp_oidc_template" {
		t.Fatalf("expected default identity provider template, got %+v", identityProviders)
	}
	if stringField(identityProviders[0].Fields, "provider_type") != "oidc" || stringField(identityProviders[0].Fields, "client_id") == "" {
		t.Fatalf("unexpected identity provider fields: %+v", identityProviders[0].Fields)
	}
}

func TestAdminImportsUsersFromExistingSystemCSV(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	messages := configureTestSMTPChannel(t, store)
	app := New(store).Handler()

	content := "username,name,email,role,team_id,status\nimported_user,导入用户,imported@example.com,user,team_platform,active\n"
	resp := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source":  "manual_csv",
		"format":  "csv",
		"content": content,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected import 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"created":1`) || !strings.Contains(resp.Body, `"updated":0`) {
		t.Fatalf("expected one created user: %s", resp.Body)
	}
	assertPasswordResetEmail(t, messages, "imported@example.com")

	update := "username,name,email,role,team_id,status\nimported_user,导入用户已更新,imported@example.com,team_leader,team_platform,active\n"
	updated := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source":  "manual_csv",
		"format":  "csv",
		"content": update,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected import update 200, got %d: %s", updated.Code, updated.Body)
	}
	if !strings.Contains(updated.Body, `"created":0`) || !strings.Contains(updated.Body, `"updated":1`) {
		t.Fatalf("expected one updated user: %s", updated.Body)
	}
	assertPasswordResetEmail(t, messages, "imported@example.com")
	users := store.ListAdminUsers()
	var found AdminUser
	for _, user := range users {
		if user.Email == "imported@example.com" {
			found = user
			break
		}
	}
	if found.ID == "" || found.Name != "导入用户已更新" || found.Role != "team_leader" {
		t.Fatalf("expected imported user update, got %+v", found)
	}
}

func TestBootstrapUsesConfiguredAdminPassword(t *testing.T) {
	store := NewMemoryStore()
	config := ConfigFromEnv()
	config.BootstrapAdminPassword = "configured-bootstrap-password"
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.AuthenticateAdminUser("admin", config.BootstrapAdminPassword, time.Hour); err != nil {
		t.Fatalf("expected configured bootstrap password to authenticate: %v", err)
	}
	if _, _, err := store.AuthenticateAdminUser("admin", "admin123456", time.Hour); AsHTTPError(err).Code != "invalid_credentials" {
		t.Fatalf("expected hard-coded default password to be rejected, got %v", err)
	}
}

func TestAdminImportsUsersFromHeaderlessCSV(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("teams", AdminResource{ID: "team_UK8jwEcIIoFmNVmJ", Name: "Imported Team", Status: StatusActive})
	messages := configureTestSMTPChannel(t, store)
	app := New(store).Handler()

	content := "xiemengjun,谢孟军,xiemengjun@e-lead.cn,admin,team_UK8jwEcIIoFmNVmJ,active\n" +
		"lisk,李世康,lisk@e-lead.cn,admin,team_UK8jwEcIIoFmNVmJ,active\n"
	resp := doJSON(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source":  "manual_csv",
		"format":  "csv",
		"content": content,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected import 200, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"created":2`) || !strings.Contains(resp.Body, `"skipped":0`) {
		t.Fatalf("expected two created users: %s", resp.Body)
	}
	assertPasswordResetEmail(t, messages, "xiemengjun@e-lead.cn")
	assertPasswordResetEmail(t, messages, "lisk@e-lead.cn")
}

func TestGatewayRejectsUnauthorizedModel(t *testing.T) {
	app := newTestServer()
	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "not-allowed",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "model_not_allowed") {
		t.Fatalf("expected model_not_allowed: %s", resp.Body)
	}
}

func TestGatewayQuotaExceeded(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Limited"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "limited",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 1, MonthlyRequests: 1, MaxConcurrency: 1},
		Status:  StatusActive,
	}, "thk_limited")
	if err != nil {
		t.Fatal(err)
	}
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "gpt-4.1-mini",
			"messages": []map[string]any{
				{"role": "user", "content": "hello"},
			},
		}, secret)
		if i == 0 && resp.Code != http.StatusOK {
			t.Fatalf("first request expected 200, got %d: %s", resp.Code, resp.Body)
		}
		if i == 1 && resp.Code != http.StatusTooManyRequests {
			t.Fatalf("second request expected 429, got %d: %s", resp.Code, resp.Body)
		}
	}
}

func TestQuotaPolicyAppliesAtRuntime(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_policy", Name: "Policy Team", Status: StatusActive})
	project := store.CreateProject(Project{Name: "Policy Limited", TeamID: "team_policy"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "policy-key",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 100},
		Status:  StatusActive,
	}, "thk_policy_limited")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("quota-policies", AdminResource{
		Name:   "Project hard cap",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       project.ID,
			"daily_requests": 1,
		},
	})
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "gpt-4.1-mini",
			"messages": []map[string]any{
				{"role": "user", "content": "quota policy"},
			},
		}, secret)
		if i == 0 && resp.Code != http.StatusOK {
			t.Fatalf("first request expected 200, got %d: %s", resp.Code, resp.Body)
		}
		if i == 1 && resp.Code != http.StatusTooManyRequests {
			t.Fatalf("second request expected 429 from quota policy, got %d: %s", resp.Code, resp.Body)
		}
	}
}

func TestBudgetExceededBlocksRuntimeCalls(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Budget Limited", CostCenter: "CC-BLOCK"})
	apiKey, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "budget-key",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 100},
		Status:  StatusActive,
	}, "thk_budget_limited")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	period := now.Format("2006-01")
	store.CreateResource("budgets", AdminResource{
		Name:   "Blocking budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":       "cost_center",
			"scope_id":    "CC-BLOCK",
			"period_ref":  period,
			"amount_usd":  1,
			"enforcement": "block",
		},
	})
	if err := store.db.Create(&UsageRecord{
		ID:          NewID("usage"),
		RequestID:   NewID("req"),
		ProjectID:   project.ID,
		APIKeyID:    apiKey.ID,
		ModelName:   "gpt-4.1-mini",
		InputTokens: 10,
		TotalTokens: 10,
		CostUSD:     1,
		CreatedAt:   now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	blocked := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "budget should block"},
		},
	}, secret)
	if blocked.Code != http.StatusTooManyRequests || !strings.Contains(blocked.Body, "budget_exceeded") {
		t.Fatalf("expected budget_exceeded, got %d: %s", blocked.Code, blocked.Body)
	}
	budgets := store.ListResources("budgets")
	budgets[0].Fields["enforcement"] = "warn"
	if _, err := store.UpdateResource("budgets", budgets[0].ID, budgets[0]); err != nil {
		t.Fatal(err)
	}
	allowed := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "budget warn only"},
		},
	}, secret)
	if allowed.Code != http.StatusOK {
		t.Fatalf("warn-only budget should allow runtime call, got %d: %s", allowed.Code, allowed.Body)
	}
}

func TestRuntimeBudgetUsesActualUsageInsteadOfCachedUsedField(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Fresh Budget", CostCenter: "CC-FRESH"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "fresh-budget-key",
		Allowed: []string{"gpt-4.1-mini"},
		Limits:  QuotaLimits{DailyRequests: 100},
		Status:  StatusActive,
	}, "thk_fresh_budget")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("budgets", AdminResource{
		Name:   "Stale report cache",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":       "cost_center",
			"scope_id":    "CC-FRESH",
			"period_ref":  time.Now().UTC().Format("2006-01"),
			"amount_usd":  1,
			"used_usd":    99,
			"enforcement": "block",
		},
	})
	mock := store.AddProvider(Provider{Name: "Mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "gpt-4.1-mini", ProviderID: mock.ID, ProviderModel: "mock-chat", Status: StatusActive})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "budget should use actual usage"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("stale used_usd should not block runtime call, got %d: %s", resp.Code, resp.Body)
	}
}

func TestAPIKeyIPAllowlistAndRotation(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Key Ops"})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:        "restricted",
		Group:       "dedicated",
		Allowed:     []string{"gpt-4.1-mini"},
		IPAllowlist: []string{"10.0.0.0/8"},
		Status:      StatusActive,
	}, "thk_restricted")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ValidateAPIKey(secret, "127.0.0.1"); AsHTTPError(err).Code != "api_key_disabled" {
		t.Fatalf("expected ip allowlist rejection, got %v", err)
	}
	if _, valid, err := store.ValidateAPIKey(secret, "10.1.2.3"); err != nil || valid.Group != "dedicated" {
		t.Fatalf("expected valid key with group, got key=%+v err=%v", valid, err)
	}
	rotated, newSecret, err := store.RotateAPIKey(key.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RotatedFromID != key.ID || newSecret == "" {
		t.Fatalf("unexpected rotated key: %+v secret=%q", rotated, newSecret)
	}
	if _, _, err := store.ValidateAPIKey(secret, "10.1.2.3"); AsHTTPError(err).Code != "api_key_disabled" {
		t.Fatalf("old key should be revoked, got %v", err)
	}
	if _, _, err := store.ValidateAPIKey(newSecret, "10.1.2.3"); err != nil {
		t.Fatalf("new key should work: %v", err)
	}
}

func TestAPIKeyStatusUpdatePreservesExpiration(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Expiring Key Ops"})
	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:      "expiring",
		Status:    StatusActive,
		ExpiresAt: &expiresAt,
	}, "thk_expiring")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateAPIKey(key.ID, APIKey{Status: StatusDisabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("expected disabled key, got %s", updated.Status)
	}
	if updated.ExpiresAt == nil || !updated.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expected expiration to be preserved, got %v want %v", updated.ExpiresAt, expiresAt)
	}
}

func TestAdminCreatesProjectAndKey(t *testing.T) {
	app := newTestServer()
	project := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{
		"name":    "Production App",
		"team_id": "team_platform",
	}, "")
	if project.Code != http.StatusCreated {
		t.Fatalf("expected project created, got %d: %s", project.Code, project.Body)
	}
	var created Project
	if err := json.Unmarshal([]byte(project.Body), &created); err != nil {
		t.Fatal(err)
	}

	key := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+created.ID+"/keys", map[string]any{
		"name":           "prod-key",
		"allowed_models": []string{"gpt-4.1-mini"},
		"limits": map[string]any{
			"daily_requests":  10,
			"max_concurrency": 2,
		},
	}, "")
	if key.Code != http.StatusCreated {
		t.Fatalf("expected key created, got %d: %s", key.Code, key.Body)
	}
	if !strings.Contains(key.Body, `"plain_text_visible_once":true`) || !strings.Contains(key.Body, `"api_key":"sk_`) {
		t.Fatalf("expected one-time key response: %s", key.Body)
	}
}

func TestAdminProjectCreateRequiresExistingActivePrimaryTeam(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_inactive", Name: "Inactive Team", Status: StatusDisabled})
	app := New(store).Handler()

	missing := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{
		"name":    "Missing Team Project",
		"team_id": "team_missing",
	}, "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body, "team_not_found") {
		t.Fatalf("missing primary team should be rejected, got %d: %s", missing.Code, missing.Body)
	}
	inactive := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{
		"name":    "Inactive Team Project",
		"team_id": "team_inactive",
	}, "")
	if inactive.Code != http.StatusBadRequest || !strings.Contains(inactive.Body, "team_inactive") {
		t.Fatalf("inactive primary team should be rejected, got %d: %s", inactive.Code, inactive.Body)
	}
	if projects := store.ListProjects(); len(projects) != 0 {
		t.Fatalf("invalid primary teams must not create projects: %+v", projects)
	}
}

func TestAPIKeyGenerationUsesSystemSettings(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("settings", AdminResource{
		ID:          "cfg_gateway",
		Name:        "Gateway Base Settings",
		Description: "Default OpenAI-compatible gateway configuration.",
		Status:      StatusActive,
		Fields: map[string]any{
			"public_base_url":       "http://localhost:8080",
			"default_timeout":       "120s",
			"audit_retention":       "180d",
			"api_key_prefix":        "corp_",
			"api_key_random_length": 32,
		},
	})
	app := New(store).Handler()

	key := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+defaultProjectID+"/keys", map[string]any{
		"name":           "custom-format-key",
		"allowed_models": []string{"gpt-4.1-mini"},
	}, "")
	if key.Code != http.StatusCreated {
		t.Fatalf("expected key created, got %d: %s", key.Code, key.Body)
	}
	var created struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(key.Body), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.APIKey, "corp_") {
		t.Fatalf("expected custom prefix, got %s", created.APIKey)
	}
	if randomPart := strings.TrimPrefix(created.APIKey, "corp_"); len(randomPart) != 32 {
		t.Fatalf("expected 32 random characters, got %d in %s", len(randomPart), created.APIKey)
	}

	rotated := doJSON(t, app, http.MethodPost, "/api/admin/api-keys/"+created.ID+"/rotate", map[string]any{}, "")
	if rotated.Code != http.StatusCreated {
		t.Fatalf("expected rotated key, got %d: %s", rotated.Code, rotated.Body)
	}
	var rotatedPayload struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(rotated.Body), &rotatedPayload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rotatedPayload.APIKey, "corp_") {
		t.Fatalf("expected rotated key to use custom prefix, got %s", rotatedPayload.APIKey)
	}
	if randomPart := strings.TrimPrefix(rotatedPayload.APIKey, "corp_"); len(randomPart) != 32 {
		t.Fatalf("expected rotated key to use 32 random characters, got %d in %s", len(randomPart), rotatedPayload.APIKey)
	}
}

func TestApprovalFlowInterceptsAPIKeyCreate(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Approval Project"})
	store.CreateResource("approval-flows", AdminResource{
		Name:   "Key approval",
		Status: StatusActive,
		Fields: map[string]any{
			"trigger":       "api_key_create",
			"approver_role": "admin",
		},
	})
	app := New(store).Handler()

	key := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{
		"name":           "needs-approval",
		"allowed_models": []string{"gpt-4.1-mini"},
		"limits": map[string]any{
			"daily_requests": 10,
		},
	}, "")
	if key.Code != http.StatusAccepted {
		t.Fatalf("expected approval response, got %d: %s", key.Code, key.Body)
	}
	var pendingKeyResponse struct {
		ApprovalRequired bool   `json:"approval_required"`
		APIKey           string `json:"api_key"`
	}
	if err := json.Unmarshal([]byte(key.Body), &pendingKeyResponse); err != nil {
		t.Fatal(err)
	}
	if !pendingKeyResponse.ApprovalRequired || pendingKeyResponse.APIKey != "" {
		t.Fatalf("expected pending approval without secret: %s", key.Body)
	}

	approvals := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, "")
	if approvals.Code != http.StatusOK {
		t.Fatalf("expected approvals list, got %d: %s", approvals.Code, approvals.Body)
	}
	var list struct {
		Data []ApprovalRequest `json:"data"`
	}
	if err := json.Unmarshal([]byte(approvals.Body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].Status != "pending" || list.Data[0].Trigger != "api_key_create" {
		t.Fatalf("unexpected approvals: %s", approvals.Body)
	}

	approved := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+list.Data[0].ID+"/approve", map[string]any{}, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("expected approval apply, got %d: %s", approved.Code, approved.Body)
	}
	if !strings.Contains(approved.Body, `"api_key":"sk_`) || !strings.Contains(approved.Body, `"status":"approved"`) {
		t.Fatalf("expected approved key result: %s", approved.Body)
	}
	if len(store.ListAPIKeys()) != 1 {
		t.Fatalf("expected key created after approval")
	}
}

func TestProjectQuotaIncreaseApprovalCreatesAndLinksPolicy(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Quota Project", Status: StatusActive})
	app := New(store).Handler()

	request := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/quota-increase", map[string]any{
		"name": "Quota Project 提升额度",
		"fields": map[string]any{
			"daily_requests":   20,
			"monthly_requests": 500,
			"monthly_cost_usd": 25,
		},
	}, "")
	if request.Code != http.StatusAccepted {
		t.Fatalf("expected quota approval request, got %d: %s", request.Code, request.Body)
	}
	if !strings.Contains(request.Body, `"approval_required":true`) || !strings.Contains(request.Body, `"trigger":"quota_increase"`) {
		t.Fatalf("expected quota approval payload: %s", request.Body)
	}

	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvals[0].ResourceType != "quota-policies" || approvals[0].ResourceID != "" {
		t.Fatalf("unexpected quota approvals: %+v", approvals)
	}

	approved := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("expected quota approval apply, got %d: %s", approved.Code, approved.Body)
	}

	quotas := store.ListResources("quota-policies")
	if len(quotas) != 1 {
		t.Fatalf("expected one quota policy after approval, got %+v", quotas)
	}
	if stringField(quotas[0].Fields, "scope") != "project" || stringField(quotas[0].Fields, "scope_id") != project.ID {
		t.Fatalf("expected project-scoped quota policy, got %+v", quotas[0].Fields)
	}
	if int64Field(quotas[0].Fields, "daily_requests") != 20 || int64Field(quotas[0].Fields, "monthly_requests") != 500 {
		t.Fatalf("expected approved quota limits, got %+v", quotas[0].Fields)
	}
	updatedProject, ok := store.GetProject(project.ID)
	if !ok || updatedProject.DefaultQuotaRef != quotas[0].ID {
		t.Fatalf("expected project quota ref %s, got %+v", quotas[0].ID, updatedProject)
	}
}

func TestLinkedTeamQuotaPermissionsAndPrimaryTeamApprovalResponsibility(t *testing.T) {
	store := NewMemoryStore()
	for _, teamID := range []string{"team_primary", "team_viewer", "team_maintainer"} {
		store.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
	}
	primaryLeader, err := store.CreateAdminUser(AdminUser{
		Username: "primary-approval-leader",
		Name:     "Primary Approval Leader",
		Email:    "primary-approval-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_primary",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	viewerLeader, err := store.CreateAdminUser(AdminUser{
		Username: "viewer-approval-leader",
		Name:     "Viewer Approval Leader",
		Email:    "viewer-approval-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_viewer",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	maintainerLeader, err := store.CreateAdminUser(AdminUser{
		Username: "maintainer-approval-leader",
		Name:     "Maintainer Approval Leader",
		Email:    "maintainer-approval-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_maintainer",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Shared Quota Project", TeamID: "team_primary", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_viewer", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_maintainer", Role: "maintainer"}); err != nil {
		t.Fatal(err)
	}
	quota := store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_shared_project",
		Name:   "Shared Project Quota",
		Status: StatusActive,
		Fields: map[string]any{"daily_requests": 100},
	})
	if _, err := store.UpdateProject(project.ID, Project{
		TeamID:          project.TeamID,
		OwnerUserID:     project.OwnerUserID,
		CostCenter:      project.CostCenter,
		Status:          project.Status,
		DefaultQuotaRef: quota.ID,
	}); err != nil {
		t.Fatal(err)
	}
	_, primarySession, err := store.AuthenticateAdminUser(primaryLeader.Email, "leader123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, viewerSession, err := store.AuthenticateAdminUser(viewerLeader.Email, "leader123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, maintainerSession, err := store.AuthenticateAdminUser(maintainerLeader.Email, "leader123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	viewerRequest := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/quota-increase", map[string]any{
		"fields": map[string]any{"daily_requests": 200},
	}, viewerSession.Token)
	if viewerRequest.Code != http.StatusForbidden {
		t.Fatalf("viewer-linked team must not request quota increases, got %d: %s", viewerRequest.Code, viewerRequest.Body)
	}
	viewerMutation := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+quota.ID, map[string]any{
		"name": "Viewer Changed Quota",
	}, viewerSession.Token)
	if viewerMutation.Code != http.StatusForbidden {
		t.Fatalf("viewer-linked team must not mutate project quota policies, got %d: %s", viewerMutation.Code, viewerMutation.Body)
	}

	maintainerRequest := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/quota-increase", map[string]any{
		"fields": map[string]any{"daily_requests": 300},
	}, maintainerSession.Token)
	if maintainerRequest.Code != http.StatusAccepted {
		t.Fatalf("maintainer-linked team should request quota increases, got %d: %s", maintainerRequest.Code, maintainerRequest.Body)
	}
	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvalProjectID(approvals[0]) != project.ID {
		t.Fatalf("expected one project approval, got %+v", approvals)
	}

	secondaryList := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, maintainerSession.Token)
	if secondaryList.Code != http.StatusOK || strings.Contains(secondaryList.Body, approvals[0].ID) {
		t.Fatalf("secondary team leader must not see project approvals: %d %s", secondaryList.Code, secondaryList.Body)
	}
	primaryList := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, primarySession.Token)
	if primaryList.Code != http.StatusOK || !strings.Contains(primaryList.Body, approvals[0].ID) {
		t.Fatalf("primary team leader should see project approvals: %d %s", primaryList.Code, primaryList.Body)
	}
	secondaryDecision := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, maintainerSession.Token)
	if secondaryDecision.Code != http.StatusForbidden || !strings.Contains(secondaryDecision.Body, "approval_primary_team_forbidden") {
		t.Fatalf("secondary team leader must not decide project approvals: %d %s", secondaryDecision.Code, secondaryDecision.Body)
	}
	primaryDecision := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, primarySession.Token)
	if primaryDecision.Code != http.StatusOK {
		t.Fatalf("primary team leader should decide project approvals: %d %s", primaryDecision.Code, primaryDecision.Body)
	}

	secondRequest := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/quota-increase", map[string]any{
		"fields": map[string]any{"daily_requests": 400},
	}, maintainerSession.Token)
	if secondRequest.Code != http.StatusAccepted {
		t.Fatalf("expected second quota approval request, got %d: %s", secondRequest.Code, secondRequest.Body)
	}
	approvals = store.ListApprovalRequests()
	if len(approvals) != 2 {
		t.Fatalf("expected two project approvals, got %+v", approvals)
	}
	pendingApproval := approvals[0]
	if pendingApproval.Status != "pending" {
		t.Fatalf("expected newest approval to be pending, got %+v", pendingApproval)
	}
	adminList := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, "")
	if adminList.Code != http.StatusOK || !strings.Contains(adminList.Body, pendingApproval.ID) {
		t.Fatalf("platform admin should see project approvals: %d %s", adminList.Code, adminList.Body)
	}
	adminDecision := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+pendingApproval.ID+"/approve", map[string]any{}, "")
	if adminDecision.Code != http.StatusOK {
		t.Fatalf("platform admin should decide project approvals: %d %s", adminDecision.Code, adminDecision.Body)
	}

	if _, err := store.UpdateResource("quota-policies", quota.ID, AdminResource{
		Name:   "Unscoped Default Quota",
		Status: StatusActive,
		Fields: map[string]any{"daily_requests": 400},
	}); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("approval-flows", AdminResource{
		ID:     "apf_partial_quota_update",
		Name:   "Partial Quota Update",
		Status: StatusActive,
		Fields: map[string]any{"trigger": "quota_increase", "approver_role": "team_leader"},
	})
	partialUpdate := doJSON(t, app, http.MethodPatch, "/api/admin/resources/quota-policies/"+quota.ID, map[string]any{
		"name": "Partially Updated Quota",
	}, maintainerSession.Token)
	if partialUpdate.Code != http.StatusAccepted {
		t.Fatalf("maintainer partial quota update should require approval, got %d: %s", partialUpdate.Code, partialUpdate.Body)
	}
	partialApproval := store.ListApprovalRequests()[0]
	if approvalProjectID(partialApproval) != project.ID {
		t.Fatalf("partial quota approval lost project context: %+v", partialApproval)
	}
	secondaryList = doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, maintainerSession.Token)
	if secondaryList.Code != http.StatusOK || strings.Contains(secondaryList.Body, partialApproval.ID) {
		t.Fatalf("secondary team leader must not see partial quota approvals: %d %s", secondaryList.Code, secondaryList.Body)
	}
	primaryList = doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, primarySession.Token)
	if primaryList.Code != http.StatusOK || !strings.Contains(primaryList.Body, partialApproval.ID) {
		t.Fatalf("primary team leader should see partial quota approvals: %d %s", primaryList.Code, primaryList.Body)
	}
	secondaryDecision = doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+partialApproval.ID+"/approve", map[string]any{}, maintainerSession.Token)
	if secondaryDecision.Code != http.StatusForbidden || !strings.Contains(secondaryDecision.Body, "approval_primary_team_forbidden") {
		t.Fatalf("secondary team leader must not decide partial quota approvals: %d %s", secondaryDecision.Code, secondaryDecision.Body)
	}
	primaryDecision = doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+partialApproval.ID+"/approve", map[string]any{}, primarySession.Token)
	if primaryDecision.Code != http.StatusOK {
		t.Fatalf("primary team leader should decide partial quota approvals: %d %s", primaryDecision.Code, primaryDecision.Body)
	}
	var updatedQuota AdminResource
	for _, item := range store.ListResources("quota-policies") {
		if item.ID == quota.ID {
			updatedQuota = item
			break
		}
	}
	if updatedQuota.Name != "Partially Updated Quota" || int64Field(updatedQuota.Fields, "daily_requests") != 400 || stringField(updatedQuota.Fields, "scope_id") != "" {
		t.Fatalf("partial quota approval should preserve the existing default-policy fields: %+v", updatedQuota)
	}
}

func TestApprovalProjectIDSupportsDirectAndScopedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "direct", payload: `{"project_id":"prj_direct"}`, want: "prj_direct"},
		{name: "scoped fields", payload: `{"fields":{"scope":"project","scope_id":"prj_scoped"}}`, want: "prj_scoped"},
		{name: "other scope", payload: `{"fields":{"scope":"team","scope_id":"team_one"}}`},
		{name: "invalid", payload: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := approvalProjectID(ApprovalRequest{Payload: test.payload}); got != test.want {
				t.Fatalf("approvalProjectID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAPIKeyUpdateApprovalUsesProjectPrimaryTeam(t *testing.T) {
	store := NewMemoryStore()
	for _, teamID := range []string{"team_key_primary", "team_key_secondary"} {
		store.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
	}
	primaryLeader, err := store.CreateAdminUser(AdminUser{
		Username: "primary-key-approver",
		Name:     "Primary Key Approver",
		Email:    "primary-key-approver@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_key_primary",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	secondaryLeader, err := store.CreateAdminUser(AdminUser{
		Username: "secondary-key-requester",
		Name:     "Secondary Key Requester",
		Email:    "secondary-key-requester@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_key_secondary",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Primary Key Approval Project", TeamID: "team_key_primary", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_key_secondary", Role: "maintainer"}); err != nil {
		t.Fatal(err)
	}
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Approval Key", Allowed: []string{"old-model"}, Status: StatusActive}, "thk_primary_approval")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("approval-flows", AdminResource{
		ID:     "apf_key_model_access",
		Name:   "Key Model Access",
		Status: StatusActive,
		Fields: map[string]any{"trigger": "model_access", "approver_role": "team_leader"},
	})
	_, primarySession, err := store.AuthenticateAdminUser(primaryLeader.Email, "leader123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, secondarySession, err := store.AuthenticateAdminUser(secondaryLeader.Email, "leader123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	requested := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+key.ID, map[string]any{
		"allowed_models": []string{"new-model"},
	}, secondarySession.Token)
	if requested.Code != http.StatusAccepted {
		t.Fatalf("secondary maintainer should request key model access, got %d: %s", requested.Code, requested.Body)
	}
	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvalProjectID(approvals[0]) != project.ID {
		t.Fatalf("API-key approval must retain its project: %+v", approvals)
	}
	secondaryList := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, secondarySession.Token)
	if secondaryList.Code != http.StatusOK || strings.Contains(secondaryList.Body, approvals[0].ID) {
		t.Fatalf("secondary team leader must not see key approvals: %d %s", secondaryList.Code, secondaryList.Body)
	}
	primaryList := doJSON(t, app, http.MethodGet, "/api/admin/approvals", nil, primarySession.Token)
	if primaryList.Code != http.StatusOK || !strings.Contains(primaryList.Body, approvals[0].ID) {
		t.Fatalf("primary team leader should see key approvals: %d %s", primaryList.Code, primaryList.Body)
	}
	secondaryDecision := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, secondarySession.Token)
	if secondaryDecision.Code != http.StatusForbidden || !strings.Contains(secondaryDecision.Body, "approval_primary_team_forbidden") {
		t.Fatalf("secondary team leader must not decide key approvals: %d %s", secondaryDecision.Code, secondaryDecision.Body)
	}
	primaryDecision := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, primarySession.Token)
	if primaryDecision.Code != http.StatusOK {
		t.Fatalf("primary team leader should decide key approvals: %d %s", primaryDecision.Code, primaryDecision.Body)
	}
	updatedKeys := store.ListProjectKeys(project.ID)
	if len(updatedKeys) != 1 || len(updatedKeys[0].Allowed) != 1 || updatedKeys[0].Allowed[0] != "new-model" {
		t.Fatalf("approved model access was not applied: %+v", updatedKeys)
	}
}

func TestTeamLeaderScopedResourceFilteringUsesConstantQueries(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{
		ID:     "team_filter",
		Name:   "Filter Team",
		Status: StatusActive,
		Fields: map[string]any{"cost_center": "CC-FILTER"},
	})
	leader, err := store.CreateAdminUser(AdminUser{
		Username: "filter-leader",
		Name:     "Filter Leader",
		Email:    "filter-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_filter",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Filter Project", TeamID: "team_filter", Status: StatusActive})
	memberships := make([]AdminResource, 0, 24)
	quotas := make([]AdminResource, 0, 24)
	for index := 0; index < 24; index++ {
		user, err := store.CreateAdminUser(AdminUser{
			Username: "filter-member-" + strconv.Itoa(index),
			Name:     "Filter Member " + strconv.Itoa(index),
			Email:    "filter-member-" + strconv.Itoa(index) + "@tokenhub.local",
			Role:     "user",
			TeamID:   "team_filter",
			Status:   StatusActive,
		}, "member123456")
		if err != nil {
			t.Fatal(err)
		}
		memberships = append(memberships, store.CreateResource("project-members", AdminResource{
			Name:   "Filter Membership " + strconv.Itoa(index),
			Status: StatusActive,
			Fields: map[string]any{"project_id": project.ID, "user_id": user.ID, "role": "viewer"},
		}))
		quotas = append(quotas, store.CreateResource("quota-policies", AdminResource{
			Name:   "Filter Quota " + strconv.Itoa(index),
			Status: StatusActive,
			Fields: map[string]any{"scope": "project", "scope_id": project.ID},
		}))
	}
	server := New(store)

	for _, test := range []struct {
		name  string
		kind  string
		items []AdminResource
	}{
		{name: "project members", kind: "project-members", items: memberships},
		{name: "quota policies", kind: "quota-policies", items: quotas},
	} {
		t.Run(test.name, func(t *testing.T) {
			var small, large []AdminResource
			smallQueries := countStoreQueries(t, store, func() {
				small = server.filterResourcesForUser(leader, test.kind, test.items[:1])
			})
			largeQueries := countStoreQueries(t, store, func() {
				large = server.filterResourcesForUser(leader, test.kind, test.items)
			})
			if len(small) != 1 || len(large) != len(test.items) {
				t.Fatalf("unexpected filtered resources: small=%d large=%d", len(small), len(large))
			}
			if largeQueries > smallQueries {
				t.Fatalf("query count grew with rows: small=%d large=%d", smallQueries, largeQueries)
			}
		})
	}
}

func countStoreQueries(t *testing.T, store *GormStore, fn func()) int {
	t.Helper()
	callbackName := "test:count-queries:" + NewID("callback")
	count := 0
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) {
		count++
	}); err != nil {
		t.Fatal(err)
	}
	fn()
	if err := store.db.Callback().Query().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	return count
}
