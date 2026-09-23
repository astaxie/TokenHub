package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdminRoutingPolicyCreateBindUnbindAndAudit(t *testing.T) {
	store := NewMemoryStore()
	model := store.AddModel(Model{Name: "admin-policy-model", Modality: "chat", Status: StatusActive})
	project := store.CreateProject(Project{ID: "prj_admin_policy", Name: "Admin Policy", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_admin_policy", Name: "Admin Policy", Type: ProviderMock, Status: StatusActive, Healthy: true})
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/resources/routing-policies", map[string]any{
		"name": "Internal only", "status": StatusActive,
		"fields": map[string]any{
			"scope": RoutingPolicyScopeUnbound, "allowed_models": []string{model.Name},
			"allowed_provider_ids": []string{provider.ID}, "strategy": RouteStrategyPriorityOnly,
		},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create policy: status=%d body=%s", created.Code, created.Body)
	}
	var policy AdminResource
	if err := json.Unmarshal([]byte(created.Body), &policy); err != nil {
		t.Fatal(err)
	}
	if stringField(policy.Fields, "scope") != RoutingPolicyScopeUnbound || int(int64FromAny(policy.Fields["priority"])) != 0 {
		t.Fatalf("created policy fields = %+v", policy.Fields)
	}

	bound := doJSON(t, app, http.MethodPost, "/api/admin/routing-policies/"+policy.ID+"/bind", map[string]any{
		"scope": RoutingPolicyScopeProject, "scope_id": project.ID,
	}, "")
	if bound.Code != http.StatusOK {
		t.Fatalf("bind policy: status=%d body=%s", bound.Code, bound.Body)
	}
	if duplicate := doJSON(t, app, http.MethodPost, "/api/admin/resources/routing-policies", map[string]any{
		"name": "Duplicate", "status": StatusActive,
		"fields": map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": project.ID},
	}, ""); duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body, "routing_policy_binding_conflict") {
		t.Fatalf("duplicate binding: status=%d body=%s", duplicate.Code, duplicate.Body)
	}
	unbound := doJSON(t, app, http.MethodPost, "/api/admin/routing-policies/"+policy.ID+"/unbind", map[string]any{}, "")
	if unbound.Code != http.StatusOK {
		t.Fatalf("unbind policy: status=%d body=%s", unbound.Code, unbound.Body)
	}
	var updated AdminResource
	if err := json.Unmarshal([]byte(unbound.Body), &updated); err != nil {
		t.Fatal(err)
	}
	if stringField(updated.Fields, "scope") != RoutingPolicyScopeUnbound || stringField(updated.Fields, "scope_id") != "" {
		t.Fatalf("unbound policy fields = %+v", updated.Fields)
	}
	actions := map[string]bool{}
	for _, event := range store.ListAuditEvents() {
		if event.ResourceType == routingPolicyResourceKind && event.ResourceID == policy.ID {
			actions[event.Action] = true
		}
	}
	if !actions["create"] || !actions["bind"] || !actions["unbind"] {
		t.Fatalf("routing policy audit actions = %+v", actions)
	}
}

