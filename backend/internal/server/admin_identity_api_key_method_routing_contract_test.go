package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminIdentityAPIKeyMethodRoute struct {
	name        string
	path        string
	wrongMethod string
	allow       string
	resource    string
}

func TestAdminIdentityAndAPIKeyMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, _, app, adminToken, userToken, teamLeaderToken, securityToken, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	messages := configureTestSMTPChannel(t, store)
	routes := adminIdentityAPIKeyMethodRoutes(targetUserID, keyID)
	usersBefore := len(store.ListAdminUsers())
	keysBefore := len(store.ListAPIKeys())
	auditsBefore := len(store.ListAuditEvents())
	approvalsBefore := len(store.ListApprovalRequests())
	policiesBefore := len(store.ListResources(routingPolicyResourceKind))
	resetTokensBefore := adminIdentityAPIKeyMethodModelCount(t, store, &AdminPasswordResetToken{})
	sessionsBefore := adminIdentityAPIKeyMethodModelCount(t, store, &AdminSession{})

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
			if route.resource == "api_key" {
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
				return
			}
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})

		t.Run(route.name+"/team_leader", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, teamLeaderToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})

		t.Run(route.name+"/security_admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, securityToken)
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
	}

	if got := len(store.ListAdminUsers()); got != usersBefore {
		t.Fatalf("wrong methods changed users: got %d, want %d", got, usersBefore)
	}
	if got := len(store.ListAPIKeys()); got != keysBefore {
		t.Fatalf("wrong methods changed API keys: got %d, want %d", got, keysBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong methods changed audit events: got %d, want %d", got, auditsBefore)
	}
	if got := len(store.ListApprovalRequests()); got != approvalsBefore {
		t.Fatalf("wrong methods changed approvals: got %d, want %d", got, approvalsBefore)
	}
	if got := len(store.ListResources(routingPolicyResourceKind)); got != policiesBefore {
		t.Fatalf("wrong methods changed routing policies: got %d, want %d", got, policiesBefore)
	}
	if got := adminIdentityAPIKeyMethodModelCount(t, store, &AdminPasswordResetToken{}); got != resetTokensBefore {
		t.Fatalf("wrong methods changed reset tokens: got %d, want %d", got, resetTokensBefore)
	}
	if got := adminIdentityAPIKeyMethodModelCount(t, store, &AdminSession{}); got != sessionsBefore {
		t.Fatalf("wrong methods changed admin sessions: got %d, want %d", got, sessionsBefore)
	}
	select {
	case message := <-messages:
		t.Fatalf("wrong methods sent email: %s", message)
	default:
	}
}

func TestAdminIdentityAndAPIKeyMethodRoutesCoverUnsupportedStandardMethods(t *testing.T) {
	_, _, app, adminToken, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	for _, route := range adminIdentityAPIKeyMethodRoutes(targetUserID, keyID) {
		for _, method := range unsupportedAdminIdentityAPIKeyMethods(route.allow) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, adminToken)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
}

func TestAdminAPIKeyMethodRoutesPreserveResourceAuthorizationBefore405(t *testing.T) {
	store, _, app, adminToken, userToken, _, _, _, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	otherProject := store.CreateProject(Project{Name: "Other Method Routing Project", Status: StatusActive})
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "Other Method Routing Key", Status: StatusActive}, "thk_other_method_routing")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		path  string
		allow string
	}{
		{name: "item", path: "/api/admin/api-keys/" + otherKey.ID, allow: "PATCH, DELETE"},
		{name: "rotate", path: "/api/admin/api-keys/" + otherKey.ID + "/rotate", allow: http.MethodPost},
	} {
		t.Run(test.name+"/inaccessible", func(t *testing.T) {
			response := methodRoutingRequest(app, http.MethodGet, test.path, userToken)
			assertJSONError(t, response, http.StatusForbidden, "api_key_forbidden")
			assertAllowHeader(t, response, "")
		})
		t.Run(test.name+"/admin_missing", func(t *testing.T) {
			path := "/api/admin/api-keys/missing"
			if test.name == "rotate" {
				path += "/rotate"
			}
			response := methodRoutingRequest(app, http.MethodGet, path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, test.allow)
		})
	}

	accessible := methodRoutingRequest(app, http.MethodGet, "/api/admin/api-keys/"+keyID, userToken)
	assertJSONError(t, accessible, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, accessible, "PATCH, DELETE")
}

