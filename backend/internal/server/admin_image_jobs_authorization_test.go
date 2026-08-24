package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAdminImageJobAuditScopesJobsAndAssetsByUserAccess(t *testing.T) {
	store := NewMemoryStore()
	owner, err := store.CreateAdminUser(AdminUser{
		Username: "image-audit-owner",
		Email:    "image-audit-owner@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "owner-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAdminUser(AdminUser{
		Username: "image-audit-other",
		Email:    "image-audit-other@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "other-password")
	if err != nil {
		t.Fatal(err)
	}
	noAccess, err := store.CreateAdminUser(AdminUser{
		Username: "image-audit-no-access",
		Email:    "image-audit-no-access@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "no-access-password")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource("teams", AdminResource{ID: "team_image_audit", Name: "Image audit team", Status: StatusActive})
	teamLeader, err := store.CreateAdminUser(AdminUser{
		Username: "image-audit-team-leader",
		Email:    "image-audit-team-leader@tokenhub.local",
		Role:     "team_leader",
		TeamID:   "team_image_audit",
		Status:   StatusActive,
	}, "team-leader-password")
	if err != nil {
		t.Fatal(err)
	}
	security, err := store.CreateAdminUser(AdminUser{
		Username: "image-audit-security",
		Email:    "image-audit-security@tokenhub.local",
		Role:     "security_admin",
		Status:   StatusActive,
	}, "security-password")
	if err != nil {
		t.Fatal(err)
	}
	platform, err := store.CreateAdminUser(AdminUser{
		Username: "image-audit-platform",
		Email:    "image-audit-platform@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "platform-password")
	if err != nil {
		t.Fatal(err)
	}

	ownerProject := store.CreateProject(Project{Name: "Owner image project", OwnerUserID: owner.ID, Status: StatusActive})
	otherProject := store.CreateProject(Project{Name: "Other image project", OwnerUserID: other.ID, Status: StatusActive})
	teamProject := store.CreateProject(Project{Name: "Team leader image project", OwnerUserID: other.ID, Status: StatusActive})
	if _, err := store.AddProjectTeam(ProjectTeam{ProjectID: teamProject.ID, TeamID: teamLeader.TeamID, Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	ownerKey, _, err := store.CreateAPIKey(ownerProject.ID, APIKey{Name: "owner-image-key", OwnerUserID: owner.ID, Status: StatusActive}, "thk_image_owner")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(otherProject.ID, APIKey{Name: "other-image-key", OwnerUserID: other.ID, Status: StatusActive}, "thk_image_other")
	if err != nil {
		t.Fatal(err)
	}
	teamKey, _, err := store.CreateAPIKey(teamProject.ID, APIKey{Name: "team-image-key", OwnerUserID: other.ID, Status: StatusActive}, "thk_image_team")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	ownerJob, err := store.CreateImageJob(ImageJob{
		ID: "imgjob_owner_visible", ProjectID: ownerProject.ID, APIKeyID: ownerKey.ID,
		Status: imageJobStatusCompleted, Model: openAIImageModelName, Action: "generate", CreatedAt: now.Add(-time.Minute),
	}, "owner-only prompt")
	if err != nil {
		t.Fatal(err)
	}
	otherJob, err := store.CreateImageJob(ImageJob{
		ID: "imgjob_other_hidden", ProjectID: otherProject.ID, APIKeyID: otherKey.ID,
		Status: imageJobStatusCompleted, Model: openAIImageModelName, Action: "generate", CreatedAt: now,
	}, "other-project secret prompt")
	if err != nil {
		t.Fatal(err)
	}
	teamJob, err := store.CreateImageJob(ImageJob{
		ID: "imgjob_team_visible", ProjectID: teamProject.ID, APIKeyID: teamKey.ID,
		Status: imageJobStatusCompleted, Model: openAIImageModelName, Action: "generate", CreatedAt: now.Add(-2 * time.Minute),
	}, "team-project prompt")
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range []ImageAsset{
		{ID: "asset_owner_visible", JobID: ownerJob.ID, ProjectID: ownerProject.ID, Role: "output", RelativePath: "owner/output.png", ContentType: "image/png", CreatedAt: now},
		{ID: "asset_other_hidden", JobID: otherJob.ID, ProjectID: otherProject.ID, Role: "output", RelativePath: "other/output.png", ContentType: "image/png", CreatedAt: now},
		{ID: "asset_team_visible", JobID: teamJob.ID, ProjectID: teamProject.ID, Role: "output", RelativePath: "team/output.png", ContentType: "image/png", CreatedAt: now},
	} {
		if _, err := store.CreateImageAsset(asset); err != nil {
			t.Fatal(err)
		}
	}

	_, ownerSession, err := store.CreateAdminSession(owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, noAccessSession, err := store.CreateAdminSession(noAccess.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, securitySession, err := store.CreateAdminSession(security.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, teamLeaderSession, err := store.CreateAdminSession(teamLeader.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, platformSession, err := store.CreateAdminSession(platform.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "image-audit-platform-token", SecretKey: "image-audit-secret"})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	app := server.Handler()

	ownerResponse := doJSON(t, app, http.MethodGet, "/api/admin/audit/image-jobs?limit=10", nil, ownerSession.Token)
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("owner image audit = %d: %s", ownerResponse.Code, ownerResponse.Body)
	}
	if !strings.Contains(ownerResponse.Body, ownerJob.ID) || !strings.Contains(ownerResponse.Body, "owner-only prompt") || !strings.Contains(ownerResponse.Body, "asset_owner_visible") {
		t.Fatalf("owner image audit omitted an accessible job or asset: %s", ownerResponse.Body)
	}
	for _, hidden := range []string{otherJob.ID, "other-project secret prompt", "asset_other_hidden", teamJob.ID, "team-project prompt", "asset_team_visible"} {
		if strings.Contains(ownerResponse.Body, hidden) {
			t.Fatalf("owner image audit leaked %q: %s", hidden, ownerResponse.Body)
		}
	}
	ownerLimitedResponse := doJSON(t, app, http.MethodGet, "/api/admin/audit/image-jobs?limit=1", nil, ownerSession.Token)
	if ownerLimitedResponse.Code != http.StatusOK || !strings.Contains(ownerLimitedResponse.Body, ownerJob.ID) {
		t.Fatalf("owner image audit applied limit before authorization scope: %d %s", ownerLimitedResponse.Code, ownerLimitedResponse.Body)
	}
	if strings.Contains(ownerLimitedResponse.Body, otherJob.ID) {
		t.Fatalf("limited owner image audit leaked a newer inaccessible job: %s", ownerLimitedResponse.Body)
	}

	noAccessResponse := doJSON(t, app, http.MethodGet, "/api/admin/audit/image-jobs?limit=10", nil, noAccessSession.Token)
	if noAccessResponse.Code != http.StatusOK || !strings.Contains(noAccessResponse.Body, `"data":[]`) {
		t.Fatalf("user without visible API keys should receive an empty image audit: %d %s", noAccessResponse.Code, noAccessResponse.Body)
	}
	for _, hidden := range []string{ownerJob.ID, otherJob.ID, teamJob.ID, "owner-only prompt", "other-project secret prompt", "team-project prompt"} {
		if strings.Contains(noAccessResponse.Body, hidden) {
			t.Fatalf("user without access leaked %q: %s", hidden, noAccessResponse.Body)
		}
	}

	teamLeaderResponse := doJSON(t, app, http.MethodGet, "/api/admin/audit/image-jobs?limit=10", nil, teamLeaderSession.Token)
	if teamLeaderResponse.Code != http.StatusOK {
		t.Fatalf("team leader image audit = %d: %s", teamLeaderResponse.Code, teamLeaderResponse.Body)
	}
	for _, visible := range []string{teamJob.ID, "team-project prompt", "asset_team_visible"} {
		if !strings.Contains(teamLeaderResponse.Body, visible) {
			t.Fatalf("team leader image audit omitted %q: %s", visible, teamLeaderResponse.Body)
		}
	}
	for _, hidden := range []string{ownerJob.ID, otherJob.ID, "owner-only prompt", "other-project secret prompt", "asset_owner_visible", "asset_other_hidden"} {
		if strings.Contains(teamLeaderResponse.Body, hidden) {
			t.Fatalf("team leader image audit leaked %q: %s", hidden, teamLeaderResponse.Body)
		}
	}

	for name, token := range map[string]string{
		"security_admin": securitySession.Token,
		"platform_admin": platformSession.Token,
	} {
		t.Run(name, func(t *testing.T) {
			response := doJSON(t, app, http.MethodGet, "/api/admin/audit/image-jobs?limit=10", nil, token)
			if response.Code != http.StatusOK {
				t.Fatalf("global image audit = %d: %s", response.Code, response.Body)
			}
			for _, visible := range []string{ownerJob.ID, otherJob.ID, teamJob.ID, "owner-only prompt", "other-project secret prompt", "team-project prompt", "asset_owner_visible", "asset_other_hidden", "asset_team_visible"} {
				if !strings.Contains(response.Body, visible) {
					t.Fatalf("global image audit omitted %q: %s", visible, response.Body)
				}
			}
		})
	}
}
