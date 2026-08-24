package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAdminAPIRequiresToken(t *testing.T) {
	app := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_admin_token") {
		t.Fatalf("expected invalid_admin_token: %s", rr.Body.String())
	}
}

func TestAdminLoginAndUserManagement(t *testing.T) {
	app := newTestServer()
	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "admin@tokenhub.local",
		"password": "admin123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d: %s", login.Code, login.Body)
	}
	var payload struct {
		Token string    `json:"token"`
		User  AdminUser `json:"user"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" || payload.User.Email != "admin@tokenhub.local" {
		t.Fatalf("unexpected login payload: %s", login.Body)
	}

	users := doJSON(t, app, http.MethodGet, "/api/admin/users", nil, payload.Token)
	if users.Code != http.StatusOK {
		t.Fatalf("expected users 200, got %d: %s", users.Code, users.Body)
	}
	if !strings.Contains(users.Body, `"email":"admin@tokenhub.local"`) || strings.Contains(users.Body, "PasswordHash") {
		t.Fatalf("unexpected users payload: %s", users.Body)
	}
}

func TestAdminAuthIdentityProvidersListActiveOAuthSources(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_gitlab",
		Name:   "GitLab OAuth",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2",
			"issuer_url":    "http://gitlab.example.test",
			"client_id":     "gitlab-client",
			"client_secret": "secret-value",
			"authorize_url": "http://gitlab.example.test/oauth/authorize",
			"token_url":     "http://gitlab.example.test/oauth/token",
			"userinfo_url":  "http://gitlab.example.test/api/v4/user",
		},
	})
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_disabled",
		Name:   "Disabled OAuth",
		Status: StatusDisabled,
		Fields: map[string]any{
			"provider_type": "oauth2",
			"client_id":     "disabled-client",
			"authorize_url": "http://disabled.example.test/oauth/authorize",
			"token_url":     "http://disabled.example.test/oauth/token",
			"userinfo_url":  "http://disabled.example.test/userinfo",
		},
	})
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_google",
		Name:   "Company SSO",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_type": "oauth2",
			"icon_key":      "google",
			"client_id":     "google-client",
			"authorize_url": "http://accounts.example.test/oauth/authorize",
			"token_url":     "http://accounts.example.test/oauth/token",
			"userinfo_url":  "http://accounts.example.test/userinfo",
		},
	})
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "test-secret"}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/identity-providers", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected providers 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"id":"idp_gitlab"`) ||
		!strings.Contains(rr.Body.String(), `"icon_key":"gitlab"`) ||
		!strings.Contains(rr.Body.String(), `"display_name":"GitLab"`) ||
		!strings.Contains(rr.Body.String(), `"id":"idp_google"`) ||
		!strings.Contains(rr.Body.String(), `"icon_key":"google"`) ||
		!strings.Contains(rr.Body.String(), `"display_name":"Google"`) ||
		strings.Contains(rr.Body.String(), "secret-value") ||
		strings.Contains(rr.Body.String(), "idp_disabled") {
		t.Fatalf("unexpected providers payload: %s", rr.Body.String())
	}
}