func TestAdminIdentityAndAPIKeyMethodRoutesPreservePathBoundaries(t *testing.T) {
	_, _, app, adminToken, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "empty user id", method: http.MethodGet, path: "/api/admin/users/", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "user trailing slash", method: http.MethodGet, path: "/api/admin/users/" + targetUserID + "/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "unknown user action", method: http.MethodGet, path: "/api/admin/users/" + targetUserID + "/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "reset wrong method", method: http.MethodGet, path: "/api/admin/users/" + targetUserID + "/reset-password-email", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "import wrong dynamic method", method: http.MethodPatch, path: "/api/admin/users/import", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "import trailing slash stays user item", method: http.MethodGet, path: "/api/admin/users/import/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "encoded user separator", method: http.MethodGet, path: "/api/admin/users/" + targetUserID + "%2Funknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "empty key id", method: http.MethodGet, path: "/api/admin/api-keys/", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "key trailing slash", method: http.MethodGet, path: "/api/admin/api-keys/" + keyID + "/", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: "PATCH, DELETE"},
		{name: "unknown key action", method: http.MethodGet, path: "/api/admin/api-keys/" + keyID + "/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "rotate wrong method", method: http.MethodGet, path: "/api/admin/api-keys/" + keyID + "/rotate", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "encoded key separator", method: http.MethodGet, path: "/api/admin/api-keys/" + keyID + "%2Funknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "encoded reset user separator", method: http.MethodGet, path: "/api/admin/users/" + targetUserID + "%2Funknown/reset-password-email", wantStatus: http.StatusNotFound, wantCode: "not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, adminToken)
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestAdminIdentityAndAPIKeyMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, _, app, _, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	for _, route := range adminIdentityAPIKeyMethodRoutes(targetUserID, keyID) {
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

func TestAdminIdentityAndAPIKeyMethodRoutesRejectRealHEAD(t *testing.T) {
	_, _, app, adminToken, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminIdentityAPIKeyMethodRoutes(targetUserID, keyID) {
		t.Run(route.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("authorization", "Bearer "+adminToken)
			response, err := httpServer.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, route.allow, "application/json", true)
		})
	}
}

