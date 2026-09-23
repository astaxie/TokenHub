package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type adminProjectMethodRoute struct {
	name         string
	path         string
	wrongMethod  string
	allowed      []string
	allow        string
	userStatus   int
	userCode     string
	userHasAllow bool
}

func TestAdminProjectMethodRoutesPreserveAuthenticationAndAuthorizationOrder(t *testing.T) {
	store, _, app, adminToken, userToken, projectID := newAdminProjectMethodRoutingServer(t)
	routes := adminProjectMethodRoutes(projectID)
	teamLeaderToken := createAdminProjectMethodSession(t, store, AdminUser{
		Username: "project-method-team-leader",
		Name:     "Project Method Team Leader",
		Email:    "project-method-team-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_project_method",
		Status:   StatusActive,
	})
	securityToken := createAdminProjectMethodSession(t, store, AdminUser{
		Username: "project-method-security",
		Name:     "Project Method Security",
		Email:    "project-method-security@tokenhub.local",
		Role:     "security_admin",
		Status:   StatusActive,
	})

	projectsBefore := len(store.ListProjects())
	keysBefore := len(store.ListAPIKeys())
	approvalsBefore := len(store.ListApprovalRequests())
	auditsBefore := len(store.ListAuditEvents())
	linksBefore := adminProjectTeamCount(t, store, projectID)

	for _, route := range routes {
		t.Run(route.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})

		t.Run(route.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})

		t.Run(route.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, userToken)
			assertJSONError(t, response, route.userStatus, route.userCode)
			if route.userHasAllow {
				assertAllowHeader(t, response, route.allow)
			} else {
				assertAllowHeader(t, response, "")
			}
		})

		t.Run(route.name+"/team_leader", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, teamLeaderToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})

		t.Run(route.name+"/security_admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, securityToken)
			if route.name == "quota increase" {
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
				return
			}
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
	}

	if got := len(store.ListProjects()); got != projectsBefore {
		t.Fatalf("wrong methods changed projects: got %d, want %d", got, projectsBefore)
	}
	if got := len(store.ListAPIKeys()); got != keysBefore {
		t.Fatalf("wrong methods changed API keys: got %d, want %d", got, keysBefore)
	}
	if got := len(store.ListApprovalRequests()); got != approvalsBefore {
		t.Fatalf("wrong methods changed approvals: got %d, want %d", got, approvalsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong methods changed audit events: got %d, want %d", got, auditsBefore)
	}
	if got := adminProjectTeamCount(t, store, projectID); got != linksBefore {
		t.Fatalf("wrong methods changed project teams: got %d, want %d", got, linksBefore)
	}
}

