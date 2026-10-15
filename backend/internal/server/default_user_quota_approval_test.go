package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func setupDefaultUserQuotaApprovalTest(t *testing.T) (*GormStore, http.Handler, AdminUser, string, string) {
	t.Helper()
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	team := store.CreateResource("teams", AdminResource{Name: "Quota approval team", Status: StatusActive})
	var leader AdminUser
	tokens := map[string]string{}
	for _, role := range []string{"admin", "team_leader"} {
		user, err := store.CreateAdminUser(AdminUser{Username: "quota-approver-" + role, Email: "quota-approver-" + role + "@example.test", Role: role, TeamID: team.ID, Status: StatusActive}, "QuotaApprovalPass123!")
		if err != nil {
			t.Fatal(err)
		}
		_, session, err := store.CreateAdminSession(user.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = session.Token
		if role == "team_leader" {
			leader = user
		}
	}
	return store, New(store).Handler(), leader, tokens["admin"], tokens["team_leader"]
}

func TestDefaultUserQuotaApprovalsRequirePlatformAdministrator(t *testing.T) {
	for _, operation := range []string{"create", "update", "convert individual"} {
		t.Run(operation, func(t *testing.T) {
			store, app, leader, adminToken, leaderToken := setupDefaultUserQuotaApprovalTest(t)
			store.CreateResource("approval-flows", AdminResource{Name: "Team quota approvals", Status: StatusActive,
				Fields: map[string]any{"trigger": "quota_increase", "approver_role": "team_leader"}})
			path := "/api/admin/resources/quota-policies"
			method := http.MethodPost
			if operation != "create" {
				scopeID := allUsersQuotaScopeID
				if operation == "convert individual" {
					scopeID = leader.ID
				}
				policy := store.CreateResource("quota-policies", AdminResource{Name: "Existing quota", Status: StatusActive,
					Fields: map[string]any{"scope": "user", "scope_id": scopeID, "daily_tokens": 10}})
				path += "/" + policy.ID
				method = http.MethodPatch
			}
			response := doJSON(t, app, method, path, map[string]any{
				"name": "Default quota", "fields": map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "daily_tokens": 20},
			}, adminToken)
			if response.Code != http.StatusAccepted {
				t.Fatalf("request template approval: %d %s", response.Code, response.Body)
			}
			var pending struct {
				Approval ApprovalRequest `json:"approval"`
			}
			if err := json.Unmarshal([]byte(response.Body), &pending); err != nil {
				t.Fatal(err)
			}
			approvalPath := "/api/admin/approvals/" + pending.Approval.ID + "/approve"
			forbidden := doJSON(t, app, http.MethodPost, approvalPath, nil, leaderToken)
			if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body, "quota_forbidden") {
				t.Fatalf("leader must not apply a template approval: %d %s", forbidden.Code, forbidden.Body)
			}
			approved := doJSON(t, app, http.MethodPost, approvalPath, nil, adminToken)
			if approved.Code != http.StatusOK {
				t.Fatalf("platform administrator must retain approval access: %d %s", approved.Code, approved.Body)
			}
		})
	}
}

func TestDefaultUserQuotaCannotBeReplacedThroughProjectQuotaApproval(t *testing.T) {
	store, app, leader, adminToken, leaderToken := setupDefaultUserQuotaApprovalTest(t)
	template := store.CreateResource("quota-policies", AdminResource{Name: "Default quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": allUsersQuotaScopeID, "daily_tokens": 10}})
	project := store.CreateProject(Project{Name: "Project referencing default quota", TeamID: leader.TeamID,
		OwnerUserID: leader.ID, DefaultQuotaRef: template.ID, Status: StatusActive})
	response := doJSON(t, app, http.MethodPost, "/api/admin/projects/"+project.ID+"/quota-increase", map[string]any{
		"fields": map[string]any{"daily_tokens": 20},
	}, leaderToken)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body, "default_user_quota_project_conflict") {
		t.Fatalf("project quota increase must reject a template reference: %d %s", response.Code, response.Body)
	}
	if pending := store.ListApprovalRequests(); len(pending) != 0 {
		t.Fatalf("rejected project quota increase created approvals: %+v", pending)
	}
	// A request queued before the policy became a template must also be rejected.
	pending := store.CreateApprovalRequest(ApprovalRequest{
		Trigger: "quota_increase", ResourceType: "quota-policies", ResourceID: template.ID,
		RequesterID: leader.ID, Status: "pending", Payload: snapshotJSON(map[string]any{
			"project_id": project.ID, "name": "Project quota increase", "status": StatusActive,
			"fields": map[string]any{"scope": "project", "scope_id": project.ID, "daily_tokens": 20},
		}),
	})
	for role, token := range map[string]string{"admin": adminToken, "team_leader": leaderToken} {
		rejected := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+pending.ID+"/approve", nil, token)
		if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body, "default_user_quota_project_conflict") {
			t.Fatalf("%s project approval must not convert a default template: %d %s", role, rejected.Code, rejected.Body)
		}
	}
	if approval, err := store.GetApprovalRequest(pending.ID); err != nil || approval.Status != "pending" {
		t.Fatalf("rejected approval should remain pending: %+v err=%v", approval, err)
	}
	unchanged, err := New(store).findResource("quota-policies", template.ID)
	if err != nil || !isDefaultUserQuotaPolicy(unchanged.Fields) || int64Field(unchanged.Fields, "daily_tokens") != 10 {
		t.Fatalf("project approval changed the default template: %+v err=%v", unchanged, err)
	}
}

func TestDefaultUserQuotaApprovalRejectsPartialTargetChangeByTeamLeader(t *testing.T) {
	store, _, leader, _, _ := setupDefaultUserQuotaApprovalTest(t)
	policy := store.CreateResource("quota-policies", AdminResource{Name: "Individual quota", Status: StatusActive,
		Fields: map[string]any{"scope": "user", "scope_id": leader.ID, "daily_tokens": 10}})
	_, err := New(store).applyApprovalRequest(ApprovalRequest{
		Trigger: "quota_increase", ResourceType: "quota-policies", ResourceID: policy.ID, Status: "pending",
		Payload: snapshotJSON(map[string]any{"fields": map[string]any{"scope_id": allUsersQuotaScopeID}}),
	}, leader)
	if err == nil || AsHTTPError(err).Status != http.StatusForbidden || AsHTTPError(err).Code != "quota_forbidden" {
		t.Fatalf("partial approval target must retain template authorization: %v", err)
	}
	unchanged, err := New(store).findResource("quota-policies", policy.ID)
	if err != nil || stringField(unchanged.Fields, "scope_id") != leader.ID || int64Field(unchanged.Fields, "daily_tokens") != 10 {
		t.Fatalf("rejected partial approval changed the individual policy: %+v err=%v", unchanged, err)
	}
}