func TestAdminIdentityAndAPIKeyMethodRoutesPreserveSuccessHandlers(t *testing.T) {
	store, _, app, adminToken, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)

	users := methodRoutingRequest(app, http.MethodGet, "/api/admin/users", adminToken)
	if users.Code != http.StatusOK {
		t.Fatalf("users GET: expected 200, got %d: %s", users.Code, users.Body.String())
	}
	createdUser := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/users", map[string]any{
		"username": "created-method-user", "email": "created-method-user@tokenhub.local", "role": "user", "password": "created-method-password",
	}, adminToken)
	if createdUser.Code != http.StatusCreated {
		t.Fatalf("users POST: expected 201, got %d: %s", createdUser.Code, createdUser.Body.String())
	}
	updatedUser := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/users/"+targetUserID, map[string]any{"name": "Updated Method User"}, adminToken)
	if updatedUser.Code != http.StatusOK {
		t.Fatalf("user PATCH: expected 200, got %d: %s", updatedUser.Code, updatedUser.Body.String())
	}

	keys := methodRoutingRequest(app, http.MethodGet, "/api/admin/api-keys", adminToken)
	if keys.Code != http.StatusOK {
		t.Fatalf("API keys GET: expected 200, got %d: %s", keys.Code, keys.Body.String())
	}
	updatedKey := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/api-keys/"+keyID, map[string]any{"name": "Updated Method Key"}, adminToken)
	if updatedKey.Code != http.StatusOK {
		t.Fatalf("API key PATCH: expected 200, got %d: %s", updatedKey.Code, updatedKey.Body.String())
	}
	rotatedKey := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/api-keys/"+keyID+"/rotate", map[string]any{}, adminToken)
	if rotatedKey.Code != http.StatusCreated {
		t.Fatalf("API key rotate: expected 201, got %d: %s", rotatedKey.Code, rotatedKey.Body.String())
	}
	var rotated struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(rotatedKey.Body).Decode(&rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.ID == "" || rotated.APIKey == "" {
		t.Fatalf("incomplete rotate response: %#v", rotated)
	}

	deleteUser, err := store.CreateAdminUser(AdminUser{Username: "delete-method-user", Email: "delete-method-user@tokenhub.local", Role: "user", Status: StatusActive}, "delete-method-password")
	if err != nil {
		t.Fatal(err)
	}
	deletedUser := methodRoutingRequest(app, http.MethodDelete, "/api/admin/users/"+deleteUser.ID, adminToken)
	if deletedUser.Code != http.StatusNoContent {
		t.Fatalf("user DELETE: expected 204, got %d: %s", deletedUser.Code, deletedUser.Body.String())
	}
	deleteKey, _, err := store.CreateAPIKey(defaultProjectID, APIKey{Name: "Delete Method Key", Status: StatusActive}, "thk_delete_method")
	if err != nil {
		t.Fatal(err)
	}
	deletedKey := methodRoutingRequest(app, http.MethodDelete, "/api/admin/api-keys/"+deleteKey.ID, adminToken)
	if deletedKey.Code != http.StatusNoContent {
		t.Fatalf("API key DELETE: expected 204, got %d: %s", deletedKey.Code, deletedKey.Body.String())
	}

	wantAudit := map[string]bool{"create/admin_user": false, "update/admin_user": false, "delete/admin_user": false, "update/api_key": false, "delete/api_key": false, "rotate/api_key": false}
	for _, event := range store.ListAuditEvents() {
		key := event.Action + "/" + event.ResourceType
		if _, ok := wantAudit[key]; ok {
			wantAudit[key] = true
		}
	}
	for event, found := range wantAudit {
		if !found {
			t.Fatalf("missing %s audit event", event)
		}
	}
}

