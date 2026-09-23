package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestAssignedAPIKeyOwnerCannotManageAdminIssuedKey(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "assigned-key-admin",
		Name:     "Assigned Key Admin",
		Email:    "assigned-key-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	sessionToken := func(userID string) string {
		t.Helper()
		_, session, err := store.CreateAdminSession(userID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return session.Token
	}
	viewer, err := store.CreateAdminUser(AdminUser{
		Username: "assigned-key-viewer",
		Name:     "Assigned Key Viewer",
		Email:    "assigned-key-viewer@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "viewer123456")
	if err != nil {
		t.Fatal(err)
	}
	creator, err := store.CreateAdminUser(AdminUser{
		Username: "self-key-creator",
		Name:     "Self Key Creator",
		Email:    "self-key-creator@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "creator123456")
	if err != nil {
		t.Fatal(err)
	}
	maintainer, err := store.CreateAdminUser(AdminUser{
		Username: "key-maintainer",
		Name:     "Key Maintainer",
		Email:    "key-maintainer@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "maintainer123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Assigned Key Project", Status: StatusActive})
	store.CreateResource("project-members", AdminResource{
		Name:   "Assigned Viewer Membership",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": project.ID,
			"user_id":    viewer.ID,
			"role":       "viewer",
		},
	})
	store.CreateResource("project-members", AdminResource{
		Name:   "Creator Developer Membership",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": project.ID,
			"user_id":    creator.ID,
			"role":       "developer",
		},
	})
	store.CreateResource("project-members", AdminResource{
		Name:   "Maintainer Membership",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": project.ID,
			"user_id":    maintainer.ID,
			"role":       "maintainer",
		},
	})
	viewerToken := sessionToken(viewer.ID)
	creatorToken := sessionToken(creator.ID)
	maintainerToken := sessionToken(maintainer.ID)
	server := New(store)
	app := server.Handler()

	issued := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{
		"name":          "Admin Issued Viewer Key",
		"owner_user_id": viewer.ID,
		"limits": map[string]any{
			"daily_requests": 100,
		},
	}, "")
	if issued.Code != http.StatusCreated {
		t.Fatalf("admin key create failed: %d %s", issued.Code, issued.Body)
	}
	var adminKey struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(issued.Body), &adminKey); err != nil {
		t.Fatal(err)
	}

	listed := doJSON(t, app, http.MethodGet, "/api/admin/api-keys", nil, viewerToken)
	if listed.Code != http.StatusOK {
		t.Fatalf("viewer list keys failed: %d %s", listed.Code, listed.Body)
	}
	var listedPayload struct {
		Data []APIKey `json:"data"`
	}
	if err := json.Unmarshal([]byte(listed.Body), &listedPayload); err != nil {
		t.Fatal(err)
	}
	if !apiKeyPayloadContainsID(listedPayload.Data, adminKey.ID) {
		t.Fatalf("assigned viewer cannot see admin-issued key: %#v", listedPayload.Data)
	}

	usage := doJSON(t, app, http.MethodGet, "/api/admin/api-keys/"+adminKey.ID+"/usage", nil, viewerToken)
	if usage.Code != http.StatusOK {
		t.Fatalf("assigned viewer should read admin-issued key usage, got %d: %s", usage.Code, usage.Body)
	}
	logs := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests?api_key_id="+adminKey.ID, nil, viewerToken)
	if logs.Code != http.StatusOK {
		t.Fatalf("assigned viewer should filter request logs for admin-issued key, got %d: %s", logs.Code, logs.Body)
	}

	for _, testCase := range []struct {
		name    string
		method  string
		path    string
		payload any
	}{
		{
			name:   "limits patch",
			method: http.MethodPatch,
			path:   "/api/admin/api-keys/" + adminKey.ID,
			payload: map[string]any{
				"limits": map[string]any{"daily_requests": 10000},
			},
		},
		{
			name:   "security patch",
			method: http.MethodPatch,
			path:   "/api/admin/api-keys/" + adminKey.ID,
			payload: map[string]any{
				"model_access_mode": "restricted",
				"allowed_models":    []string{"gpt-4.1"},
				"ip_allowlist":      []string{"203.0.113.10/32"},
			},
		},
		{
			name:    "rotate",
			method:  http.MethodPost,
			path:    "/api/admin/api-keys/" + adminKey.ID + "/rotate",
			payload: map[string]any{},
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			path:   "/api/admin/api-keys/" + adminKey.ID,
		},
	} {
		resp := doJSON(t, app, testCase.method, testCase.path, testCase.payload, viewerToken)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("assigned viewer %s: expected 403, got %d: %s", testCase.name, resp.Code, resp.Body)
		}
	}

	adminPatch := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+adminKey.ID, map[string]any{
		"name": "Admin Updated Viewer Key",
	}, "")
	if adminPatch.Code != http.StatusOK {
		t.Fatalf("platform admin should manage admin-issued key, got %d: %s", adminPatch.Code, adminPatch.Body)
	}

	maintainerPatch := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+adminKey.ID, map[string]any{
		"name": "Maintainer Updated Viewer Key",
	}, maintainerToken)
	if maintainerPatch.Code != http.StatusOK {
		t.Fatalf("project maintainer should manage admin-issued key, got %d: %s", maintainerPatch.Code, maintainerPatch.Body)
	}

	selfIssued := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/keys", map[string]any{
		"name": "Self Issued Key",
	}, creatorToken)
	if selfIssued.Code != http.StatusCreated {
		t.Fatalf("creator key create failed: %d %s", selfIssued.Code, selfIssued.Body)
	}
	var selfKey struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(selfIssued.Body), &selfKey); err != nil {
		t.Fatal(err)
	}
	selfPatch := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+selfKey.ID, map[string]any{
		"name": "Creator Updated Own Key",
	}, creatorToken)
	if selfPatch.Code != http.StatusOK {
		t.Fatalf("creator should patch own key, got %d: %s", selfPatch.Code, selfPatch.Body)
	}
	selfRotate := doJSON(t, app, http.MethodPost, "/api/admin/api-keys/"+selfKey.ID+"/rotate", map[string]any{}, creatorToken)
	if selfRotate.Code != http.StatusCreated {
		t.Fatalf("creator should rotate own key, got %d: %s", selfRotate.Code, selfRotate.Body)
	}
	selfDelete := doJSON(t, app, http.MethodDelete, "/api/admin/api-keys/"+selfKey.ID, nil, creatorToken)
	if selfDelete.Code != http.StatusNoContent {
		t.Fatalf("creator should delete own key, got %d: %s", selfDelete.Code, selfDelete.Body)
	}

	legacyKey, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Legacy Assigned Key", OwnerUserID: viewer.ID, Status: StatusActive}, "thk_legacy_assigned")
	if err != nil {
		t.Fatal(err)
	}
	legacyPatch := doJSON(t, app, http.MethodPatch, "/api/admin/api-keys/"+legacyKey.ID, map[string]any{
		"name": "Viewer Updated Legacy Key",
	}, viewerToken)
	if legacyPatch.Code != http.StatusForbidden {
		t.Fatalf("legacy assigned key should fail closed for viewer mutation, got %d: %s", legacyPatch.Code, legacyPatch.Body)
	}
}

