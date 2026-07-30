package server

import (
	"fmt"
	"net/http"
	"testing"
)

func TestPriorityWeightedRoutingFollowsConfiguredRatio(t *testing.T) {
	server := New(NewMemoryStore())
	routes := []RouteSelection{
		{Route: ModelRoute{ID: "route_75", Priority: 1, Weight: 75, Strategy: RouteStrategyPriorityWeighted}},
		{Route: ModelRoute{ID: "route_25", Priority: 1, Weight: 25, Strategy: RouteStrategyPriorityWeighted}},
	}

	firstChoices := map[string]int{}
	for index := 0; index < 10_000; index++ {
		planned := server.planRouteOrder(CallContext{RequestID: fmt.Sprintf("req_weight_%d", index)}, routes)
		firstChoices[planned[0].Route.ID]++
	}
	share := float64(firstChoices["route_75"]) / 10_000
	if share < 0.73 || share > 0.77 {
		t.Fatalf("75:25 route share = %.4f, choices=%v", share, firstChoices)
	}
}

func TestProjectScopedRoutesSelectDifferentProviders(t *testing.T) {
	server := New(NewMemoryStore())
	privateProject := Project{ID: "prj_private", Status: StatusActive}
	publicProject := Project{ID: "prj_public", Status: StatusActive}
	routes := []RouteSelection{
		{
			Provider: Provider{ID: "prv_private"},
			Route: ModelRoute{
				ID: "route_private", Priority: 1, Weight: 100,
				ProjectScope: RouteProjectScopeInclude, ProjectIDs: []string{privateProject.ID},
			},
		},
		{
			Provider: Provider{ID: "prv_external"},
			Route: ModelRoute{
				ID: "route_external", Priority: 1, Weight: 100,
				ProjectScope: RouteProjectScopeExclude, ProjectIDs: []string{privateProject.ID},
			},
		},
	}

	privatePlan := server.planRouteOrder(CallContext{RequestID: "req_private", Project: privateProject}, routes)
	if len(privatePlan) != 1 || privatePlan[0].Provider.ID != "prv_private" {
		t.Fatalf("private project routes = %+v", privatePlan)
	}
	publicPlan := server.planRouteOrder(CallContext{RequestID: "req_public", Project: publicProject}, routes)
	if len(publicPlan) != 1 || publicPlan[0].Provider.ID != "prv_external" {
		t.Fatalf("public project routes = %+v", publicPlan)
	}
}

func TestAccessibleModelsRespectProjectRouteScope(t *testing.T) {
	store := NewMemoryStore()
	privateProject := store.CreateProject(Project{ID: "prj_catalog_private", Name: "Private", Status: StatusActive})
	publicProject := store.CreateProject(Project{ID: "prj_catalog_public", Name: "Public", Status: StatusActive})
	privateKey, _, err := store.CreateAPIKey(privateProject.ID, APIKey{ID: "key_catalog_private", Name: "Private", Status: StatusActive}, "thk_catalog_private")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := store.CreateAPIKey(publicProject.ID, APIKey{ID: "key_catalog_public", Name: "Public", Status: StatusActive}, "thk_catalog_public")
	if err != nil {
		t.Fatal(err)
	}
	model := store.AddModel(Model{Name: "private-only-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_catalog_private", Name: "Private", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ID: "route_catalog_private", ModelName: model.Name, ProviderID: provider.ID,
		ProviderModel: "private-upstream", Priority: 1, Weight: 100, Status: StatusActive,
		ProjectScope: RouteProjectScopeInclude, ProjectIDs: []string{privateProject.ID},
	})

	if !modelListContains(store.AccessibleModels(privateKey), model.Name) {
		t.Fatal("private project should see its scoped model")
	}
	if modelListContains(store.AccessibleModels(publicKey), model.Name) {
		t.Fatal("other projects must not see a model with no eligible route")
	}
}