func TestAdminUserMethodRoutesPreserveTeamLeaderScope(t *testing.T) {
	store, _, app, _, _, teamLeaderToken, _, targetUserID, _ := newAdminIdentityAPIKeyMethodRoutingServer(t)
	messages := configureTestSMTPChannel(t, store)
	teamUser, err := store.CreateAdminUser(AdminUser{
		Username: "leader-team-method-user", Email: "leader-team-method-user@tokenhub.local", Role: "user", TeamID: "team_platform", Status: StatusActive,
	}, "leader-team-method-password")
	if err != nil {
		t.Fatal(err)
	}
	teamAdmin, err := store.CreateAdminUser(AdminUser{
		Username: "leader-team-method-admin", Email: "leader-team-method-admin@tokenhub.local", Role: "admin", TeamID: "team_platform", Status: StatusActive,
	}, "leader-team-admin-password")
	if err != nil {
		t.Fatal(err)
	}

	created := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/users", map[string]any{
		"username": "leader-created-method-user", "email": "leader-created-method-user@tokenhub.local", "role": "user", "team_id": "team_platform", "password": "leader-created-password",
	}, teamLeaderToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("team leader user create: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var createdUser AdminUser
	if err := json.NewDecoder(created.Body).Decode(&createdUser); err != nil {
		t.Fatal(err)
	}
	if createdUser.TeamID != "team_platform" || normalizeAdminRole(createdUser.Role) != "user" {
		t.Fatalf("team leader created user outside enforced scope: %#v", createdUser)
	}

	roleCreate := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/users", map[string]any{
		"username": "leader-created-method-admin", "email": "leader-created-method-admin@tokenhub.local", "role": "admin", "password": "leader-created-admin-password",
	}, teamLeaderToken)
	assertJSONError(t, roleCreate, http.StatusForbidden, "role_forbidden")
	assertAllowHeader(t, roleCreate, "")

	teamCreate := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/users", map[string]any{
		"username": "leader-created-other-team", "email": "leader-created-other-team@tokenhub.local", "role": "user", "team_id": "team_other", "password": "leader-created-other-password",
	}, teamLeaderToken)
	assertJSONError(t, teamCreate, http.StatusForbidden, "team_forbidden")
	assertAllowHeader(t, teamCreate, "")

	updated := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/users/"+teamUser.ID, map[string]any{"name": "Leader Updated Method User"}, teamLeaderToken)
	if updated.Code != http.StatusOK {
		t.Fatalf("team leader user update: expected 200, got %d: %s", updated.Code, updated.Body.String())
	}
	outsideUpdate := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/users/"+targetUserID, map[string]any{"name": "Forbidden Update"}, teamLeaderToken)
	assertJSONError(t, outsideUpdate, http.StatusForbidden, "team_forbidden")
	roleUpdate := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/users/"+teamUser.ID, map[string]any{"role": "admin"}, teamLeaderToken)
	assertJSONError(t, roleUpdate, http.StatusForbidden, "role_forbidden")

	resetRequest := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+teamUser.ID+"/reset-password-email", nil)
	resetRequest.Header.Set("authorization", "Bearer "+teamLeaderToken)
	resetRequest.Header.Set("x-forwarded-proto", "https")
	resetRequest.Header.Set("x-forwarded-host", "console.tokenhub.example")
	resetResponse := httptest.NewRecorder()
	app.ServeHTTP(resetResponse, resetRequest)
	if resetResponse.Code != http.StatusOK {
		t.Fatalf("team leader reset email: expected 200, got %d: %s", resetResponse.Code, resetResponse.Body.String())
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "To: "+teamUser.Email) || !strings.Contains(message, "https://console.tokenhub.example/?reset_token=") {
			t.Fatalf("reset email did not preserve forwarded headers: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for team leader reset email")
	}
	outsideReset := methodRoutingRequest(app, http.MethodPost, "/api/admin/users/"+targetUserID+"/reset-password-email", teamLeaderToken)
	assertJSONError(t, outsideReset, http.StatusForbidden, "team_forbidden")

	adminDelete := methodRoutingRequest(app, http.MethodDelete, "/api/admin/users/"+teamAdmin.ID, teamLeaderToken)
	assertJSONError(t, adminDelete, http.StatusForbidden, "team_forbidden")
	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/users/"+createdUser.ID, teamLeaderToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("team leader user delete: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}

	imported := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source": "team_leader_method_route_test",
		"users": []map[string]any{{
			"username": "leader-imported-method-user", "email": "leader-imported-method-user@tokenhub.local", "role": "user", "team_id": "team_other", "status": StatusActive,
		}},
	}, teamLeaderToken)
	if imported.Code != http.StatusOK {
		t.Fatalf("team leader user import: expected 200, got %d: %s", imported.Code, imported.Body.String())
	}
	assertPasswordResetEmail(t, messages, "leader-imported-method-user@tokenhub.local")
	var importedUser AdminUser
	for _, user := range store.ListAdminUsers() {
		if user.Email == "leader-imported-method-user@tokenhub.local" {
			importedUser = user
			break
		}
	}
	if importedUser.ID == "" || importedUser.TeamID != "team_platform" || normalizeAdminRole(importedUser.Role) != "user" {
		t.Fatalf("team leader import escaped enforced scope: %#v", importedUser)
	}
}