func TestAdminOAuthLoginCreatesSession(t *testing.T) {
	var receivedTokenRequest bool
	var receivedUserInfoRequest bool
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			receivedTokenRequest = true
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("grant_type") != "authorization_code" ||
				r.FormValue("code") != "oauth-code" ||
				r.FormValue("client_id") != "gitlab-client" ||
				r.FormValue("client_secret") != "gitlab-secret" ||
				r.FormValue("redirect_uri") != "http://localhost:8080/api/admin/auth/oauth/callback" {
				t.Fatalf("unexpected token form: %+v", r.Form)
			}
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "gitlab-access-token", "token_type": "Bearer"})
		case "/api/v4/user":
			receivedUserInfoRequest = true
			if r.Header.Get("authorization") != "Bearer gitlab-access-token" {
				t.Fatalf("unexpected userinfo authorization: %s", r.Header.Get("authorization"))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"name":       "GitLab User",
				"email":      "gitlab.user@example.test",
				"department": "Product",
			})
		default:
			t.Fatalf("unexpected oauth path: %s", r.URL.Path)
		}
	}))
	defer oauthServer.Close()

	store := NewMemoryStore()
	store.CreateResource("identity-providers", AdminResource{
		ID:     "idp_gitlab",
		Name:   "GitLab OAuth",
		Status: StatusActive,
		Fields: map[string]any{
			"provider_type":  "oauth2",
			"issuer_url":     oauthServer.URL,
			"client_id":      "gitlab-client",
			"client_secret":  "gitlab-secret",
			"authorize_url":  oauthServer.URL + "/oauth/authorize",
			"token_url":      oauthServer.URL + "/oauth/token",
			"userinfo_url":   oauthServer.URL + "/api/v4/user",
			"redirect_uri":   "http://localhost:8080/api/admin/auth/oauth/callback",
			"scopes":         "openid profile email read_user",
			"username_claim": "name",
			"email_claim":    "email",
			"team_claim":     "department",
		},
	})
	app := NewWithConfig(store, Config{
		AdminToken: "dev_admin_token", SecretKey: "test-secret",
		CORSAllowedOrigins: []string{"http://localhost:3001"},
	}).Handler()

	startReq := httptest.NewRequest(http.MethodGet, adminOAuthStartURLForTest(t, "/api/admin/auth/oauth/start?id=idp_gitlab&return_url=http%3A%2F%2Flocalhost%3A3001%2Foverview"), nil)
	startReq.Host = "127.0.0.1:8080"
	startResp := httptest.NewRecorder()
	app.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusFound {
		t.Fatalf("expected start redirect, got %d: %s", startResp.Code, startResp.Body.String())
	}
	authorizeLocation, err := url.Parse(startResp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if authorizeLocation.Path != "/oauth/authorize" {
		t.Fatalf("unexpected authorize location: %s", authorizeLocation.String())
	}
	authorizeQuery := authorizeLocation.Query()
	if authorizeQuery.Get("client_id") != "gitlab-client" ||
		authorizeQuery.Get("redirect_uri") != "http://localhost:8080/api/admin/auth/oauth/callback" ||
		authorizeQuery.Get("scope") != "openid profile email read_user" ||
		authorizeQuery.Get("response_type") != "code" ||
		authorizeQuery.Get("state") == "" {
		t.Fatalf("unexpected authorize query: %s", authorizeLocation.RawQuery)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/admin/auth/oauth/callback?code=oauth-code&state="+url.QueryEscape(authorizeQuery.Get("state")), nil)
	callbackReq.Host = "localhost:8080"
	callbackReq.AddCookie(requireResponseCookieWithPrefix(t, startResp, adminOAuthStateCookiePrefix))
	callbackResp := httptest.NewRecorder()
	app.ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusFound {
		t.Fatalf("expected callback redirect, got %d: %s", callbackResp.Code, callbackResp.Body.String())
	}
	if !receivedTokenRequest || !receivedUserInfoRequest {
		t.Fatalf("expected token and userinfo requests, token=%v userinfo=%v", receivedTokenRequest, receivedUserInfoRequest)
	}
	returnLocation, err := url.Parse(callbackResp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if returnLocation.String() == "" || returnLocation.Scheme != "http" || returnLocation.Host != "localhost:3001" || returnLocation.Path != "/overview" {
		t.Fatalf("unexpected return location: %s", returnLocation.String())
	}
	if returnLocation.Query().Has("oauth_token") || returnLocation.Query().Has("oauth_code") || strings.Contains(returnLocation.String(), "oauth_token=") {
		t.Fatalf("OAuth callback leaked a credential in the query: %s", returnLocation.String())
	}
	returnParams, err := url.ParseQuery(returnLocation.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	exchangeCode := returnParams.Get("oauth_code")
	if exchangeCode == "" {
		t.Fatalf("missing OAuth exchange code fragment: %s", returnLocation.Fragment)
	}
	session := exchangeAdminOAuthCodeForTest(t, app, exchangeCode, testAdminOAuthCodeVerifier)
	me := doJSON(t, app, http.MethodGet, "/api/admin/auth/me", nil, session.Token)
	if me.Code != http.StatusOK || !strings.Contains(me.Body, `"email":"gitlab.user@example.test"`) || !strings.Contains(me.Body, `"role":"user"`) {
		t.Fatalf("unexpected me response: %d %s", me.Code, me.Body)
	}
}

func TestOAuthDefaultProvisioningAssignsTeamRoleAndProject(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{
		ID:     "team_product",
		Name:   "Product",
		Status: StatusActive,
		Fields: map[string]any{
			"code": "PRODUCT",
		},
	})
	project := store.CreateProject(Project{Name: "AI Platform", TeamID: "team_product", Status: StatusActive})
	provider := AdminResource{
		ID:     "idp_enterprise",
		Name:   "Enterprise SSO",
		Status: StatusActive,
		Fields: map[string]any{
			"username_claim":       "name",
			"email_claim":          "email",
			"team_claim":           "department",
			"default_role":         "team_leader",
			"default_team_id":      "team_product",
			"default_project_id":   project.ID,
			"default_project_role": "developer",
		},
	}
	server := New(store)

	user, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"name":       "OAuth Leader",
		"email":      "leader@example.test",
		"department": "Unknown Department",
	})
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != "team_leader" || user.TeamID != "team_product" {
		t.Fatalf("unexpected oauth user defaults: role=%s team=%s", user.Role, user.TeamID)
	}
	member, ok := findProjectMember(store.ListResources("project-members"), project.ID, user.ID)
	if !ok {
		t.Fatalf("expected default project membership for %s in %s", user.ID, project.ID)
	}
	if stringField(member.Fields, "role") != "developer" || !truthyField(member.Fields, "can_issue_keys") {
		t.Fatalf("unexpected default project member fields: %+v", member.Fields)
	}

	existing, err := store.CreateAdminUser(AdminUser{
		Username: "existing-oauth",
		Name:     "Existing OAuth",
		Email:    "existing@example.test",
		Role:     "team_leader",
		Status:   StatusActive,
	}, "existing123456")
	if err != nil {
		t.Fatal(err)
	}
	provider.Fields["default_role"] = "user"
	provider.Fields["default_project_role"] = "viewer"
	updated, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"name":  "Existing OAuth Renamed",
		"email": existing.Email,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Role != "team_leader" {
		t.Fatalf("existing oauth user role should not be overwritten, got %s", updated.Role)
	}
	if updated.TeamID != "team_product" {
		t.Fatalf("existing oauth user without team should receive default team, got %s", updated.TeamID)
	}
	if _, ok := findProjectMember(store.ListResources("project-members"), project.ID, updated.ID); !ok {
		t.Fatalf("expected default project membership for existing user")
	}
	if _, err := server.upsertOAuthAdminUser(provider, map[string]any{
		"name":  "Existing OAuth Renamed",
		"email": existing.Email,
	}); err != nil {
		t.Fatal(err)
	}
	if got := countProjectMembers(store.ListResources("project-members"), project.ID, updated.ID); got != 1 {
		t.Fatalf("expected one default project membership after repeated login, got %d", got)
	}
}

func findProjectMember(items []AdminResource, projectID string, userID string) (AdminResource, bool) {
	for _, item := range items {
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == projectID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == userID {
			return item, true
		}
	}
	return AdminResource{}, false
}

func countProjectMembers(items []AdminResource, projectID string, userID string) int {
	count := 0
	for _, item := range items {
		if strings.TrimSpace(stringField(item.Fields, "project_id")) == projectID &&
			strings.TrimSpace(stringField(item.Fields, "user_id")) == userID {
			count++
		}
	}
	return count
}