func TestAdaptiveRoutingUsesPersistedLatencyAndSuccessRate(t *testing.T) {
	store := NewMemoryStore()
	providerFast := store.AddProvider(Provider{ID: "prv_adaptive_fast", Name: "Fast", Type: ProviderMock, Status: StatusActive, Healthy: true})
	providerSlow := store.AddProvider(Provider{ID: "prv_adaptive_slow", Name: "Slow", Type: ProviderMock, Status: StatusActive, Healthy: true})
	fastRoute := store.AddRoute(ModelRoute{ID: "route_adaptive_fast", ModelName: "adaptive-model", ProviderID: providerFast.ID, ProviderModel: "fast", Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyAdaptive})
	slowRoute := store.AddRoute(ModelRoute{ID: "route_adaptive_slow", ModelName: "adaptive-model", ProviderID: providerSlow.ID, ProviderModel: "slow", Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyAdaptive})

	for index := 0; index < 10; index++ {
		store.RecordRouteAttempts(fmt.Sprintf("req_adaptive_fast_%d", index), []RouteAttempt{{
			Selection: RouteSelection{Provider: providerFast, ProviderModel: fastRoute.ProviderModel, Route: fastRoute},
			Status:    http.StatusOK, Invoked: true, LatencyMS: 100,
		}})
		status := http.StatusOK
		if index >= 8 {
			status = http.StatusBadGateway
		}
		store.RecordRouteAttempts(fmt.Sprintf("req_adaptive_slow_%d", index), []RouteAttempt{{
			Selection: RouteSelection{Provider: providerSlow, ProviderModel: slowRoute.ProviderModel, Route: slowRoute},
			Status:    status, Invoked: true, LatencyMS: 400,
		}})
	}

	candidates, err := store.SelectRouteCandidates("adaptive-model")
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	firstChoices := map[string]int{}
	for index := 0; index < 4_000; index++ {
		planned := server.planRouteOrder(CallContext{RequestID: fmt.Sprintf("req_adaptive_choice_%d", index)}, candidates)
		firstChoices[planned[0].Route.ID]++
	}
	if firstChoices[fastRoute.ID] <= firstChoices[slowRoute.ID]*3 {
		t.Fatalf("adaptive routing did not favor the faster reliable route: %v", firstChoices)
	}
}

func TestAdaptiveRoutingPenalizesRouteWithOnlyFailures(t *testing.T) {
	store := NewMemoryStore()
	healthyProvider := store.AddProvider(Provider{ID: "prv_adaptive_healthy", Name: "Healthy", Type: ProviderMock, Status: StatusActive, Healthy: true})
	failingProvider := store.AddProvider(Provider{ID: "prv_adaptive_failing", Name: "Failing", Type: ProviderMock, Status: StatusActive, Healthy: true})
	healthyRoute := store.AddRoute(ModelRoute{ID: "route_adaptive_healthy", ModelName: "adaptive-failure-model", ProviderID: healthyProvider.ID, ProviderModel: "healthy", Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyAdaptive})
	failingRoute := store.AddRoute(ModelRoute{ID: "route_adaptive_failing", ModelName: "adaptive-failure-model", ProviderID: failingProvider.ID, ProviderModel: "failing", Priority: 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyAdaptive})

	for index := 0; index < 5; index++ {
		store.RecordRouteAttempts(fmt.Sprintf("req_adaptive_healthy_%d", index), []RouteAttempt{{
			Selection: RouteSelection{Provider: healthyProvider, ProviderModel: healthyRoute.ProviderModel, Route: healthyRoute},
			Status:    http.StatusOK, Invoked: true, LatencyMS: 100,
		}})
		store.RecordRouteAttempts(fmt.Sprintf("req_adaptive_failing_%d", index), []RouteAttempt{{
			Selection: RouteSelection{Provider: failingProvider, ProviderModel: failingRoute.ProviderModel, Route: failingRoute},
			Status:    http.StatusBadGateway, Invoked: true, LatencyMS: 50,
		}})
	}

	candidates, err := store.SelectRouteCandidates("adaptive-failure-model")
	if err != nil {
		t.Fatal(err)
	}
	planned := New(store).planRouteOrder(CallContext{RequestID: "req_adaptive_failure_choice"}, candidates)
	effectiveWeights := map[string]int{}
	samples := map[string]int64{}
	for _, candidate := range planned {
		effectiveWeights[candidate.Route.ID] = candidate.Runtime.EffectiveWeight
		samples[candidate.Route.ID] = candidate.Runtime.Samples
	}
	if samples[healthyRoute.ID] != 5 || samples[failingRoute.ID] != 5 {
		t.Fatalf("adaptive samples were not loaded for all routes: %v", samples)
	}
	if effectiveWeights[healthyRoute.ID] != 100 || effectiveWeights[failingRoute.ID] != 25 {
		t.Fatalf("unexpected adaptive effective weights: %v", effectiveWeights)
	}
}

func TestRoutePolicyValidatesProjectScope(t *testing.T) {
	server := New(NewMemoryStore())
	project := server.store.CreateProject(Project{ID: "prj_route_policy", Name: "Route Policy", Status: StatusActive})
	tests := []struct {
		name  string
		route ModelRoute
		code  string
	}{
		{name: "unknown scope", route: ModelRoute{Strategy: RouteStrategyPriorityWeighted, ProjectScope: "private_only"}, code: "invalid_route_project_scope"},
		{name: "missing projects", route: ModelRoute{Strategy: RouteStrategyPriorityWeighted, ProjectScope: RouteProjectScopeInclude}, code: "route_projects_required"},
		{name: "unknown project", route: ModelRoute{Strategy: RouteStrategyPriorityWeighted, ProjectScope: RouteProjectScopeExclude, ProjectIDs: []string{"prj_missing"}}, code: "route_project_not_found"},
		{name: "supported scope", route: ModelRoute{Strategy: RouteStrategyPriorityWeighted, ProjectScope: RouteProjectScopeInclude, ProjectIDs: []string{project.ID}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := server.validateRoutePolicy(test.route)
			if test.code == "" {
				if err != nil {
					t.Fatalf("expected valid route policy, got %v", err)
				}
				return
			}
			httpErr := AsHTTPError(err)
			if httpErr == nil || httpErr.Status != http.StatusBadRequest || httpErr.Code != test.code {
				t.Fatalf("expected %s error, got %#v", test.code, err)
			}
		})
	}
}