func apiKeyPayloadContainsID(keys []APIKey, id string) bool {
	for _, key := range keys {
		if key.ID == id {
			return true
		}
	}
	return false
}

func TestOrdinaryUsersCanReadOnlyOwnActiveTeamsForAPIKeyConsoleAuthz(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("teams", AdminResource{ID: "team_active_authz", Name: "Active Authz Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_disabled_authz", Name: "Disabled Authz Team", Status: StatusActive})
	store.CreateResource("teams", AdminResource{ID: "team_other_authz", Name: "Other Authz Team", Status: StatusActive})
	user, err := store.CreateAdminUser(AdminUser{
		Username: "team-authz-user",
		Email:    "team-authz-user@tokenhub.local",
		Role:     "user",
		TeamID:   "team_active_authz",
		TeamIDs:  []string{"team_disabled_authz"},
		Status:   StatusActive,
	}, "team123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateResource("teams", "team_disabled_authz", AdminResource{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	listed := doJSON(t, app, http.MethodGet, "/api/admin/resources/teams", nil, session.Token)
	if listed.Code != http.StatusOK {
		t.Fatalf("ordinary user should read own active teams, got %d: %s", listed.Code, listed.Body)
	}
	var payload struct {
		Data []AdminResource `json:"data"`
	}
	if err := json.Unmarshal([]byte(listed.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "team_active_authz" {
		t.Fatalf("ordinary user team resource scope = %#v, want only active own team", payload.Data)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/teams", map[string]any{"name": "Forbidden Team"}, session.Token)
	if created.Code != http.StatusForbidden {
		t.Fatalf("ordinary user should not create teams, got %d: %s", created.Code, created.Body)
	}
}
