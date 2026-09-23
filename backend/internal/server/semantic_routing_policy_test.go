package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSemanticRoutingPolicyPersistenceAndAtomicity(t *testing.T) {
	server, routed, _ := semanticFixture(t)
	policy := ModelRoutePolicy{Strategy: RouteStrategyQuality, SemanticRouting: &SemanticRoutingPolicy{Mode: "shadow", MinConfidence: 0.8}}
	for _, route := range routed.Routes {
		policy.Routes = append(policy.Routes, ModelRoutePolicyRoute{RouteID: route.Route.ID, Weight: 100, QualityScore: 50, CostScore: 50})
	}
	path := "/api/admin/model-routing-policies/auto-chat"
	result := doJSON(t, server.Handler(), http.MethodPatch, path, policy, "")
	if result.Code != 200 {
		t.Fatalf("policy update failed: %s", result.Body)
	}
	read := func() Model {
		t.Helper()
		for _, model := range server.store.ListModels() {
			if model.Name == "auto-chat" {
				return model
			}
		}
		t.Fatal("model missing")
		return Model{}
	}
	model := read()
	if modelSemanticRoutingPolicy(model).Mode != "shadow" || model.Metadata["owner"] != "synthetic" {
		t.Fatalf("metadata not preserved: %+v", model.Metadata)
	}
	// Old clients can still update base policy without resetting semantic routing.
	policy.SemanticRouting = nil
	policy.Strategy = RouteStrategyAdaptive
	if result = doJSON(t, server.Handler(), http.MethodPatch, path, policy, ""); result.Code != 200 {
		t.Fatal(result.Body)
	}
	if !reflect.DeepEqual(read().Metadata, model.Metadata) {
		t.Fatal("legacy update reset semantic policy")
	}
	routesBefore := server.store.ListRoutes()
	for _, invalid := range []*SemanticRoutingPolicy{{Mode: "unknown", MinConfidence: 0.5}, {Mode: "enforce", MinConfidence: 1.1}, {Mode: "shadow", MinConfidence: -0.1}} {
		policy.SemanticRouting = invalid
		policy.Strategy = RouteStrategyCost
		if result = doJSON(t, server.Handler(), http.MethodPatch, path, policy, ""); result.Code != 400 {
			t.Fatalf("expected validation failure: %d", result.Code)
		}
		if !reflect.DeepEqual(routesBefore, server.store.ListRoutes()) || !reflect.DeepEqual(read().Metadata, model.Metadata) {
			t.Fatal("failed update partially persisted")
		}
	}
	policy.SemanticRouting = &SemanticRoutingPolicy{Mode: "off", MinConfidence: 0.65}
	policy.Routes[0].RouteID = "missing"
	if result = doJSON(t, server.Handler(), http.MethodPatch, path, policy, ""); result.Code != 400 {
		t.Fatal("unknown route accepted")
	}
	if !reflect.DeepEqual(read().Metadata, model.Metadata) {
		t.Fatal("invalid routes changed metadata")
	}
}

func TestSemanticRoutingConfigAndSessionHeader(t *testing.T) {
	config := Config{SemanticRoutingEnabled: true, TypeSafeAPIKey: "test", SemanticRoutingProjects: []string{"prj_test"}, SemanticRoutingTimeoutMS: 1000}
	if err := config.validateSemanticRouting(); err != nil {
		t.Fatal(err)
	}
	invalid := config
	invalid.TypeSafeAPIKey = ""
	if invalid.validateSemanticRouting() == nil {
		t.Fatal("missing key accepted")
	}
	invalid = config
	invalid.SemanticRoutingProjects = nil
	if invalid.validateSemanticRouting() == nil {
		t.Fatal("missing allowlist accepted")
	}
	invalid = config
	invalid.SemanticRoutingTimeoutMS = -1
	if invalid.validateSemanticRouting() == nil {
		t.Fatal("invalid timeout accepted")
	}
	t.Setenv("TOKENHUB_SEMANTIC_ROUTING_ENABLED", "true")
	t.Setenv("TOKENHUB_SEMANTIC_ROUTING_PROJECTS", "prj_a,prj_b")
	t.Setenv("TOKENHUB_TYPESAFE_API_KEY", "synthetic")
	t.Setenv("TOKENHUB_SEMANTIC_ROUTING_TIMEOUT_MS", "invalid")
	if ConfigFromEnv().validateSemanticRouting() == nil {
		t.Fatal("malformed timeout silently accepted")
	}
	server, routed, req := semanticFixture(t)
	server.semanticRouter = semanticTestEvaluator(func(context.Context, string, []semanticCandidate, string) (semanticDecision, error) {
		t.Fatal("session header caused semantic evaluation")
		return semanticDecision{}, nil
	})
	if err := server.applySemanticRouting(context.Background(), &routed, req, http.Header{"X-Tokenhub-Session-Id": []string{"synthetic-session"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(server.store.ListAuditEvents())
	if string(data) == "[]" {
		t.Fatal("missing skip audit")
	}
}

func TestSemanticRoutingSurvivesCatalogReloadAndStaleModelEdits(t *testing.T) {
	server, routed, _ := semanticFixture(t)
	store := server.store.(*GormStore)
	catalog := filepath.Join(t.TempDir(), "models.yaml")
	content := "version: 1\nmodels:\n  - name: auto-chat\n    category: refreshed\n    family: test\n    modality: chat\n    context_window: 1000\n    input_modalities: [text]\n    output_modalities: [text]\n    capabilities: [chat]\n    supported_parameters: []\n"
	if err := os.WriteFile(catalog, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RunStartupBootstrap(context.Background(), store, Config{ModelCatalogFile: catalog, BootstrapAdminPassword: "synthetic-test-password"}); err != nil {
		t.Fatal(err)
	}
	read := func() Model {
		t.Helper()
		var current Model
		if err := store.db.First(&current, "name = ?", routed.Call.Model.Name).Error; err != nil {
			t.Fatal(err)
		}
		return current
	}
	current := read()
	if modelSemanticRoutingPolicy(current).Mode != "enforce" || current.Category != "refreshed" {
		t.Fatal("startup lost policy or failed to refresh catalog")
	}
	stale := current
	policy := ModelRoutePolicy{Strategy: RouteStrategyQuality, SemanticRouting: &SemanticRoutingPolicy{Mode: "off", MinConfidence: 0.8}}
	for _, route := range routed.Routes {
		policy.Routes = append(policy.Routes, ModelRoutePolicyRoute{RouteID: route.Route.ID, Weight: 100, QualityScore: 50, CostScore: 50})
	}
	if _, err := store.UpdateModelRoutePolicy(current.Name, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateModel(current.Name, stale); err != nil {
		t.Fatal(err)
	}
	if modelSemanticRoutingPolicy(read()).Mode != "off" {
		t.Fatal("stale model editor restored egress")
	}
	store.AddModel(stale)
	if modelSemanticRoutingPolicy(read()).Mode != "off" {
		t.Fatal("stale model upsert restored egress")
	}
	if _, err := store.CreateModelWithRoutes(stale, nil); err != nil {
		t.Fatal(err)
	}
	if modelSemanticRoutingPolicy(read()).Mode != "off" {
		t.Fatal("stale catalog upsert restored egress")
	}
}