func TestAdminUpdatesWholeModelRoutingPolicyAtomically(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	modelName := "gpt-4.1-mini"
	var primaryRoute ModelRoute
	for _, route := range store.ListRoutes() {
		if route.ModelName == modelName {
			primaryRoute = route
			break
		}
	}
	if primaryRoute.ID == "" {
		t.Fatalf("expected seeded route for %s", modelName)
	}
	secondaryProvider := store.AddProvider(Provider{ID: "prv_policy_secondary", Name: "Policy Secondary", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ProviderID: secondaryProvider.ID, UpstreamModel: "mock-secondary", DisplayName: "Mock Secondary", Status: StatusActive})
	secondaryRoute := store.AddRoute(ModelRoute{
		ID: "route_policy_secondary", ModelName: modelName, ProviderID: secondaryProvider.ID,
		ProviderModel: "mock-secondary", Priority: 2, Weight: 100, QualityScore: 50, CostScore: 50,
		Status: StatusActive, Strategy: RouteStrategyBalanced,
	})
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/model-routing-policies/"+modelName, map[string]any{
		"strategy": RouteStrategyPriorityWeighted,
		"routes": []map[string]any{
			{"route_id": primaryRoute.ID, "weight": 75, "quality_score": 60, "cost_score": 40},
			{"route_id": secondaryRoute.ID, "weight": 25, "quality_score": 40, "cost_score": 60},
		},
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected model routing policy update 200, got %d: %s", updated.Code, updated.Body)
	}
	routes := modelRoutesForTest(store.ListRoutes(), modelName)
	if len(routes) != 2 {
		t.Fatalf("expected two model routes, got %+v", routes)
	}
	for _, route := range routes {
		if route.Strategy != RouteStrategyPriorityWeighted || route.Priority != 1 {
			t.Fatalf("weighted policy did not create one traffic pool: %+v", routes)
		}
	}
	weights := map[string]int{routes[0].ID: routes[0].Weight, routes[1].ID: routes[1].Weight}
	if weights[primaryRoute.ID] != 75 || weights[secondaryRoute.ID] != 25 {
		t.Fatalf("unexpected model route weights: %v", weights)
	}

	reordered := doJSON(t, app, http.MethodPatch, "/api/admin/model-routing-policies/"+modelName, map[string]any{
		"strategy": RouteStrategyPriorityOnly,
		"routes": []map[string]any{
			{"route_id": secondaryRoute.ID, "weight": 25, "quality_score": 40, "cost_score": 60},
			{"route_id": primaryRoute.ID, "weight": 75, "quality_score": 60, "cost_score": 40},
		},
	}, "")
	if reordered.Code != http.StatusOK {
		t.Fatalf("expected priority policy update 200, got %d: %s", reordered.Code, reordered.Body)
	}
	routes = modelRoutesForTest(store.ListRoutes(), modelName)
	if routes[0].ID != secondaryRoute.ID || routes[0].Priority != 1 || routes[1].ID != primaryRoute.ID || routes[1].Priority != 2 {
		t.Fatalf("priority policy did not preserve submitted order: %+v", routes)
	}

	invalid := doJSON(t, app, http.MethodPatch, "/api/admin/model-routing-policies/"+modelName, map[string]any{
		"strategy": RouteStrategyAdaptive,
		"routes": []map[string]any{
			{"route_id": secondaryRoute.ID, "weight": 25, "quality_score": 40, "cost_score": 60},
			{"route_id": "route_missing", "weight": 75, "quality_score": 60, "cost_score": 40},
		},
	}, "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid atomic policy update 400, got %d: %s", invalid.Code, invalid.Body)
	}
	routes = modelRoutesForTest(store.ListRoutes(), modelName)
	for _, route := range routes {
		if route.Strategy != RouteStrategyPriorityOnly {
			t.Fatalf("failed policy update changed route state: %+v", routes)
		}
	}
}

func modelRoutesForTest(routes []ModelRoute, modelName string) []ModelRoute {
	result := make([]ModelRoute, 0, len(routes))
	for _, route := range routes {
		if route.ModelName == modelName {
			result = append(result, route)
		}
	}
	return result
}

func modelListContains(models []Model, name string) bool {
	for _, model := range models {
		if model.Name == name {
			return true
		}
	}
	return false
}