func TestRoutingPolicyBindingIsUniqueAcrossConcurrentCreates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_policy_create_race", Name: "Create Race", Status: StatusActive})
	start := make(chan struct{})
	errors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			_, err := store.CreateRoutingPolicy(scopedPolicyResource(
				fmt.Sprintf("pol_create_race_%d", index), RoutingPolicyScopeProject, project.ID, "",
			))
			errors <- err
		}(index)
	}
	close(start)

	succeeded, conflicted := 0, 0
	for index := 0; index < 2; index++ {
		err := <-errors
		switch {
		case err == nil:
			succeeded++
		case AsHTTPError(err).Code == "routing_policy_binding_conflict":
			conflicted++
		default:
			t.Fatalf("concurrent create error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent create results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestRoutingPolicyBindingIsUniqueAcrossConcurrentBinds(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_policy_bind_race", Name: "Bind Race", Status: StatusActive})
	policyIDs := []string{"pol_bind_race_a", "pol_bind_race_b"}
	for _, policyID := range policyIDs {
		if _, err := store.CreateRoutingPolicy(AdminResource{
			ID: policyID, Name: policyID, Status: StatusActive,
			Fields: map[string]any{"scope": RoutingPolicyScopeUnbound},
		}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errors := make(chan error, len(policyIDs))
	for _, policyID := range policyIDs {
		go func(policyID string) {
			<-start
			_, err := store.UpdateResource(routingPolicyResourceKind, policyID, AdminResource{Fields: map[string]any{
				"scope": RoutingPolicyScopeProject, "scope_id": project.ID,
			}})
			errors <- err
		}(policyID)
	}
	close(start)

	succeeded, conflicted := 0, 0
	for range policyIDs {
		err := <-errors
		switch {
		case err == nil:
			succeeded++
		case AsHTTPError(err).Code == "routing_policy_binding_conflict":
			conflicted++
		default:
			t.Fatalf("concurrent bind error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent bind results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestRoutingPolicyBindingBackfillUpgradesExistingPolicies(t *testing.T) {
	store := NewMemoryStore()
	legacy := AdminResource{
		ID: "pol_legacy_binding", Kind: routingPolicyResourceKind, Name: "Legacy Binding", Status: StatusActive,
		Fields: map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": "prj_legacy_binding"},
	}
	if err := store.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillRoutingPolicyBindingKeys(store.db); err != nil {
		t.Fatal(err)
	}
	var upgraded AdminResource
	if err := store.db.First(&upgraded, "kind = ? AND id = ?", routingPolicyResourceKind, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	want := RoutingPolicyScopeProject + ":prj_legacy_binding"
	if upgraded.RoutingPolicyBindingKey == nil || *upgraded.RoutingPolicyBindingKey != want {
		t.Fatalf("backfilled binding key = %v, want %q", upgraded.RoutingPolicyBindingKey, want)
	}
}

func TestRoutingPolicyBindingBackfillRejectsLegacyDuplicates(t *testing.T) {
	store := NewMemoryStore()
	for _, policyID := range []string{"pol_legacy_duplicate_a", "pol_legacy_duplicate_b"} {
		legacy := AdminResource{
			ID: policyID, Kind: routingPolicyResourceKind, Name: policyID, Status: StatusActive,
			Fields: map[string]any{"scope": RoutingPolicyScopeGlobal, "scope_id": RoutingPolicyScopeGlobal},
		}
		if err := store.db.Create(&legacy).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := backfillRoutingPolicyBindingKeys(store.db); err == nil {
		t.Fatal("expected duplicate legacy routing policy bindings to fail migration")
	}
}

func TestAPIKeyRotationPreservesScopedRoutingPolicyDuringGracePeriod(t *testing.T) {
	store := NewMemoryStore()
	model := store.AddModel(Model{Name: "rotation-policy-model", Modality: "chat", Status: StatusActive})
	project := store.CreateProject(Project{ID: "prj_policy_rotation", Name: "Policy Rotation", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_policy_rotation", Name: "Policy Rotation", Status: StatusActive,
		ModelAccessMode: ModelAccessModeRestricted, Allowed: []string{model.Name},
	}, "thk_policy_rotation")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_policy_rotation", Name: "Policy Rotation", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ID: "route_policy_rotation", ModelName: model.Name, ProviderID: provider.ID, ProviderModel: model.Name, Status: StatusActive})
	oldPolicy, err := store.CreateRoutingPolicy(scopedPolicyResource(
		"pol_policy_rotation", RoutingPolicyScopeAPIKey, key.ID, provider.ID,
	))
	if err != nil {
		t.Fatal(err)
	}

	graceUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	resp := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/api-keys/"+key.ID+"/rotate", map[string]any{
		"grace_until": graceUntil,
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("rotate API key: status=%d body=%s", resp.Code, resp.Body)
	}
	var rotated struct {
		ID              string   `json:"id"`
		ModelAccessMode string   `json:"model_access_mode"`
		AllowedModels   []string `json:"allowed_models"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.ID == "" || rotated.ModelAccessMode != ModelAccessModeRestricted || !containsString(rotated.AllowedModels, model.Name) {
		t.Fatalf("rotated key did not preserve model access: %+v", rotated)
	}

	oldEffective, err := effectiveScopedRoutingPolicy(store.ListResources(routingPolicyResourceKind), project.ID, key.ID)
	if err != nil || oldEffective == nil || oldEffective.ID != oldPolicy.ID {
		t.Fatalf("old key policy during grace period: policy=%+v err=%v", oldEffective, err)
	}
	newEffective, err := effectiveScopedRoutingPolicy(store.ListResources(routingPolicyResourceKind), project.ID, rotated.ID)
	if err != nil || newEffective == nil || newEffective.ID == oldPolicy.ID || !containsString(newEffective.AllowedProviderIDs, provider.ID) {
		t.Fatalf("rotated key policy: policy=%+v err=%v", newEffective, err)
	}
	rotateBindAudited := false
	for _, event := range store.ListAuditEvents() {
		if event.Action == "rotate_bind" && event.ResourceType == routingPolicyResourceKind && event.ResourceID == newEffective.ID {
			rotateBindAudited = true
			break
		}
	}
	if !rotateBindAudited {
		t.Fatal("rotated policy binding was not audited")
	}
}

func TestProjectAndAPIKeyModelAccessUsesLeastPrivilege(t *testing.T) {
	store := NewMemoryStore()
	for _, name := range []string{"model-a", "model-b", "model-c"} {
		store.AddModel(Model{Name: name, Modality: "chat", Status: StatusActive})
	}
	project, err := store.CreateProjectChecked(Project{
		ID: "prj_model_intersection", Name: "Intersection", Status: StatusActive,
		ModelAccessMode: ModelAccessModeRestricted, AllowedModels: []string{"model-a", "model-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_model_intersection", Name: "Intersection", Status: StatusActive,
		ModelAccessMode: ModelAccessModeRestricted, Allowed: []string{"model-b", "model-c"},
	}, "thk_model_intersection")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "model-b", 0); err != nil {
		t.Fatalf("intersection model should be allowed: %v", err)
	}
	for _, denied := range []string{"model-a", "model-c"} {
		if _, err := store.StartCall(context.Background(), project, key, denied, 0); AsHTTPError(err).Code != "model_not_allowed" {
			t.Fatalf("model %s error = %v", denied, err)
		}
	}
	key, err = store.UpdateAPIKey(key.ID, APIKey{ModelAccessMode: ModelAccessModeRestricted, Allowed: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "model-b", 0); AsHTTPError(err).Code != "model_not_allowed" {
		t.Fatalf("restricted empty key must deny all: %v", err)
	}
	key, err = store.UpdateAPIKey(key.ID, APIKey{ModelAccessMode: ModelAccessModeInherit})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartCall(context.Background(), project, key, "model-a", 0); err != nil {
		t.Fatalf("inherited key should use project access: %v", err)
	}
}

func TestRoutingPolicySimulationReturnsSafeDiagnosticFailures(t *testing.T) {
	store := NewMemoryStore()
	model := store.AddModel(Model{Name: "policy-diagnostic-model", Modality: "chat", Status: StatusActive})
	project := store.CreateProject(Project{ID: "prj_policy_diagnostic", Name: "Diagnostic", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_policy_diagnostic", Name: "Diagnostic", Status: StatusActive}, "thk_policy_diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_policy_diagnostic", Name: "Diagnostic", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ID: "route_policy_diagnostic", ModelName: model.Name, ProviderID: provider.ID, ProviderModel: model.Name, Status: StatusActive})
	policy := store.CreateResource(routingPolicyResourceKind, AdminResource{
		ID: "pol_policy_diagnostic", Name: "No matching tag", Status: StatusActive,
		Fields: map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": project.ID, "required_tags": []string{"internal"}},
	})
	app := New(store).Handler()
	simulate := func() RoutingPolicyResolution {
		t.Helper()
		resp := doJSON(t, app, http.MethodPost, "/api/admin/routing-policies/simulate", map[string]any{
			"project_id": project.ID, "api_key_id": key.ID, "model": model.Name,
		}, "")
		if resp.Code != http.StatusOK {
			t.Fatalf("simulate diagnostic: status=%d body=%s", resp.Code, resp.Body)
		}
		var resolution RoutingPolicyResolution
		if err := json.Unmarshal([]byte(resp.Body), &resolution); err != nil {
			t.Fatal(err)
		}
		return resolution
	}
	resolution := simulate()
	if resolution.ErrorCode != "routing_policy_no_candidate" || len(resolution.Candidates) != 1 || !containsString(resolution.Candidates[0].Reasons, "required_tags_missing") {
		t.Fatalf("no-candidate resolution = %+v", resolution)
	}
	if _, err := store.UpdateResource(routingPolicyResourceKind, policy.ID, AdminResource{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	resolution = simulate()
	if resolution.ErrorCode != "routing_policy_unavailable" || resolution.EffectivePolicy == nil || resolution.EffectivePolicy.ID != policy.ID {
		t.Fatalf("disabled resolution = %+v", resolution)
	}
}

func TestRoutingPolicySimulationExplainsMissingBaseCandidates(t *testing.T) {
	store := NewMemoryStore()
	model := store.AddModel(Model{Name: "policy-no-base-route", Modality: "chat", Status: StatusActive})
	project := store.CreateProject(Project{ID: "prj_policy_no_base", Name: "No Base Route", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_policy_no_base", Name: "No Base Route", Status: StatusActive}, "thk_policy_no_base")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.CreateRoutingPolicy(AdminResource{
		ID: "pol_policy_no_base", Name: "No Base Route", Status: StatusActive,
		Fields: map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": project.ID, "required_tags": []string{"internal"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/routing-policies/simulate", map[string]any{
		"project_id": project.ID, "api_key_id": key.ID, "model": model.Name,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("simulate missing base routes: status=%d body=%s", resp.Code, resp.Body)
	}
	var resolution RoutingPolicyResolution
	if err := json.Unmarshal([]byte(resp.Body), &resolution); err != nil {
		t.Fatal(err)
	}
	if resolution.EffectivePolicy == nil || resolution.EffectivePolicy.ID != policy.ID ||
		resolution.ErrorCode != "routing_policy_no_candidate" || resolution.ErrorMessage == "" || len(resolution.Candidates) != 0 {
		t.Fatalf("missing-base resolution = %+v", resolution)
	}

	chat := doJSON(t, New(store).Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": model.Name, "messages": []map[string]any{{"role": "user", "content": "no route"}},
	}, "thk_policy_no_base")
	if chat.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat missing base routes: status=%d body=%s", chat.Code, chat.Body)
	}
	logs := store.ListRequestLogs()
	if len(logs) == 0 || logs[len(logs)-1].RoutingPolicyID != policy.ID || logs[len(logs)-1].RoutingPolicyScope != RoutingPolicyScopeProject {
		t.Fatalf("chat no-route log missing policy metadata: %+v", logs)
	}

	anthropic := doAnthropicRequest(t, New(store).Handler(), "/v1/messages", map[string]any{
		"model": model.Name, "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "no route"}},
	}, "", "thk_policy_no_base")
	if anthropic.Code != http.StatusServiceUnavailable {
		t.Fatalf("Anthropic missing base routes: status=%d body=%s", anthropic.Code, anthropic.Body.String())
	}
	logs = store.ListRequestLogs()
	if len(logs) < 2 || logs[len(logs)-1].RoutingPolicyID != policy.ID || logs[len(logs)-1].RoutingPolicyScope != RoutingPolicyScopeProject {
		t.Fatalf("Anthropic no-route log missing policy metadata: %+v", logs)
	}
}

func TestPlaygroundGlobalRoutingPolicyAnnotatesMissingBaseCandidates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Playground Project"})
	model := store.AddModel(Model{Name: "playground-policy-no-base", Modality: "chat", Status: StatusActive})
	policy, err := store.CreateRoutingPolicy(AdminResource{
		ID: "pol_playground_no_base", Name: "Playground No Base", Status: StatusActive,
		Fields: map[string]any{"scope": RoutingPolicyScopeGlobal, "scope_id": RoutingPolicyScopeGlobal},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/playground/chat", "/api/admin/playground/chat/stream"} {
		resp := doJSON(t, New(store).Handler(), http.MethodPost, path, map[string]any{
			"project_id": project.ID, "model": model.Name, "messages": []map[string]any{{"role": "user", "content": "no route"}},
		}, "")
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s missing base routes: status=%d body=%s", path, resp.Code, resp.Body)
		}
		logs := store.ListRequestLogs()
		if len(logs) == 0 || logs[len(logs)-1].RoutingPolicyID != policy.ID || logs[len(logs)-1].RoutingPolicyScope != RoutingPolicyScopeGlobal {
			t.Fatalf("%s no-route log missing policy metadata: %+v", path, logs)
		}
	}
}

func TestScopedRoutingPolicySimulationUsesKeyProjectGlobalPrecedence(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_scoped_policy", Name: "Scoped Policy", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_scoped_policy", Name: "Scoped Policy", Status: StatusActive}, "thk_scoped_policy")
	if err != nil {
		t.Fatal(err)
	}
	model := store.AddModel(Model{Name: "scoped-policy-model", Modality: "chat", Status: StatusActive})

	providers := []Provider{
		{ID: "prv_policy_global", Name: "Global", Type: ProviderMock, Status: StatusActive, Healthy: true},
		{ID: "prv_policy_project", Name: "Project", Type: ProviderMock, Status: StatusActive, Healthy: true},
		{ID: "prv_policy_key", Name: "Key", Type: ProviderMock, Status: StatusActive, Healthy: true},
	}
	for index, provider := range providers {
		provider = store.AddProvider(provider)
		store.AddRoute(ModelRoute{
			ID:            "route_" + provider.ID,
			ModelName:     model.Name,
			ProviderID:    provider.ID,
			ProviderModel: model.Name,
			Priority:      index + 1,
			Weight:        100,
			Status:        StatusActive,
			Strategy:      RouteStrategyPriorityOnly,
		})
	}

	store.CreateResource("routing-policies", scopedPolicyResource(
		"pol_global", "global", "global", providers[0].ID,
	))
	projectPolicy := store.CreateResource("routing-policies", scopedPolicyResource(
		"pol_project", "project", project.ID, providers[1].ID,
	))
	keyPolicy := store.CreateResource("routing-policies", scopedPolicyResource(
		"pol_key", "api_key", key.ID, providers[2].ID,
	))

	app := New(store).Handler()
	assertSimulation := func(wantPolicyID, wantScope, wantRouteID string) {
		t.Helper()
		resp := doJSON(t, app, http.MethodPost, "/api/admin/routing-policies/simulate", map[string]any{
			"project_id": project.ID,
			"api_key_id": key.ID,
			"model":      model.Name,
		}, "")
		if resp.Code != http.StatusOK {
			t.Fatalf("simulate routing policy: status=%d body=%s", resp.Code, resp.Body)
		}
		var payload struct {
			EffectivePolicy struct {
				ID       string `json:"id"`
				Scope    string `json:"scope"`
				Priority int    `json:"priority"`
			} `json:"effective_policy"`
			SelectedRouteID string `json:"selected_route_id"`
		}
		if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.EffectivePolicy.ID != wantPolicyID || payload.EffectivePolicy.Scope != wantScope {
			t.Fatalf("effective policy = %+v, want id=%s scope=%s", payload.EffectivePolicy, wantPolicyID, wantScope)
		}
		if payload.SelectedRouteID != wantRouteID {
			t.Fatalf("selected route = %q, want %q", payload.SelectedRouteID, wantRouteID)
		}
	}

	assertSimulation(keyPolicy.ID, "api_key", "route_"+providers[2].ID)
	if err := store.DeleteResource("routing-policies", keyPolicy.ID); err != nil {
		t.Fatal(err)
	}
	assertSimulation(projectPolicy.ID, "project", "route_"+providers[1].ID)
	if err := store.DeleteResource("routing-policies", projectPolicy.ID); err != nil {
		t.Fatal(err)
	}
	assertSimulation("pol_global", "global", "route_"+providers[0].ID)
}

func TestProjectRoutingPolicyNeverFallsBackOutsideItsCandidates(t *testing.T) {
	var internalHits, externalHits int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&internalHits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"internal capacity exhausted"}}`)
	}))
	defer internal.Close()
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&externalHits, 1)
		writeChatStreamChunk(w, "external response must not be used")
	}))
	defer external.Close()

	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_internal_only", Name: "Internal Only", Status: StatusActive})
	const modelName = "internal-only-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Internal Only", Allowed: []string{modelName}, Status: StatusActive}, "thk_internal_only")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	internalProvider := store.AddProvider(Provider{ID: "prv_internal_only", Name: "Internal", Type: ProviderOpenAICompatible, BaseURL: internal.URL, Status: StatusActive, Healthy: true})
	externalProvider := store.AddProvider(Provider{ID: "prv_external_fallback", Name: "External", Type: ProviderOpenAICompatible, BaseURL: external.URL, Status: StatusActive, Healthy: true})
	for index, provider := range []Provider{internalProvider, externalProvider} {
		store.AddRoute(ModelRoute{
			ID:            "route_" + provider.ID,
			ModelName:     modelName,
			ProviderID:    provider.ID,
			ProviderModel: modelName,
			Priority:      index + 1,
			Weight:        100,
			Status:        StatusActive,
			Strategy:      RouteStrategyPriorityOnly,
		})
	}
	store.CreateResource(routingPolicyResourceKind, scopedPolicyResource(
		"pol_internal_only", RoutingPolicyScopeProject, project.ID, internalProvider.ID,
	))

	resp := postStream(t, New(store).Handler(), "/v1/chat/completions", map[string]any{
		"model":    modelName,
		"messages": []map[string]any{{"role": "user", "content": "private workload"}},
		"stream":   true,
	}, secret)
	if resp.Code == http.StatusOK {
		t.Fatalf("internal-only request unexpectedly fell back: %s", resp.Body.String())
	}
	if atomic.LoadInt32(&internalHits) != 1 {
		t.Fatalf("internal route hits = %d, want 1", internalHits)
	}
	if atomic.LoadInt32(&externalHits) != 0 {
		t.Fatalf("external route must not be attempted, hits=%d", externalHits)
	}
	logs := store.ListRequestLogs()
	if len(logs) == 0 || logs[len(logs)-1].RoutingPolicyID != "pol_internal_only" || logs[len(logs)-1].RoutingPolicyScope != RoutingPolicyScopeProject {
		t.Fatalf("request log is missing policy metadata: %+v", logs)
	}

	anthropicResp := doAnthropicRequest(t, New(store).Handler(), "/v1/messages", map[string]any{
		"model": modelName, "max_tokens": 32,
		"messages": []any{map[string]any{"role": "user", "content": "private workload"}},
	}, "", secret)
	if anthropicResp.Code == http.StatusOK {
		t.Fatalf("Anthropic request unexpectedly fell back: %s", anthropicResp.Body.String())
	}
	if atomic.LoadInt32(&internalHits) != 2 || atomic.LoadInt32(&externalHits) != 0 {
		t.Fatalf("Anthropic scoped attempts internal=%d external=%d", internalHits, externalHits)
	}
}

