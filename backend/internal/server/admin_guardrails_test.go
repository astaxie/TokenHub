package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tokenhub/backend/internal/guardrails"

	"gorm.io/gorm"
)

func TestAdminGuardrailPolicyLifecycle(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	secondProject := store.CreateProject(Project{Name: "Second project", Status: StatusActive})
	app := New(store).Handler()

	createdResponse := doJSON(t, app, http.MethodPost, "/api/admin/guardrail-policies", map[string]any{
		"name":        "Outbound protection",
		"description": "Protect enterprise data before provider dispatch.",
		"detection_items": []map[string]any{{
			"name": "Internal names", "detector_type": "pattern", "action": "block",
			"config": map[string]any{"keywords": []string{"Project Aurora"}, "regex": []string{"TH-[0-9]{6}"}},
		}, {
			"name": "Customer identity", "detector_type": "sensitive_data", "action": "mask",
			"config": map[string]any{"data_types": []string{"email", "phone", "cn_id_card"}},
		}, {
			"name": "Qwen3Guard", "detector_type": "model", "action": "audit",
			"config": map[string]any{},
		}},
		"bindings": []map[string]any{
			{"scope_type": "project", "scope_id": defaultProjectID},
			{"scope_type": "project", "scope_id": secondProject.ID},
		},
	}, "")
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("expected create 201, got %d: %s", createdResponse.Code, createdResponse.Body)
	}
	var created guardrails.Policy
	if err := json.Unmarshal([]byte(createdResponse.Body), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || len(created.DetectionItems) != 3 || len(created.Bindings) != 2 {
		t.Fatalf("unexpected created policy: %#v", created)
	}
	if created.Bindings[0].Checkpoint != guardrails.CheckpointBeforeProvider || created.Bindings[0].Protocol != guardrails.ProtocolAll {
		t.Fatalf("expected fixed MVP checkpoint and protocol: %#v", created.Bindings[0])
	}
	modelItem := guardrailDetectionItemByType(t, created, guardrails.DetectorModel)
	if created.ConfigVersion != guardrails.CurrentConfigVersion || modelItem.Config["block_on"] != "unsafe" {
		t.Fatalf("expected normalized config versions and model defaults: %#v", created)
	}

	listed := doJSON(t, app, http.MethodGet, "/api/admin/guardrail-policies", nil, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, created.ID) {
		t.Fatalf("expected policy in list, got %d: %s", listed.Code, listed.Body)
	}
	loaded := doJSON(t, app, http.MethodGet, "/api/admin/guardrail-policies/"+created.ID, nil, "")
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body, "Project Aurora") {
		t.Fatalf("expected policy detail, got %d: %s", loaded.Code, loaded.Body)
	}

	patternItem := guardrailDetectionItemByType(t, created, guardrails.DetectorPattern)
	patternItemID := patternItem.ID
	created.Name = "Outbound protection updated"
	for index := range created.DetectionItems {
		if created.DetectionItems[index].ID == patternItemID {
			created.DetectionItems[index].Config = map[string]any{"keywords": []any{"Project Atlas"}}
		}
	}
	created.Bindings = []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}}
	updatedResponse := doJSON(t, app, http.MethodPut, "/api/admin/guardrail-policies/"+created.ID, created, "")
	if updatedResponse.Code != http.StatusOK {
		t.Fatalf("expected update 200, got %d: %s", updatedResponse.Code, updatedResponse.Body)
	}
	var updated guardrails.Policy
	if err := json.Unmarshal([]byte(updatedResponse.Body), &updated); err != nil {
		t.Fatal(err)
	}
	updatedPattern := guardrailDetectionItemByType(t, updated, guardrails.DetectorPattern)
	if updated.Name != created.Name || updatedPattern.ID != patternItemID || len(updated.Bindings) != 1 || updated.Bindings[0].ScopeType != guardrails.ScopeAllProjects {
		t.Fatalf("unexpected updated policy: %#v", updated)
	}

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/guardrail-policies/"+created.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("expected delete 204, got %d: %s", deleted.Code, deleted.Body)
	}
	missing := doJSON(t, app, http.MethodGet, "/api/admin/guardrail-policies/"+created.ID, nil, "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body, `"code":"guardrail_policy_not_found"`) {
		t.Fatalf("expected deleted policy to be absent, got %d: %s", missing.Code, missing.Body)
	}
	auditActions := map[string]bool{}
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType != "guardrail_policy" {
			continue
		}
		auditActions[event.Action] = true
		snapshots := event.BeforeSnapshot + event.AfterSnapshot
		if strings.Contains(snapshots, "Project Aurora") || strings.Contains(snapshots, "Project Atlas") || strings.Contains(snapshots, `"config":`) {
			t.Fatalf("guardrail audit leaked detector config: %#v", event)
		}
	}
	for _, action := range []string{"create", "update", "delete"} {
		if !auditActions[action] {
			t.Fatalf("missing %s guardrail audit event: %#v", action, store.ListAuditEvents())
		}
	}
}

func TestGuardrailPolicyPutIsAllowedByCORS(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/api/admin/guardrail-policies/grp_test", nil)
	request.Header.Set("origin", "http://localhost:3000")
	request.Header.Set("access-control-request-method", http.MethodPut)
	response := httptest.NewRecorder()
	New(NewMemoryStore()).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected preflight 204, got %d: %s", response.Code, response.Body.String())
	}
	methods := response.Header().Get("access-control-allow-methods")
	if !strings.Contains(methods, http.MethodPut) {
		t.Fatalf("expected PUT in CORS methods, got %q", methods)
	}
}