func TestRBACAndAdminAuditEvents(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateAdminUser(AdminUser{
		Username: "viewer",
		Name:     "Viewer",
		Email:    "viewer@tokenhub.local",
		Role:     "viewer",
		Status:   StatusActive,
	}, "viewer123456")
	if err != nil {
		t.Fatal(err)
	}
	_ = viewer
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "viewer@tokenhub.local",
		"password": "viewer123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	forbidden := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name": "Forbidden Provider",
		"type": "mock",
	}, payload.Token)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("viewer should not create provider, got %d: %s", forbidden.Code, forbidden.Body)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{"name": "Audited Project"}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("admin project create failed: %d %s", created.Code, created.Body)
	}
	audit := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, "")
	if audit.Code != http.StatusOK {
		t.Fatalf("audit events failed: %d %s", audit.Code, audit.Body)
	}
	if !strings.Contains(audit.Body, `"resource_type":"project"`) || !strings.Contains(audit.Body, `"action":"create"`) {
		t.Fatalf("expected project create audit event: %s", audit.Body)
	}
}

func TestRolePermissionsForDeveloperAndTeamLeaderWorkspaces(t *testing.T) {
	if !canAdmin("user", "playground", http.MethodPost) {
		t.Fatal("regular user should be allowed to use playground")
	}
	if canAdmin("user", "routing", http.MethodPost) {
		t.Fatal("regular user should not manage routing")
	}
	if !canAdmin("team_leader", "project", http.MethodPost) {
		t.Fatal("team leader should be allowed to manage team projects")
	}
	if !canAdmin("team_leader", "quota", http.MethodGet) {
		t.Fatal("team leader should be allowed to read visible project quotas")
	}
	if !canAdmin("team_leader", "quota", http.MethodPost) {
		t.Fatal("team leader should be allowed to request or save visible project quotas")
	}
}

func TestRegularUserModelsComeFromActiveRoutesNotKeys(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_platform", Name: "Platform Team", Status: StatusActive})
	_, err := store.CreateAdminUser(AdminUser{
		Username: "model-viewer",
		Name:     "Model Viewer",
		Email:    "model-viewer@tokenhub.local",
		Role:     "user",
		TeamID:   "team_platform",
		Status:   StatusActive,
	}, "viewer123456")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "routed-chat", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "unrouted-chat", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "disabled-routed-chat", Modality: "chat", Status: StatusDisabled})
	store.AddModel(Model{Name: "disabled-route-chat", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "routed-chat", ProviderID: "provider_mock", ProviderModel: "routed-chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "disabled-routed-chat", ProviderID: "provider_mock", ProviderModel: "disabled-routed-chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "disabled-route-chat", ProviderID: "provider_mock", ProviderModel: "disabled-route-chat", Status: StatusDisabled})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "model-viewer@tokenhub.local",
		"password": "viewer123456",
	}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	models := doJSON(t, app, http.MethodGet, "/api/admin/models", nil, payload.Token)
	if models.Code != http.StatusOK {
		t.Fatalf("models failed: %d %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, "routed-chat") {
		t.Fatalf("expected routed model without any user key: %s", models.Body)
	}
	for _, hidden := range []string{"unrouted-chat", "disabled-routed-chat", "disabled-route-chat"} {
		if strings.Contains(models.Body, hidden) {
			t.Fatalf("model %s should not be visible: %s", hidden, models.Body)
		}
	}
}

func TestTeamLeaderProjectManagementIsTeamScoped(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_project", Name: "Project Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_other", Name: "Other Team", Status: StatusActive})
	leader, err := store.CreateAdminUser(AdminUser{
		Username: "project-leader",
		Name:     "Project Leader",
		Email:    "project-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_project",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	teamProject := store.CreateProject(Project{Name: "Existing Team Project", TeamID: leader.TeamID})
	otherProject := store.CreateProject(Project{Name: "Other Team Project", TeamID: "team_other"})
	teamQuota := store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_team_project",
		Name:   "Team Project Quota",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       teamProject.ID,
			"daily_requests": 100,
		},
	})
	otherQuota := store.CreateResource("quota-policies", AdminResource{
		ID:     "quota_other_project",
		Name:   "Other Project Quota",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":          "project",
			"scope_id":       otherProject.ID,
			"daily_requests": 200,
		},
	})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "project-leader@tokenhub.local",
		"password": "leader123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	created := doJSON(t, app, http.MethodPost, "/api/admin/projects", map[string]any{
		"name":          "Team Project",
		"team_id":       "team_other",
		"owner_user_id": "",
	}, payload.Token)
	if created.Code != http.StatusCreated {
		t.Fatalf("team leader project create failed: %d %s", created.Code, created.Body)
	}
	var project Project
	if err := json.Unmarshal([]byte(created.Body), &project); err != nil {
		t.Fatal(err)
	}
	if project.TeamID != leader.TeamID || project.OwnerUserID != leader.ID {
		t.Fatalf("team leader project should be scoped to own team/user: %+v", project)
	}
	quotas := doJSON(t, app, http.MethodGet, "/api/admin/resources/quota-policies", nil, payload.Token)
	if quotas.Code != http.StatusOK {
		t.Fatalf("team leader should read scoped project quotas, got %d: %s", quotas.Code, quotas.Body)
	}
	if !strings.Contains(quotas.Body, teamQuota.ID) || strings.Contains(quotas.Body, otherQuota.ID) {
		t.Fatalf("quota list should be scoped to team projects: %s", quotas.Body)
	}
	createdQuota := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name":   "Created Team Project Quota",
		"status": StatusActive,
		"fields": map[string]any{
			"scope":            "project",
			"scope_id":         teamProject.ID,
			"monthly_requests": 500,
		},
	}, payload.Token)
	if createdQuota.Code != http.StatusCreated {
		t.Fatalf("team leader should create quota for own project, got %d: %s", createdQuota.Code, createdQuota.Body)
	}
	forbiddenQuota := doJSON(t, app, http.MethodPost, "/api/admin/resources/quota-policies", map[string]any{
		"name":   "Other Team Quota",
		"status": StatusActive,
		"fields": map[string]any{
			"scope":    "project",
			"scope_id": otherProject.ID,
		},
	}, payload.Token)
	if forbiddenQuota.Code != http.StatusForbidden {
		t.Fatalf("team leader should not create quota for another team project, got %d: %s", forbiddenQuota.Code, forbiddenQuota.Body)
	}
	forbidden := doJSON(t, app, http.MethodPatch, "/api/admin/projects/"+otherProject.ID, map[string]any{
		"name":    "Hijacked",
		"team_id": leader.TeamID,
	}, payload.Token)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("team leader should not update another team project, got %d: %s", forbidden.Code, forbidden.Body)
	}
}