func TestAdminIdentityAndAPIKeyMethodRoutesPreserveSpecialPostHandlers(t *testing.T) {
	store, _, app, adminToken, userToken, _, _, targetUserID, _ := newAdminIdentityAPIKeyMethodRoutingServer(t)
	messages := configureTestSMTPChannel(t, store)

	imported := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/users/import", map[string]any{
		"source": "method_route_test",
		"format": "json",
		"users": []map[string]any{{
			"username": "imported-method-user", "email": "imported-method-user@tokenhub.local", "role": "user", "status": StatusActive,
		}},
	}, adminToken)
	if imported.Code != http.StatusOK {
		t.Fatalf("users import POST: expected 200, got %d: %s", imported.Code, imported.Body.String())
	}
	assertPasswordResetEmail(t, messages, "imported-method-user@tokenhub.local")

	reset := methodRoutingRequest(app, http.MethodPost, "/api/admin/users/"+targetUserID+"/reset-password-email", adminToken)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset password email POST: expected 200, got %d: %s", reset.Code, reset.Body.String())
	}
	assertPasswordResetEmail(t, messages, "target-method-user@tokenhub.local")

	createdKey := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/api-keys", map[string]any{"name": "Created Method Key"}, userToken)
	if createdKey.Code != http.StatusCreated {
		t.Fatalf("API keys POST: expected 201, got %d: %s", createdKey.Code, createdKey.Body.String())
	}
	var keyPayload struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(createdKey.Body).Decode(&keyPayload); err != nil {
		t.Fatal(err)
	}
	if keyPayload.ID == "" || keyPayload.APIKey == "" {
		t.Fatalf("incomplete API key create response: %#v", keyPayload)
	}

	wantAudit := map[string]bool{"import/admin_user": false, "send_reset_password_email/admin_user": false, "create/api_key": false}
	for _, event := range store.ListAuditEvents() {
		key := event.Action + "/" + event.ResourceType
		if _, ok := wantAudit[key]; ok {
			wantAudit[key] = true
		}
	}
	for event, found := range wantAudit {
		if !found {
			t.Fatalf("missing %s audit event", event)
		}
	}
}