func TestGuardrailPolicyUpdateIsAtomic(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Atomic project", Status: StatusActive})
	created, err := store.CreateGuardrailPolicy(testGuardrailPolicy("Original", project.ID))
	if err != nil {
		t.Fatal(err)
	}

	update := created
	update.Name = "Must roll back"
	update.DetectionItems[0].Name = "Must also roll back"
	update.Bindings = []guardrails.Binding{{ScopeType: guardrails.ScopeProject, ScopeID: "prj_missing"}}
	if _, _, err := store.UpdateGuardrailPolicy(created.ID, update); AsHTTPError(err).Code != "guardrail_binding_project_not_found" {
		t.Fatalf("expected missing project error, got %#v", err)
	}
	after, err := store.GetGuardrailPolicy(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "Original" || after.DetectionItems[0].Name != "Internal names" || after.Bindings[0].ScopeID != project.ID {
		t.Fatalf("failed update left partial state: before=%#v after=%#v", created, after)
	}
}

func TestGuardrailPoliciesComposeOnOneProject(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Shared project", Status: StatusActive})
	for _, name := range []string{"Enterprise baseline", "Project protection"} {
		if _, err := store.CreateGuardrailPolicy(testGuardrailPolicy(name, project.ID)); err != nil {
			t.Fatal(err)
		}
	}
	policies, err := store.ListGuardrailPolicies()
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 || policies[0].Bindings[0].ScopeID != project.ID || policies[1].Bindings[0].ScopeID != project.ID {
		t.Fatalf("expected two policies on one project: %#v", policies)
	}

	if err := store.DeleteProject(project.ID); AsHTTPError(err).Code != "project_guardrail_binding_conflict" {
		t.Fatalf("expected project deletion to require explicit policy update, got %v", err)
	}
}

func TestAdminGuardrailPolicyReturnsStableValidationErrors(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{name: "unknown detector", mutate: func(payload map[string]any) {
			payload["detection_items"].([]map[string]any)[0]["detector_type"] = "custom"
		}, code: "unknown_guardrail_detector_type"},
		{name: "invalid action", mutate: func(payload map[string]any) {
			payload["detection_items"].([]map[string]any)[0]["action"] = "mask"
		}, code: "invalid_guardrail_action"},
		{name: "unsupported config", mutate: func(payload map[string]any) {
			payload["detection_items"].([]map[string]any)[0]["config"] = map[string]any{"keywords": []string{"Aurora"}, "command": "run"}
		}, code: "invalid_guardrail_detector_config"},
		{name: "duplicate binding", mutate: func(payload map[string]any) {
			payload["bindings"] = []map[string]any{{"scope_type": "project", "scope_id": defaultProjectID}, {"scope_type": "project", "scope_id": defaultProjectID}}
		}, code: "guardrail_binding_conflict"},
		{name: "unsupported version", mutate: func(payload map[string]any) {
			payload["config_version"] = 2
		}, code: "unsupported_guardrail_config_version"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := validGuardrailPayload()
			test.mutate(payload)
			response := doJSON(t, app, http.MethodPost, "/api/admin/guardrail-policies", payload, "")
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, `"code":"`+test.code+`"`) {
				t.Fatalf("expected %s, got %d: %s", test.code, response.Code, response.Body)
			}
		})
	}
}

func TestGuardrailSchemaEnforcesUniqueBindings(t *testing.T) {
	store := NewMemoryStore()
	for _, model := range []any{&guardrails.Policy{}, &guardrails.DetectionItem{}, &guardrails.Binding{}} {
		if !store.db.Migrator().HasTable(model) {
			t.Fatalf("guardrail table was not migrated for %T", model)
		}
	}
	project := store.CreateProject(Project{Name: "Constraint project", Status: StatusActive})
	created, err := store.CreateGuardrailPolicy(testGuardrailPolicy("Constraint policy", project.ID))
	if err != nil {
		t.Fatal(err)
	}
	duplicate := guardrails.Binding{
		ID: "gbd_duplicate", PolicyID: created.ID, ScopeType: guardrails.ScopeProject,
		ScopeID: project.ID, Checkpoint: guardrails.CheckpointBeforeProvider, Protocol: guardrails.ProtocolAll,
		ConfigVersion: guardrails.CurrentConfigVersion,
	}
	if err := store.db.Create(&duplicate).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("expected duplicate binding constraint, got %#v", err)
	}
}

func testGuardrailPolicy(name string, projectID string) guardrails.Policy {
	return guardrails.Policy{
		Name: name,
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Internal names", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock,
			Config: map[string]any{"keywords": []string{"Project Aurora"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeProject, ScopeID: projectID}},
	}
}

func validGuardrailPayload() map[string]any {
	return map[string]any{
		"name": "Outbound protection",
		"detection_items": []map[string]any{{
			"name": "Internal names", "detector_type": "pattern", "action": "block",
			"config": map[string]any{"keywords": []string{"Project Aurora"}},
		}},
		"bindings": []map[string]any{{"scope_type": "project", "scope_id": defaultProjectID}},
	}
}

func guardrailDetectionItemByType(t *testing.T, policy guardrails.Policy, detectorType string) guardrails.DetectionItem {
	t.Helper()
	for _, item := range policy.DetectionItems {
		if item.DetectorType == detectorType {
			return item
		}
	}
	t.Fatalf("detector %s not found in policy %#v", detectorType, policy)
	return guardrails.DetectionItem{}
}