func TestProjectMembersAssignMultipleProjectsAndKeyIssueScope(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_member", Name: "Member Team", Status: StatusActive})
	user, err := store.CreateAdminUser(AdminUser{
		Username: "project-member",
		Name:     "Project Member",
		Email:    "project-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_member",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := store.CreateAdminUser(AdminUser{
		Username: "other-member",
		Name:     "Other Member",
		Email:    "other-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_member",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	developerProject := store.CreateProject(Project{Name: "Developer Project", TeamID: user.TeamID})
	viewerProject := store.CreateProject(Project{Name: "Viewer Project", TeamID: user.TeamID})
	sameTeamProject := store.CreateProject(Project{Name: "Same Team Unassigned Project", TeamID: user.TeamID})
	otherMemberProject := store.CreateProject(Project{Name: "Other Member Project", TeamID: user.TeamID})
	store.CreateResource("project-members", AdminResource{
		Name:   "Developer Project Member",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": developerProject.ID,
			"user_id":    user.ID,
			"role":       "developer",
		},
	})
	store.CreateResource("project-members", AdminResource{
		Name:   "Viewer Project Member",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": viewerProject.ID,
			"user_id":    user.ID,
			"role":       "viewer",
		},
	})
	store.CreateResource("project-members", AdminResource{
		Name:   "Other Project Member",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": otherMemberProject.ID,
			"user_id":    otherUser.ID,
			"role":       "developer",
		},
	})
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "project-member@tokenhub.local",
		"password": "user123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	projects := doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, payload.Token)
	if projects.Code != http.StatusOK {
		t.Fatalf("user project list failed: %d %s", projects.Code, projects.Body)
	}
	if !strings.Contains(projects.Body, developerProject.ID) || !strings.Contains(projects.Body, viewerProject.ID) {
		t.Fatalf("assigned projects should be visible: %s", projects.Body)
	}
	for _, hidden := range []string{sameTeamProject.ID, otherMemberProject.ID} {
		if strings.Contains(projects.Body, hidden) {
			t.Fatalf("unassigned project %s should not be visible: %s", hidden, projects.Body)
		}
	}
	memberships := doJSON(t, app, http.MethodGet, "/api/admin/resources/project-members", nil, payload.Token)
	if memberships.Code != http.StatusOK {
		t.Fatalf("user project memberships failed: %d %s", memberships.Code, memberships.Body)
	}
	if !strings.Contains(memberships.Body, developerProject.ID) || !strings.Contains(memberships.Body, viewerProject.ID) ||
		strings.Contains(memberships.Body, otherMemberProject.ID) {
		t.Fatalf("user should only read own project memberships: %s", memberships.Body)
	}
	createdKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+developerProject.ID+"/keys", map[string]any{
		"name": "Developer Key",
	}, payload.Token)
	if createdKey.Code != http.StatusCreated || !strings.Contains(createdKey.Body, `"api_key"`) {
		t.Fatalf("developer member should issue key, got %d: %s", createdKey.Code, createdKey.Body)
	}
	forbiddenOwner := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+developerProject.ID+"/keys", map[string]any{
		"name":          "Wrong Owner Key",
		"owner_user_id": otherUser.ID,
	}, payload.Token)
	if forbiddenOwner.Code != http.StatusForbidden {
		t.Fatalf("ordinary user should not assign a key to another user, got %d: %s", forbiddenOwner.Code, forbiddenOwner.Body)
	}
	viewerKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+viewerProject.ID+"/keys", map[string]any{
		"name": "Viewer Key",
	}, payload.Token)
	if viewerKey.Code != http.StatusForbidden {
		t.Fatalf("viewer member should not issue key, got %d: %s", viewerKey.Code, viewerKey.Body)
	}
	unassignedKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+sameTeamProject.ID+"/keys", map[string]any{
		"name": "Unassigned Key",
	}, payload.Token)
	if unassignedKey.Code != http.StatusForbidden {
		t.Fatalf("same-team unassigned user should not issue key, got %d: %s", unassignedKey.Code, unassignedKey.Body)
	}
}