func TestAdminProjectMethodRoutesPreserveRoleChecksForReadAndWriteMethods(t *testing.T) {
	store, _, app, _, userToken, projectID := newAdminProjectMethodRoutingServer(t)
	teamLeaderToken := createAdminProjectMethodSession(t, store, AdminUser{
		Username: "project-method-matrix-team-leader",
		Name:     "Project Method Matrix Team Leader",
		Email:    "project-method-matrix-team-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_project_method",
		Status:   StatusActive,
	})
	securityToken := createAdminProjectMethodSession(t, store, AdminUser{
		Username: "project-method-matrix-security",
		Name:     "Project Method Matrix Security",
		Email:    "project-method-matrix-security@tokenhub.local",
		Role:     "security_admin",
		Status:   StatusActive,
	})

	tests := []struct {
		name       string
		token      string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "user project item read", token: userToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "user project item write", token: userToken, method: http.MethodPost, path: "/api/admin/projects/" + projectID, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "team leader project item read", token: teamLeaderToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "team leader project item write", token: teamLeaderToken, method: http.MethodPost, path: "/api/admin/projects/" + projectID, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "security project item read", token: securityToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "security project item write", token: securityToken, method: http.MethodPost, path: "/api/admin/projects/" + projectID, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "user team item read", token: userToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusForbidden, wantCode: "project_forbidden"},
		{name: "user team item write", token: userToken, method: http.MethodPost, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "team leader team item read", token: teamLeaderToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusForbidden, wantCode: "project_forbidden"},
		{name: "team leader team item write", token: teamLeaderToken, method: http.MethodPost, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "security team item read", token: securityToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "security team item write", token: securityToken, method: http.MethodPost, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "user quota read", token: userToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/quota-increase", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "user quota write", token: userToken, method: http.MethodPut, path: "/api/admin/projects/" + projectID + "/quota-increase", wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
		{name: "team leader quota read", token: teamLeaderToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/quota-increase", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "team leader quota write", token: teamLeaderToken, method: http.MethodPut, path: "/api/admin/projects/" + projectID + "/quota-increase", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "security quota read", token: securityToken, method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/quota-increase", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "security quota write", token: securityToken, method: http.MethodPut, path: "/api/admin/projects/" + projectID + "/quota-increase", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, test.token)
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestAdminProjectMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	store, _, app, adminToken, _, projectID := newAdminProjectMethodRoutingServer(t)
	projectsBefore := len(store.ListProjects())
	keysBefore := len(store.ListAPIKeys())
	approvalsBefore := len(store.ListApprovalRequests())
	auditsBefore := len(store.ListAuditEvents())
	linksBefore := adminProjectTeamCount(t, store, projectID)

	for _, route := range adminProjectMethodRoutes(projectID) {
		for _, method := range route.unsupportedMethods() {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, adminToken)
				wantStatus := http.StatusMethodNotAllowed
				wantCode := "method_not_allowed"
				wantAllow := route.allow
				if route.name == "project team item" && method == http.MethodGet {
					wantStatus = http.StatusForbidden
					wantCode = "project_forbidden"
					wantAllow = ""
				}
				assertJSONError(t, response, wantStatus, wantCode)
				assertAllowHeader(t, response, wantAllow)
			})
		}
	}

	if got := len(store.ListProjects()); got != projectsBefore {
		t.Fatalf("unsupported methods changed projects: got %d, want %d", got, projectsBefore)
	}
	if got := len(store.ListAPIKeys()); got != keysBefore {
		t.Fatalf("unsupported methods changed API keys: got %d, want %d", got, keysBefore)
	}
	if got := len(store.ListApprovalRequests()); got != approvalsBefore {
		t.Fatalf("unsupported methods changed approvals: got %d, want %d", got, approvalsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("unsupported methods changed audit events: got %d, want %d", got, auditsBefore)
	}
	if got := adminProjectTeamCount(t, store, projectID); got != linksBefore {
		t.Fatalf("unsupported methods changed project teams: got %d, want %d", got, linksBefore)
	}
}

func TestAdminProjectRoutesRejectRealHEADAfterAuthorization(t *testing.T) {
	_, server, _, adminToken, userToken, projectID := newAdminProjectMethodRoutingServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	tests := []struct {
		name       string
		path       string
		allow      string
		userStatus int
		userAllow  string
	}{
		{name: "projects", path: "/api/admin/projects", allow: "GET, POST", userStatus: http.StatusForbidden},
		{name: "project item", path: "/api/admin/projects/" + projectID, allow: "PATCH, DELETE", userStatus: http.StatusForbidden},
		{name: "project keys", path: "/api/admin/projects/" + projectID + "/keys", allow: "GET, POST", userStatus: http.StatusMethodNotAllowed, userAllow: "GET, POST"},
		{name: "project teams", path: "/api/admin/projects/" + projectID + "/teams", allow: "GET, POST", userStatus: http.StatusForbidden},
		{name: "project team item", path: "/api/admin/projects/" + projectID + "/teams/team_shared", allow: "PATCH, DELETE", userStatus: http.StatusForbidden},
		{name: "quota increase", path: "/api/admin/projects/" + projectID + "/quota-increase", allow: http.MethodPost, userStatus: http.StatusForbidden},
	}

	for _, test := range tests {
		for _, credential := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: test.allow},
			{name: "ordinary_user", token: userToken, wantStatus: test.userStatus, wantAllow: test.userAllow},
		} {
			t.Run(test.name+"/"+credential.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, httpServer.URL+test.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if credential.token != "" {
					request.Header.Set("authorization", "Bearer "+credential.token)
				}
				response, err := httpServer.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				assertRealHEADResponse(t, response, credential.wantStatus, credential.wantAllow, "application/json", true)
			})
		}
	}
}

func TestAdminProjectMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, _, app, _, _, projectID := newAdminProjectMethodRoutingServer(t)
	for _, route := range adminProjectMethodRoutes(projectID) {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.wrongMethod)
			response := httptest.NewRecorder()

			app.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
				t.Fatalf("access-control-allow-methods = %q", got)
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestAdminProjectMethodRoutesPreservePathValidationOrder(t *testing.T) {
	_, _, app, adminToken, _, projectID := newAdminProjectMethodRoutingServer(t)
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "empty project id", method: http.MethodPost, path: "/api/admin/projects/", wantStatus: http.StatusBadRequest, wantCode: "project_required"},
		{name: "project trailing slash", method: http.MethodPost, path: "/api/admin/projects/" + projectID + "/", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "unknown child", method: http.MethodPost, path: "/api/admin/projects/" + projectID + "/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "encoded project separator", method: http.MethodPost, path: "/api/admin/projects/" + projectID + "%2Funknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "missing teams project", method: http.MethodPut, path: "/api/admin/projects/missing/teams", wantStatus: http.StatusNotFound, wantCode: "project_not_found"},
		{name: "team item GET keeps forbidden", method: http.MethodGet, path: "/api/admin/projects/" + projectID + "/teams/team_shared", wantStatus: http.StatusForbidden, wantCode: "project_forbidden"},
		{name: "encoded team separator", method: http.MethodPatch, path: "/api/admin/projects/" + projectID + "/teams/team%2Fshared", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "extra team segment", method: http.MethodPost, path: "/api/admin/projects/" + projectID + "/teams/team_shared/extra", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, adminToken)
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestAdminProjectMethodRoutesPreserveMissingProjectOrder(t *testing.T) {
	_, _, app, adminToken, _, _ := newAdminProjectMethodRoutingServer(t)
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "project item rejects method without lookup", method: http.MethodGet, path: "/api/admin/projects/missing", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "project keys reject method without lookup", method: http.MethodDelete, path: "/api/admin/projects/missing/keys", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "GET, POST"},
		{name: "quota increase rejects method without lookup", method: http.MethodGet, path: "/api/admin/projects/missing/quota-increase", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "project teams look up project before rejecting method", method: http.MethodPut, path: "/api/admin/projects/missing/teams", wantStatus: http.StatusNotFound, wantCode: "project_not_found"},
		{name: "project team item write looks up project before rejecting method", method: http.MethodPost, path: "/api/admin/projects/missing/teams/team_shared", wantStatus: http.StatusNotFound, wantCode: "project_not_found"},
		{name: "project team item read looks up project before special rejection", method: http.MethodGet, path: "/api/admin/projects/missing/teams/team_shared", wantStatus: http.StatusNotFound, wantCode: "project_not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, adminToken)
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestAdminProjectTeamItemRoutesPreserveTrimmedTeamID(t *testing.T) {
	store, _, app, adminToken, _, projectID := newAdminProjectMethodRoutingServer(t)
	path := "/api/admin/projects/" + projectID + "/teams/%20team_shared%20"

	updated := methodRoutingJSONRequest(t, app, http.MethodPatch, path, map[string]any{"role": "developer"}, adminToken)
	if updated.Code != http.StatusOK {
		t.Fatalf("team PATCH with surrounding spaces: expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	project, ok := store.GetProject(projectID)
	if !ok {
		t.Fatalf("project %s not found", projectID)
	}
	link, found := projectTeamByID(project.Teams, "team_shared")
	if !found || link.Role != "developer" {
		t.Fatalf("trimmed team link was not updated: %#v", project.Teams)
	}

	deleted := methodRoutingRequest(app, http.MethodDelete, path, adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("team DELETE with surrounding spaces: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	project, ok = store.GetProject(projectID)
	if !ok {
		t.Fatalf("project %s not found after team deletion", projectID)
	}
	if _, found := projectTeamByID(project.Teams, "team_shared"); found {
		t.Fatalf("trimmed team link still exists after DELETE: %#v", project.Teams)
	}

	blank := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/projects/"+projectID+"/teams/%20", map[string]any{"role": "viewer"}, adminToken)
	assertJSONError(t, blank, http.StatusBadRequest, "invalid_project_team")
	assertAllowHeader(t, blank, "")
}

func TestAdminProjectMethodRoutesPreserveProjectItemAndKeySuccess(t *testing.T) {
	store, _, app, adminToken, _, projectID := newAdminProjectMethodRoutingServer(t)

	updated := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/projects/"+projectID, map[string]any{
		"name":   "Updated Method Routing Project",
		"status": StatusActive,
	}, adminToken)
	if updated.Code != http.StatusOK {
		t.Fatalf("project PATCH: expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	var project Project
	if err := json.NewDecoder(updated.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	if project.ID != projectID || project.Name != "Updated Method Routing Project" {
		t.Fatalf("unexpected updated project: %#v", project)
	}

	keys := methodRoutingRequest(app, http.MethodGet, "/api/admin/projects/"+projectID+"/keys", adminToken)
	if keys.Code != http.StatusOK {
		t.Fatalf("project keys GET: expected 200, got %d: %s", keys.Code, keys.Body.String())
	}
	assertAllowHeader(t, keys, "")

	deletedProject := store.CreateProject(Project{Name: "Delete Method Routing Project", Status: StatusActive})
	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/projects/"+deletedProject.ID, adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("project DELETE: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if _, ok := store.GetProject(deletedProject.ID); ok {
		t.Fatalf("deleted project %s still exists", deletedProject.ID)
	}

	wantAudit := map[string]bool{"update": false, "delete": false}
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == "project" && (event.ResourceID == projectID || event.ResourceID == deletedProject.ID) {
			wantAudit[event.Action] = true
		}
	}
	for action, found := range wantAudit {
		if !found {
			t.Fatalf("missing project %s audit event", action)
		}
	}
}

func adminProjectMethodRoutes(projectID string) []adminProjectMethodRoute {
	return []adminProjectMethodRoute{
		{name: "projects", path: "/api/admin/projects", wrongMethod: http.MethodPut, allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST", userStatus: http.StatusForbidden, userCode: "admin_forbidden"},
		{name: "project item", path: "/api/admin/projects/" + projectID, wrongMethod: http.MethodGet, allowed: []string{http.MethodPatch, http.MethodDelete}, allow: "PATCH, DELETE", userStatus: http.StatusMethodNotAllowed, userCode: "method_not_allowed", userHasAllow: true},
		{name: "project keys", path: "/api/admin/projects/" + projectID + "/keys", wrongMethod: http.MethodDelete, allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST", userStatus: http.StatusMethodNotAllowed, userCode: "method_not_allowed", userHasAllow: true},
		{name: "project teams", path: "/api/admin/projects/" + projectID + "/teams", wrongMethod: http.MethodPut, allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST", userStatus: http.StatusForbidden, userCode: "admin_forbidden"},
		{name: "project team item", path: "/api/admin/projects/" + projectID + "/teams/team_shared", wrongMethod: http.MethodPost, allowed: []string{http.MethodPatch, http.MethodDelete}, allow: "PATCH, DELETE", userStatus: http.StatusForbidden, userCode: "admin_forbidden"},
		{name: "quota increase", path: "/api/admin/projects/" + projectID + "/quota-increase", wrongMethod: http.MethodGet, allowed: []string{http.MethodPost}, allow: http.MethodPost, userStatus: http.StatusForbidden, userCode: "admin_forbidden"},
	}
}

func (route adminProjectMethodRoute) unsupportedMethods() []string {
	allowed := map[string]bool{}
	for _, method := range route.allowed {
		allowed[method] = true
	}
	methods := make([]string, 0, 5-len(allowed))
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !allowed[method] {
			methods = append(methods, method)
		}
	}
	return methods
}

func newAdminProjectMethodRoutingServer(t *testing.T) (*GormStore, *Server, http.Handler, string, string, string) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "project-method-routing-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	store.CreateResource("teams", AdminResource{ID: "team_project_method", Name: "Project Method Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_shared", Name: "Shared Method Team", Status: StatusActive})
	project := store.CreateProject(Project{Name: "Method Routing Project", TeamID: "team_project_method", Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: project.ID, TeamID: "team_shared", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateAdminUser(AdminUser{
		Username: "project-method-user",
		Name:     "Project Method User",
		Email:    "project-method-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "project-method-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()
	adminToken, _ := loginMethodRoutingAdmin(t, app, config.BootstrapAdminPassword)
	return store, server, app, adminToken, userSession.Token, project.ID
}

func adminProjectTeamCount(t *testing.T, store *GormStore, projectID string) int64 {
	t.Helper()
	_, total, err := store.ListProjectTeams(projectID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func createAdminProjectMethodSession(t *testing.T, store *GormStore, user AdminUser) string {
	t.Helper()
	created, err := store.CreateAdminUser(user, "project-method-role-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(created.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}