func TestAdminIdentityAndAPIKeyMethodRoutesPreserveTrailingSlashPostHandlers(t *testing.T) {
	store, _, app, adminToken, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	messages := configureTestSMTPChannel(t, store)

	reset := methodRoutingRequest(app, http.MethodPost, "/api/admin/users/"+targetUserID+"/reset-password-email/", adminToken)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset password email with trailing slash: expected 200, got %d: %s", reset.Code, reset.Body.String())
	}
	assertPasswordResetEmail(t, messages, "target-method-user@tokenhub.local")

	rotated := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/api-keys/"+keyID+"/rotate/", map[string]any{}, adminToken)
	if rotated.Code != http.StatusCreated {
		t.Fatalf("API key rotate with trailing slash: expected 201, got %d: %s", rotated.Code, rotated.Body.String())
	}
	var payload struct {
		ID     string `json:"id"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(rotated.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID == "" || payload.APIKey == "" {
		t.Fatalf("incomplete trailing-slash rotate response: %#v", payload)
	}
}

func TestAdminIdentityAndAPIKeyMethodRoutesRejectEncodedPathSeparatorsWithoutSideEffects(t *testing.T) {
	store, _, app, adminToken, _, _, _, targetUserID, keyID := newAdminIdentityAPIKeyMethodRoutingServer(t)
	messages := configureTestSMTPChannel(t, store)
	usersBefore := len(store.ListAdminUsers())
	keysBefore := len(store.ListAPIKeys())
	auditsBefore := len(store.ListAuditEvents())
	resetTokensBefore := adminIdentityAPIKeyMethodModelCount(t, store, &AdminPasswordResetToken{})

	for _, test := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "user patch", method: http.MethodPatch, path: "/api/admin/users/" + targetUserID + "%2Funknown"},
		{name: "user reset", method: http.MethodPost, path: "/api/admin/users/" + targetUserID + "%2Funknown/reset-password-email"},
		{name: "API key patch", method: http.MethodPatch, path: "/api/admin/api-keys/" + keyID + "%2Funknown"},
		{name: "API key rotate", method: http.MethodPost, path: "/api/admin/api-keys/" + keyID + "%2Funknown/rotate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, adminToken)
			assertJSONError(t, response, http.StatusNotFound, "not_found")
			assertAllowHeader(t, response, "")
		})
	}

	if got := len(store.ListAdminUsers()); got != usersBefore {
		t.Fatalf("encoded paths changed users: got %d, want %d", got, usersBefore)
	}
	if got := len(store.ListAPIKeys()); got != keysBefore {
		t.Fatalf("encoded paths changed API keys: got %d, want %d", got, keysBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("encoded paths changed audit events: got %d, want %d", got, auditsBefore)
	}
	if got := adminIdentityAPIKeyMethodModelCount(t, store, &AdminPasswordResetToken{}); got != resetTokensBefore {
		t.Fatalf("encoded paths changed reset tokens: got %d, want %d", got, resetTokensBefore)
	}
	select {
	case message := <-messages:
		t.Fatalf("encoded paths sent email: %s", message)
	default:
	}
}

func adminIdentityAPIKeyMethodRoutes(userID string, keyID string) []adminIdentityAPIKeyMethodRoute {
	return []adminIdentityAPIKeyMethodRoute{
		{name: "users", path: "/api/admin/users", wrongMethod: http.MethodPut, allow: "GET, POST", resource: "identity"},
		{name: "users import", path: "/api/admin/users/import", wrongMethod: http.MethodGet, allow: http.MethodPost, resource: "identity"},
		{name: "user item", path: "/api/admin/users/" + userID, wrongMethod: http.MethodGet, allow: "PATCH, DELETE", resource: "identity"},
		{name: "user reset email", path: "/api/admin/users/" + userID + "/reset-password-email", wrongMethod: http.MethodGet, allow: http.MethodPost, resource: "identity"},
		{name: "API keys", path: "/api/admin/api-keys", wrongMethod: http.MethodPut, allow: "GET, POST", resource: "api_key"},
		{name: "API key item", path: "/api/admin/api-keys/" + keyID, wrongMethod: http.MethodGet, allow: "PATCH, DELETE", resource: "api_key"},
		{name: "API key rotate", path: "/api/admin/api-keys/" + keyID + "/rotate", wrongMethod: http.MethodGet, allow: http.MethodPost, resource: "api_key"},
	}
}

func unsupportedAdminIdentityAPIKeyMethods(allow string) []string {
	allowed := map[string]bool{}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if allow == method || allow == "GET, POST" && (method == http.MethodGet || method == http.MethodPost) || allow == "PATCH, DELETE" && (method == http.MethodPatch || method == http.MethodDelete) {
			allowed[method] = true
		}
	}
	methods := []string{}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !allowed[method] {
			methods = append(methods, method)
		}
	}
	return methods
}

func newAdminIdentityAPIKeyMethodRoutingServer(t *testing.T) (*GormStore, *Server, http.Handler, string, string, string, string, string, string) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "identity-api-key-method-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateAdminUser(AdminUser{Username: "target-method-user", Email: "target-method-user@tokenhub.local", Role: "user", Status: StatusActive}, "target-method-password")
	if err != nil {
		t.Fatal(err)
	}
	ordinary, ordinaryToken := createAdminIdentityAPIKeyMethodSession(t, store, AdminUser{Username: "ordinary-method-user", Email: "ordinary-method-user@tokenhub.local", Role: "user", Status: StatusActive})
	_, teamLeaderToken := createAdminIdentityAPIKeyMethodSession(t, store, AdminUser{Username: "leader-method-user", Email: "leader-method-user@tokenhub.local", Role: "team_leader", TeamID: "team_platform", Status: StatusActive})
	_, securityToken := createAdminIdentityAPIKeyMethodSession(t, store, AdminUser{Username: "security-method-user", Email: "security-method-user@tokenhub.local", Role: "security_admin", Status: StatusActive})
	key, _, err := store.CreateAPIKey(defaultProjectID, APIKey{Name: "Owned Method Routing Key", OwnerUserID: ordinary.ID, Status: StatusActive}, "thk_owned_method_routing")
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()
	adminToken, _ := loginMethodRoutingAdmin(t, app, config.BootstrapAdminPassword)
	return store, server, app, adminToken, ordinaryToken, teamLeaderToken, securityToken, target.ID, key.ID
}

func createAdminIdentityAPIKeyMethodSession(t *testing.T, store *GormStore, user AdminUser) (AdminUser, string) {
	t.Helper()
	created, err := store.CreateAdminUser(user, "identity-api-key-role-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(created.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return created, session.Token
}

func adminIdentityAPIKeyMethodModelCount(t *testing.T, store *GormStore, model any) int64 {
	t.Helper()
	var count int64
	if err := store.db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