func TestProjectTeamAssociationGrantsRoleBasedAccessAndRevokesImmediately(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		ID:       "usr_project_team_admin",
		Username: "project-team-admin",
		Name:     "Project Team Admin",
		Email:    "project-team-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("teams", AdminResource{ID: "team_primary", Name: "Primary Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_shared", Name: "Shared Team", Status: StatusActive})
	project := store.CreateProject(Project{Name: "Shared Project", TeamID: "team_primary", Status: StatusActive})
	user, err := store.CreateAdminUser(AdminUser{
		Username: "shared-team-member",
		Name:     "Shared Team Member",
		Email:    "shared-team-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_shared",
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/teams", map[string]any{
		"team_id": "team_shared",
		"role":    "viewer",
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("project team create failed: %d %s", created.Code, created.Body)
	}
	duplicate := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/teams", map[string]any{
		"team_id": "team_shared",
		"role":    "developer",
	}, "")
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate project team should conflict, got %d: %s", duplicate.Code, duplicate.Body)
	}
	listed := doJSON(t, app, http.MethodGet, "/api/admin/projects/"+project.ID+"/teams?limit=1&offset=1", nil, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, `"total":2`) || !strings.Contains(listed.Body, `"team_id":"team_shared"`) {
		t.Fatalf("paginated project team list failed: %d %s", listed.Code, listed.Body)
	}

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Email,
		"password": "member123456",
	}, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &session); err != nil {
		t.Fatal(err)
	}
	projects := doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, session.Token)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body, project.ID) {
		t.Fatalf("viewer team member should see the project: %d %s", projects.Code, projects.Body)
	}
	viewerKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{"name": "Viewer Key"}, session.Token)
	if viewerKey.Code != http.StatusForbidden {
		t.Fatalf("viewer team member should not issue keys, got %d: %s", viewerKey.Code, viewerKey.Body)
	}

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/projects/"+project.ID+"/teams/team_shared", map[string]any{"role": "developer"}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("project team update failed: %d %s", updated.Code, updated.Body)
	}
	developerKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{"name": "Developer Key"}, session.Token)
	if developerKey.Code != http.StatusCreated {
		t.Fatalf("developer team member should issue own key, got %d: %s", developerKey.Code, developerKey.Body)
	}

	removed := doJSON(t, app, http.MethodDelete, "/api/admin/projects/"+project.ID+"/teams/team_shared", nil, "")
	if removed.Code != http.StatusNoContent {
		t.Fatalf("project team delete failed: %d %s", removed.Code, removed.Body)
	}
	projects = doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, session.Token)
	if projects.Code != http.StatusOK || strings.Contains(projects.Body, project.ID) {
		t.Fatalf("removed team member should lose access immediately: %d %s", projects.Code, projects.Body)
	}
	audit := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, "")
	for _, action := range []string{`"action":"create"`, `"action":"update"`, `"action":"delete"`} {
		if !strings.Contains(audit.Body, action) || !strings.Contains(audit.Body, `"resource_type":"project_team"`) {
			t.Fatalf("project team changes should be audited: %s", audit.Body)
		}
	}
}

func TestDisabledLinkedTeamRevokesProjectAccessAndRejectsNewAssignments(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_active_primary", Name: "Active Primary Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_disable_shared", Name: "Shared Team", Status: StatusActive})
	project := store.CreateProject(Project{Name: "Disable Team Project", TeamID: "team_active_primary", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_disable_shared", Role: "developer"}); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{
		Username: "disabled-team-member",
		Name:     "Disabled Team Member",
		Email:    "disabled-team-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_disable_shared",
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Email,
		"password": "member123456",
	}, "")
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &session); err != nil {
		t.Fatal(err)
	}
	createdKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{"name": "Active Team Key"}, session.Token)
	if createdKey.Code != http.StatusCreated {
		t.Fatalf("active linked team should grant developer access: %d %s", createdKey.Code, createdKey.Body)
	}

	if _, err := store.UpdateResource("teams", "team_disable_shared", AdminResource{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	projects := doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, session.Token)
	if projects.Code != http.StatusOK || strings.Contains(projects.Body, project.ID) {
		t.Fatalf("disabled linked team must revoke project visibility immediately: %d %s", projects.Code, projects.Body)
	}
	forbiddenKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{"name": "Disabled Team Key"}, session.Token)
	if forbiddenKey.Code != http.StatusForbidden {
		t.Fatalf("disabled linked team must revoke project mutation access: %d %s", forbiddenKey.Code, forbiddenKey.Body)
	}

	_, err = store.CreateAdminUser(AdminUser{
		Username: "new-disabled-team-member",
		Name:     "New Disabled Team Member",
		Email:    "new-disabled-team-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_disable_shared",
		Status:   StatusActive,
	}, "member123456")
	if err == nil || AsHTTPError(err).Code != "team_inactive" {
		t.Fatalf("new user assignment to a disabled team must fail with team_inactive, got %v", err)
	}
}

func TestProjectAccessMergesRolesAcrossUserTeamsWithoutDuplicatingProjects(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_primary", Name: "Primary Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_viewer", Name: "Viewer Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_developer", Name: "Developer Team", Status: StatusActive})
	user, err := store.CreateAdminUser(AdminUser{
		Username: "multi-team-member",
		Name:     "Multi Team Member",
		Email:    "multi-team-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_viewer",
		TeamIDs:  []string{"team_viewer", "team_developer"},
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Role Merge Project", TeamID: "team_primary", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_viewer", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_developer", Role: "developer"}); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Email,
		"password": "member123456",
	}, "")
	var session struct {
		Token string    `json:"token"`
		User  AdminUser `json:"user"`
	}
	if err := json.Unmarshal([]byte(login.Body), &session); err != nil {
		t.Fatal(err)
	}
	if len(session.User.TeamIDs) != 2 {
		t.Fatalf("user team memberships were not returned: %+v", session.User.TeamIDs)
	}

	projects := doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, session.Token)
	var projectList struct {
		Data []Project `json:"data"`
	}
	if err := json.Unmarshal([]byte(projects.Body), &projectList); err != nil {
		t.Fatal(err)
	}
	if len(projectList.Data) != 1 || projectList.Data[0].ID != project.ID {
		t.Fatalf("multiple team access must return one project row: %s", projects.Body)
	}
	createdKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{"name": "Merged Role Key"}, session.Token)
	if createdKey.Code != http.StatusCreated {
		t.Fatalf("highest developer role should allow key issuance: %d %s", createdKey.Code, createdKey.Body)
	}
	if keys := store.ListProjectKeys(project.ID); len(keys) != 1 {
		t.Fatalf("project resources must not be copied per team: %+v", keys)
	}

	if _, err := store.UpdateProjectTeam(project.ID, "team_developer", "viewer"); err != nil {
		t.Fatal(err)
	}
	viewerKey := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{"name": "Viewer Only Key"}, session.Token)
	if viewerKey.Code != http.StatusForbidden {
		t.Fatalf("merged viewer roles should not issue keys, got %d: %s", viewerKey.Code, viewerKey.Body)
	}
	projects = doJSON(t, app, http.MethodGet, "/api/admin/projects", nil, session.Token)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body, project.ID) {
		t.Fatalf("viewer access from either team should remain stable: %d %s", projects.Code, projects.Body)
	}
}