func TestRoutingPolicyAllowsFailoverWithinScopedCandidates(t *testing.T) {
	var firstInternalHits, secondInternalHits, externalHits int32
	firstInternal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&firstInternalHits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"first internal provider busy"}}`)
	}))
	defer firstInternal.Close()
	secondInternal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&secondInternalHits, 1)
		writeChatStreamChunk(w, "scoped internal failover")
	}))
	defer secondInternal.Close()
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&externalHits, 1)
		writeChatStreamChunk(w, "external response must not be used")
	}))
	defer external.Close()

	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_scoped_failover", Name: "Scoped Failover", Status: StatusActive})
	const modelName = "scoped-failover-model"
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Scoped Failover", Status: StatusActive}, "thk_scoped_failover")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	providers := []Provider{
		{ID: "prv_scoped_failover_a", Name: "Internal A", Type: ProviderOpenAICompatible, BaseURL: firstInternal.URL, Status: StatusActive, Healthy: true},
		{ID: "prv_scoped_failover_b", Name: "Internal B", Type: ProviderOpenAICompatible, BaseURL: secondInternal.URL, Status: StatusActive, Healthy: true},
		{ID: "prv_scoped_failover_external", Name: "External", Type: ProviderOpenAICompatible, BaseURL: external.URL, Status: StatusActive, Healthy: true},
	}
	for index, provider := range providers {
		provider = store.AddProvider(provider)
		store.AddRoute(ModelRoute{
			ID: "route_" + provider.ID, ModelName: modelName, ProviderID: provider.ID, ProviderModel: modelName,
			Priority: index + 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
		})
		providers[index] = provider
	}
	if _, err := store.CreateRoutingPolicy(AdminResource{
		ID: "pol_scoped_failover", Name: "Internal Failover", Status: StatusActive,
		Fields: map[string]any{
			"scope": RoutingPolicyScopeProject, "scope_id": project.ID,
			"allowed_provider_ids": []string{providers[0].ID, providers[1].ID},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resp := postStream(t, New(store).Handler(), "/v1/chat/completions", map[string]any{
		"model": modelName, "messages": []map[string]any{{"role": "user", "content": "private workload"}}, "stream": true,
	}, secret)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "scoped internal failover") {
		t.Fatalf("scoped failover response: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if atomic.LoadInt32(&firstInternalHits) != 1 || atomic.LoadInt32(&secondInternalHits) != 1 || atomic.LoadInt32(&externalHits) != 0 {
		t.Fatalf("scoped failover hits: first=%d second=%d external=%d", firstInternalHits, secondInternalHits, externalHits)
	}
}

func TestImageRoutingPolicyNeverFallsBackOutsideItsCandidates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_internal_image", Name: "Internal Image", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "Internal Image", Allowed: []string{openAIImageModelName}, Status: StatusActive,
	}, "thk_internal_image")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	internalProvider := store.AddProvider(Provider{ID: "prv_internal_image", Name: "Internal Image", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	externalProvider := store.AddProvider(Provider{ID: "prv_external_image", Name: "External Image", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	for index, provider := range []Provider{internalProvider, externalProvider} {
		store.AddRoute(ModelRoute{
			ID: "route_" + provider.ID, ModelName: openAIImageModelName, ProviderID: provider.ID,
			ProviderModel: openAIImageModelName, Priority: index + 1, Weight: 100,
			Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
		})
	}
	store.CreateResource(routingPolicyResourceKind, scopedPolicyResource(
		"pol_internal_image", RoutingPolicyScopeProject, project.ID, internalProvider.ID,
	))

	server := NewWithConfig(store, Config{
		AdminToken: "test-admin-token", SecretKey: "internal-image-policy-secret", ImageStorageDir: t.TempDir(),
	})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	var internalHits, externalHits int32
	server.imageRunner = func(_ context.Context, route RouteSelection, _ ImageJob) ([]byte, string, Usage, error) {
		if route.Provider.ID == internalProvider.ID {
			atomic.AddInt32(&internalHits, 1)
			return nil, "", Usage{}, NewHTTPError(http.StatusServiceUnavailable, "provider_upstream_unavailable", "Internal image provider unavailable")
		}
		atomic.AddInt32(&externalHits, 1)
		return realPNGFixture(t), "", Usage{}, nil
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "private image workload",
	}, secret, nil)
	if response.Code == http.StatusOK {
		t.Fatalf("internal-only image request unexpectedly fell back: %s", response.Body)
	}
	if atomic.LoadInt32(&internalHits) != 1 || atomic.LoadInt32(&externalHits) != 0 {
		t.Fatalf("image scoped attempts internal=%d external=%d", internalHits, externalHits)
	}
	logs := store.ListRequestLogs()
	if len(logs) == 0 || logs[len(logs)-1].RoutingPolicyID != "pol_internal_image" || logs[len(logs)-1].RoutingPolicyScope != RoutingPolicyScopeProject {
		t.Fatalf("image request log is missing policy metadata: %+v", logs)
	}
}

func TestImageRoutingPolicyAnnotatesMissingBaseCandidates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_image_no_base", Name: "Image No Base", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_image_no_base", Name: "Image No Base", Status: StatusActive}, "thk_image_no_base")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	policy, err := store.CreateRoutingPolicy(AdminResource{
		ID: "pol_image_no_base", Name: "Image No Base", Status: StatusActive,
		Fields: map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": project.ID, "required_tags": []string{"internal"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-no-base-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "private image workload",
	}, secret, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("image missing base routes: status=%d body=%s", response.Code, response.Body)
	}
	logs := store.ListRequestLogs()
	if len(logs) == 0 || logs[len(logs)-1].RoutingPolicyID != policy.ID || logs[len(logs)-1].RoutingPolicyScope != RoutingPolicyScopeProject {
		t.Fatalf("image no-route log missing policy metadata: %+v", logs)
	}
}

func TestRoutingPolicyFiltersCandidatesBeforeCacheAffinity(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_policy_affinity", Name: "Affinity", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_policy_affinity", Name: "Affinity", Status: StatusActive}, "thk_policy_affinity")
	if err != nil {
		t.Fatal(err)
	}
	store.CreateResource(routingPolicyResourceKind, scopedPolicyResource("pol_policy_affinity", RoutingPolicyScopeAPIKey, key.ID, "prv_internal_affinity"))
	routes := []RouteSelection{
		{Provider: Provider{ID: "prv_external_affinity"}, Route: ModelRoute{ID: "route_external_affinity", ProviderID: "prv_external_affinity", Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyBalanced}},
		{Provider: Provider{ID: "prv_internal_affinity"}, Route: ModelRoute{ID: "route_internal_affinity", ProviderID: "prv_internal_affinity", Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyBalanced}},
	}
	call := CallContext{
		RequestID: "req_policy_affinity", Project: project, Key: key, Model: Model{Name: "affinity-model"},
		Affinity: &RequestAffinity{Kind: AffinityKindCacheLocality, KeyHash: "external-preferring-session"},
	}
	filtered, err := New(store).resolveScopedRoutingPolicyForCall(&call, routes)
	if err != nil {
		t.Fatal(err)
	}
	planned := New(store).planRouteOrder(call, filtered)
	if len(planned) != 1 || planned[0].Provider.ID != "prv_internal_affinity" {
		t.Fatalf("affinity escaped scoped candidates: %+v", planned)
	}
}

func TestRoutingPolicyLimitsHalfOpenRecoveryToScopedCandidates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_policy_half_open", Name: "Half Open", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_policy_half_open", Name: "Half Open", Status: StatusActive}, "thk_policy_half_open")
	if err != nil {
		t.Fatal(err)
	}
	model := store.AddModel(Model{Name: "policy-half-open-model", Modality: "chat", Status: StatusActive})
	internal := store.AddProvider(Provider{ID: "prv_policy_half_open_internal", Name: "Half Open Internal", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true})
	external := store.AddProvider(Provider{ID: "prv_policy_half_open_external", Name: "Half Open External", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true})
	internalResource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_policy_half_open_internal", ProviderID: internal.ID, Name: "Half Open Internal", Status: StatusActive, Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	externalResource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_policy_half_open_external", ProviderID: external.ID, Name: "Half Open External", Status: StatusActive, Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	cooldownExpired := time.Now().UTC().Add(-time.Minute)
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", internalResource.ID).Updates(map[string]any{
		"healthy": false, "cooldown_until": cooldownExpired,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, route := range []ModelRoute{
		{ID: "route_policy_half_open_internal", ModelName: model.Name, ProviderID: internal.ID, ProviderResourceID: internalResource.ID, ProviderModel: model.Name, Status: StatusActive},
		{ID: "route_policy_half_open_external", ModelName: model.Name, ProviderID: external.ID, ProviderResourceID: externalResource.ID, ProviderModel: model.Name, Status: StatusActive},
	} {
		store.AddRoute(route)
	}
	if _, err := store.CreateRoutingPolicy(scopedPolicyResource(
		"pol_policy_half_open", RoutingPolicyScopeProject, project.ID, internal.ID,
	)); err != nil {
		t.Fatal(err)
	}

	routes, err := store.SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	call := CallContext{RequestID: "req_policy_half_open", Project: project, Key: key, Model: model}
	filtered, err := New(store).resolveScopedRoutingPolicyForCall(&call, routes)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || routeResourceID(filtered[0]) != internalResource.ID {
		t.Fatalf("half-open scoped candidates = %+v", filtered)
	}
	_, claimContext, err := store.CheckProviderResourceCapacity(context.Background(), routeResourceID(filtered[0]))
	if err != nil {
		t.Fatalf("scoped half-open trial was not admitted: %v", err)
	}
	if !hasHalfOpenClaim(claimContext) {
		t.Fatal("scoped half-open route did not receive the recovery claim")
	}
}

func TestRoutingPolicySimulationExplainsResourceTagAndBoundaryConstraints(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_policy_boundary", Name: "Boundary", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{ID: "key_policy_boundary", Name: "Boundary", Status: StatusActive}, "thk_policy_boundary")
	if err != nil {
		t.Fatal(err)
	}
	model := store.AddModel(Model{Name: "boundary-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_policy_boundary", Name: "Boundary", Type: ProviderMock, Status: StatusActive, Healthy: true})
	cnResource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_policy_cn", ProviderID: provider.ID, Name: "CN", ResourceType: "api_key",
		Region: "cn-east", Environment: "prod", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddProviderResource(ProviderResource{
		ID: "rsrc_policy_us", ProviderID: provider.ID, Name: "US", ResourceType: "api_key",
		Region: "us-west", Environment: "prod", Status: StatusActive, Healthy: true, Priority: 2, Weight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ID: "route_policy_boundary", ModelName: model.Name, ProviderID: provider.ID, ProviderModel: model.Name,
		Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly,
		Tags: []string{"internal", "compliant"},
	})
	store.CreateResource(routingPolicyResourceKind, AdminResource{
		ID: "pol_boundary", Name: "CN Internal", Status: StatusActive,
		Fields: map[string]any{
			"scope":                         RoutingPolicyScopeProject,
			"scope_id":                      project.ID,
			"allowed_models":                []string{model.Name},
			"allowed_provider_ids":          []string{provider.ID},
			"allowed_provider_resource_ids": []string{cnResource.ID},
			"required_tags":                 []string{"internal", "compliant"},
			"allowed_regions":               []string{"cn-east"},
			"allowed_environments":          []string{"prod"},
		},
	})

	resp := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/routing-policies/simulate", map[string]any{
		"project_id": project.ID,
		"api_key_id": key.ID,
		"model":      model.Name,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("simulate routing boundary: status=%d body=%s", resp.Code, resp.Body)
	}
	var resolution RoutingPolicyResolution
	if err := json.Unmarshal([]byte(resp.Body), &resolution); err != nil {
		t.Fatal(err)
	}
	if resolution.SelectedRouteID != "route_policy_boundary" {
		t.Fatalf("selected route = %q", resolution.SelectedRouteID)
	}
	if len(resolution.Candidates) != 2 {
		t.Fatalf("candidate decisions = %+v", resolution.Candidates)
	}
	decisions := map[string]RoutingPolicyCandidateDecision{}
	for _, decision := range resolution.Candidates {
		decisions[decision.ResourceID] = decision
	}
	if !decisions[cnResource.ID].Allowed {
		t.Fatalf("CN resource should be allowed: %+v", decisions[cnResource.ID])
	}
	usDecision := decisions["rsrc_policy_us"]
	if usDecision.Allowed || !containsString(usDecision.Reasons, "resource_not_allowed") || !containsString(usDecision.Reasons, "region_not_allowed") {
		t.Fatalf("US resource exclusion is not explained: %+v", usDecision)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func scopedPolicyResource(id string, scope string, scopeID string, providerID string) AdminResource {
	return AdminResource{
		ID:     id,
		Name:   id,
		Status: StatusActive,
		Fields: map[string]any{
			"scope":                scope,
			"scope_id":             scopeID,
			"allowed_provider_ids": []string{providerID},
		},
	}
}