func TestAdminUserAPIStoresPrimaryAndAdditionalTeams(t *testing.T) {
	store := NewMemoryStore()
	for _, teamID := range []string{"team_primary", "team_secondary"} {
		store.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
	}
	if _, err := store.CreateAdminUser(AdminUser{
		ID:       "usr_multi_team_admin",
		Username: "multi-team-admin",
		Name:     "Multi Team Admin",
		Email:    "multi-team-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/users", map[string]any{
		"username": "api-multi-team-user",
		"name":     "API Multi Team User",
		"email":    "api-multi-team-user@tokenhub.local",
		"password": "member123456",
		"role":     "user",
		"team_id":  "team_primary",
		"team_ids": []string{"team_primary", "team_secondary", "team_secondary"},
		"status":   StatusActive,
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("multi-team user create failed: %d %s", created.Code, created.Body)
	}
	var user AdminUser
	if err := json.Unmarshal([]byte(created.Body), &user); err != nil {
		t.Fatal(err)
	}
	if user.TeamID != "team_primary" || !equalStringSlices(user.TeamIDs, []string{"team_primary", "team_secondary"}) {
		t.Fatalf("unexpected normalized team memberships: primary=%s teams=%v", user.TeamID, user.TeamIDs)
	}
}

func TestProjectTeamRemovalAndTeamDeletionAreSafe(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		ID:       "usr_team_safety_admin",
		Username: "team-safety-admin",
		Name:     "Team Safety Admin",
		Email:    "team-safety-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	for _, teamID := range []string{"team_primary", "team_secondary", "team_only"} {
		store.CreateResource("teams", AdminResource{ID: teamID, Name: teamID, Status: StatusActive})
	}
	project := store.CreateProject(Project{Name: "Safe Team Project", TeamID: "team_primary", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_secondary", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	teamlessProject := store.CreateProject(Project{Name: "Last Team Project", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: teamlessProject.ID, TeamID: "team_only", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	linkedDelete := doJSON(t, app, http.MethodDelete, "/api/admin/resources/teams/team_secondary", nil, "")
	if linkedDelete.Code != http.StatusConflict || !strings.Contains(linkedDelete.Body, "team_has_projects") {
		t.Fatalf("linked team deletion should be blocked: %d %s", linkedDelete.Code, linkedDelete.Body)
	}
	primaryRemove := doJSON(t, app, http.MethodDelete, "/api/admin/projects/"+project.ID+"/teams/team_primary", nil, "")
	if primaryRemove.Code != http.StatusConflict || !strings.Contains(primaryRemove.Body, "project_primary_team") {
		t.Fatalf("primary team removal should be blocked: %d %s", primaryRemove.Code, primaryRemove.Body)
	}
	lastRemove := doJSON(t, app, http.MethodDelete, "/api/admin/projects/"+teamlessProject.ID+"/teams/team_only", nil, "")
	if lastRemove.Code != http.StatusConflict || !strings.Contains(lastRemove.Body, "project_last_team") {
		t.Fatalf("last team removal should be blocked: %d %s", lastRemove.Code, lastRemove.Body)
	}

	unlinked := doJSON(t, app, http.MethodDelete, "/api/admin/projects/"+project.ID+"/teams/team_secondary", nil, "")
	if unlinked.Code != http.StatusNoContent {
		t.Fatalf("secondary team unlink failed: %d %s", unlinked.Code, unlinked.Body)
	}
	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/resources/teams/team_secondary", nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("unlinked team should be deletable: %d %s", deleted.Code, deleted.Body)
	}
}

func TestAdminAPIKeyOwnerAttributionAndUsageSnapshot(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_key_owner", Name: "Key Owner Team", Status: StatusActive})
	if _, err := store.CreateAdminUser(AdminUser{
		ID:       "usr_admin",
		Username: "key-owner-admin",
		Name:     "Key Owner Admin",
		Email:    "key-owner-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAdminUser(AdminUser{
		Username: "key-owner",
		Name:     "Key Owner",
		Email:    "key-owner@tokenhub.local",
		Role:     "user",
		TeamID:   "team_key_owner",
		Status:   StatusActive,
	}, "owner123456")
	if err != nil {
		t.Fatal(err)
	}
	otherOwner, err := store.CreateAdminUser(AdminUser{
		Username: "key-owner-other",
		Name:     "Other Key Owner",
		Email:    "key-owner-other@tokenhub.local",
		Role:     "user",
		TeamID:   "team_key_owner",
		Status:   StatusActive,
	}, "owner123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Key Attribution Project", TeamID: owner.TeamID, Status: StatusActive})
	server := New(store)
	app := server.Handler()

	createKey := func(name string) APIKey {
		t.Helper()
		resp := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{
			"name":          name,
			"owner_user_id": owner.ID,
		}, "")
		if resp.Code != http.StatusCreated {
			t.Fatalf("create owned key failed: %d %s", resp.Code, resp.Body)
		}
		var payload struct {
			ID          string `json:"id"`
			OwnerUserID string `json:"owner_user_id"`
		}
		if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.OwnerUserID != owner.ID {
			t.Fatalf("created key owner = %q, want %q", payload.OwnerUserID, owner.ID)
		}
		for _, key := range store.ListAPIKeys() {
			if key.ID == payload.ID {
				return key
			}
		}
		t.Fatalf("created key %q not found", payload.ID)
		return APIKey{}
	}

	keyA := createKey("Owner Key A")
	keyB := createKey("Owner Key B")
	if keyA.Metadata["created_by"] != "usr_admin" {
		t.Fatalf("key issuer metadata = %q, want usr_admin", keyA.Metadata["created_by"])
	}
	finishUsage := func(requestID string, key APIKey, totalTokens int64) {
		store.FinishCall(CallContext{
			RequestID: requestID,
			Project:   project,
			Key:       key,
			Model:     Model{Name: "gpt-4.1-mini"},
			StartedAt: time.Now(),
		}, RouteSelection{}, Usage{PromptTokens: totalTokens, TotalTokens: totalTokens}, http.StatusOK, "", "127.0.0.1", "owner-test")
	}
	finishUsage("req_owner_a_before_transfer", keyA, 100)

	transfer := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+keyA.ID, map[string]any{
		"owner_user_id": otherOwner.ID,
	}, "")
	if transfer.Code != http.StatusOK {
		t.Fatalf("transfer key owner failed: %d %s", transfer.Code, transfer.Body)
	}
	updatedKeyA, err := server.findAPIKey(keyA.ID)
	if err != nil {
		t.Fatal(err)
	}
	finishUsage("req_owner_a_after_transfer", updatedKeyA, 300)
	finishUsage("req_owner_b", keyB, 200)

	rotate := doJSON(t, app, http.MethodPost, "/api/admin/api-keys/"+keyA.ID+"/rotate", map[string]any{}, "")
	if rotate.Code != http.StatusCreated {
		t.Fatalf("rotate transferred key failed: %d %s", rotate.Code, rotate.Body)
	}
	var rotatedPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(rotate.Body), &rotatedPayload); err != nil {
		t.Fatal(err)
	}
	rotatedKey, err := server.findAPIKey(rotatedPayload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotatedKey.OwnerUserID != otherOwner.ID {
		t.Fatalf("rotated key owner = %q, want %q", rotatedKey.OwnerUserID, otherOwner.ID)
	}

	resp := doJSON(t, app, http.MethodGet, "/api/admin/usage/breakdown", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("usage breakdown failed: %d %s", resp.Code, resp.Body)
	}
	var breakdown struct {
		Members []struct {
			ID            string `json:"id"`
			RequestCount  int64  `json:"request_count"`
			TotalTokens   int64  `json:"total_tokens"`
			OwnedKeyCount int    `json:"owned_key_count"`
			UsedKeyCount  int    `json:"used_key_count"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &breakdown); err != nil {
		t.Fatal(err)
	}
	rows := map[string]struct {
		requests int64
		tokens   int64
		owned    int
		used     int
	}{}
	for _, row := range breakdown.Members {
		rows[row.ID] = struct {
			requests int64
			tokens   int64
			owned    int
			used     int
		}{row.RequestCount, row.TotalTokens, row.OwnedKeyCount, row.UsedKeyCount}
	}
	if got := rows[owner.ID]; got.requests != 2 || got.tokens != 300 || got.owned != 1 || got.used != 2 {
		t.Fatalf("original owner usage = %+v, want requests=2 tokens=300 owned=1 used=2", got)
	}
	if got := rows[otherOwner.ID]; got.requests != 1 || got.tokens != 300 || got.owned != 1 || got.used != 1 {
		t.Fatalf("new owner usage = %+v, want requests=1 tokens=300 owned=1 used=1", got)
	}
}

func TestAPIKeyCreateApprovalPreservesOwnerAndIssuer(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_approval_key", Name: "Approval Key Team", Status: StatusActive})
	requester, err := store.CreateAdminUser(AdminUser{
		Username: "approval-key-requester",
		Name:     "Approval Key Requester",
		Email:    "approval-key-requester@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_approval_key",
		Status:   StatusActive,
	}, "requester123456")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAdminUser(AdminUser{
		Username: "approval-key-owner",
		Name:     "Approval Key Owner",
		Email:    "approval-key-owner@tokenhub.local",
		Role:     "user",
		TeamID:   requester.TeamID,
		Status:   StatusActive,
	}, "owner123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Approval Key Project", TeamID: requester.TeamID, Status: StatusActive})
	server := New(store)
	result, err := server.applyApprovalRequest(ApprovalRequest{
		ID:           "approval_key_create",
		Trigger:      "api_key_create",
		ResourceType: "api_key",
		RequesterID:  requester.ID,
		Status:       "pending",
		Payload: snapshotJSON(map[string]any{
			"project_id":    project.ID,
			"name":          "Approved Owned Key",
			"owner_user_id": owner.ID,
		}),
	}, AdminUser{ID: "approval-admin", Role: "admin", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := result.(map[string]any)
	if !ok || payload["owner_user_id"] != owner.ID {
		t.Fatalf("approval result = %#v, want owner %q", result, owner.ID)
	}
	keys := store.ListAPIKeys()
	if len(keys) != 1 {
		t.Fatalf("approved keys = %d, want 1", len(keys))
	}
	if keys[0].OwnerUserID != owner.ID || keys[0].Metadata["created_by"] != requester.ID {
		t.Fatalf("approved key attribution = owner %q issuer %q", keys[0].OwnerUserID, keys[0].Metadata["created_by"])
	}
}

func TestUserRequestAuditIsScopedToOwnLogs(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "request-auditor",
		Name:     "Request Auditor",
		Email:    "request-auditor@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := store.CreateAdminUser(AdminUser{
		Username: "other-request-auditor",
		Name:     "Other Request Auditor",
		Email:    "other-request-auditor@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "other123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "User Audit Project", OwnerUserID: user.ID})
	otherProject := store.CreateProject(Project{Name: "Other Audit Project", OwnerUserID: otherUser.ID})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:     "user-owned-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": user.ID},
	}, "thk_user_audit")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{
		Name:     "other-owned-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": otherUser.ID},
	}, "thk_other_audit")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.db.Create(&RequestLog{
		ID:         "log_user_visible",
		RequestID:  "req_user_visible",
		ProjectID:  project.ID,
		APIKeyID:   key.ID,
		ModelName:  "gpt-4.1-mini",
		StatusCode: http.StatusOK,
		LatencyMS:  120,
		CreatedAt:  now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{
		ID:         "log_other_hidden",
		RequestID:  "req_other_hidden",
		ProjectID:  otherProject.ID,
		APIKeyID:   otherKey.ID,
		ModelName:  "gpt-4.1-mini",
		StatusCode: http.StatusOK,
		LatencyMS:  95,
		CreatedAt:  now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestPayloadLog{
		ID:           "payload_user_visible",
		RequestID:    "req_user_visible",
		RequestBody:  `{"model":"gpt-4.1-mini"}`,
		ResponseBody: `{"id":"chatcmpl_user"}`,
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "request-auditor@tokenhub.local",
		"password": "user123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	logs := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests", nil, payload.Token)
	if logs.Code != http.StatusOK {
		t.Fatalf("expected user request audit 200, got %d: %s", logs.Code, logs.Body)
	}
	if !strings.Contains(logs.Body, "req_user_visible") || strings.Contains(logs.Body, "req_other_hidden") {
		t.Fatalf("request audit should only include user's logs: %s", logs.Body)
	}
	detail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/req_user_visible", nil, payload.Token)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body, "chatcmpl_user") {
		t.Fatalf("expected own request detail, got %d: %s", detail.Code, detail.Body)
	}
	hiddenDetail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/req_other_hidden", nil, payload.Token)
	if hiddenDetail.Code != http.StatusForbidden {
		t.Fatalf("expected hidden request detail 403, got %d: %s", hiddenDetail.Code, hiddenDetail.Body)
	}
	adminAudit := doJSON(t, app, http.MethodGet, "/api/admin/audit/events", nil, payload.Token)
	if adminAudit.Code != http.StatusForbidden {
		t.Fatalf("user should not read admin audit events, got %d: %s", adminAudit.Code, adminAudit.Body)
	}
}

func TestTeamLeaderUsageBreakdownIncludesMembers(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_usage", Name: "Usage Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_other", Name: "Other Team", Status: StatusActive})
	leader, err := store.CreateAdminUser(AdminUser{
		Username: "usage-leader",
		Name:     "Usage Leader",
		Email:    "usage-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_usage",
		Status:   StatusActive,
	}, "leader123456")
	if err != nil {
		t.Fatal(err)
	}
	memberA, err := store.CreateAdminUser(AdminUser{
		Username: "usage-member-a",
		Name:     "Usage Member A",
		Email:    "usage-member-a@tokenhub.local",
		Role:     "user",
		TeamID:   leader.TeamID,
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	memberB, err := store.CreateAdminUser(AdminUser{
		Username: "usage-member-b",
		Name:     "Usage Member B",
		Email:    "usage-member-b@tokenhub.local",
		Role:     "user",
		TeamID:   leader.TeamID,
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	otherMember, err := store.CreateAdminUser(AdminUser{
		Username: "usage-member-other",
		Name:     "Usage Member Other",
		Email:    "usage-member-other@tokenhub.local",
		Role:     "user",
		TeamID:   "team_other",
		Status:   StatusActive,
	}, "member123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Team Usage App", TeamID: leader.TeamID})
	otherProject := store.CreateProject(Project{Name: "Other Usage App", TeamID: otherMember.TeamID})
	keyA, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "member-a-key", Status: StatusActive, Metadata: map[string]string{"created_by": memberA.ID}}, "thk_usage_member_a")
	if err != nil {
		t.Fatal(err)
	}
	keyB, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "member-b-key", Status: StatusActive, Metadata: map[string]string{"created_by": memberB.ID}}, "thk_usage_member_b")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "other-member-key", Status: StatusActive, Metadata: map[string]string{"created_by": otherMember.ID}}, "thk_usage_other")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	records := []UsageRecord{
		{ID: "usage_member_a", RequestID: "req_member_a", ProjectID: project.ID, APIKeyID: keyA.ID, ModelName: "gpt-4.1-mini", TotalTokens: 100, CostUSD: 0.1, CreatedAt: now},
		{ID: "usage_member_b", RequestID: "req_member_b", ProjectID: project.ID, APIKeyID: keyB.ID, ModelName: "gpt-4.1-mini", TotalTokens: 250, CostUSD: 0.2, CreatedAt: now},
		{ID: "usage_other", RequestID: "req_other", ProjectID: otherProject.ID, APIKeyID: otherKey.ID, ModelName: "gpt-4.1-mini", TotalTokens: 999, CostUSD: 9.9, CreatedAt: now},
	}
	if err := store.db.Create(&records).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": "usage-leader@tokenhub.local",
		"password": "leader123456",
	}, "")
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &payload); err != nil {
		t.Fatal(err)
	}
	resp := doJSON(t, app, http.MethodGet, "/api/admin/usage/breakdown", nil, payload.Token)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected team leader usage breakdown, got %d: %s", resp.Code, resp.Body)
	}
	var breakdown struct {
		Members []struct {
			ID           string `json:"id"`
			RequestCount int64  `json:"request_count"`
			TotalTokens  int64  `json:"total_tokens"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &breakdown); err != nil {
		t.Fatal(err)
	}
	totals := map[string]int64{}
	for _, row := range breakdown.Members {
		totals[row.ID] = row.TotalTokens
		if row.RequestCount != 1 {
			t.Fatalf("expected one request per member row, got %+v", row)
		}
	}
	if totals[memberA.ID] != 100 || totals[memberB.ID] != 250 {
		t.Fatalf("expected member totals for team members, got %+v", totals)
	}
	if _, ok := totals[otherMember.ID]; ok {
		t.Fatalf("other team member should not be included: %+v", totals)
	}
}
