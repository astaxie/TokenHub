package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminModelRoutingMethodRoute struct {
	name           string
	path           string
	wrongMethod    string
	allow          string
	userWantStatus int
}

func TestAdminModelRoutingMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "model-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "model-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "model-routing-user",
		Name:     "Model Routing User",
		Email:    "model-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "model-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	modelsBefore := len(store.ListModels())
	routesBefore := len(store.ListRoutes())
	policiesBefore := len(store.ListResources(routingPolicyResourceKind))
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminModelRoutingMethodRoutes() {
		t.Run(route.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, userSession.Token)
			if route.userWantStatus == http.StatusMethodNotAllowed {
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
				return
			}
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}

	if got := len(store.ListModels()); got != modelsBefore {
		t.Fatalf("wrong methods changed models: got %d, want %d", got, modelsBefore)
	}
	if got := len(store.ListRoutes()); got != routesBefore {
		t.Fatalf("wrong methods changed routing rules: got %d, want %d", got, routesBefore)
	}
	if got := len(store.ListResources(routingPolicyResourceKind)); got != policiesBefore {
		t.Fatalf("wrong methods changed routing policies: got %d, want %d", got, policiesBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong methods wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminModelRoutingMethodRoutesCoverUnsupportedStandardMethods(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "model-routing-methods-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "model-routing-methods-password")
	for _, route := range adminModelRoutingMethodRoutes() {
		for _, method := range unsupportedAdminModelRoutingMethods(route.allow) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, adminToken)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
}

func TestAdminModelRoutingMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "model-routing-head-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "model-routing-head-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "model-routing-head-user",
		Name:     "Model Routing Head User",
		Email:    "model-routing-head-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "model-routing-head-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminModelRoutingMethodRoutes() {
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "ordinary_user", token: userSession.Token, wantStatus: http.StatusForbidden},
			{name: "admin", token: adminToken, wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
		} {
			t.Run(route.name+"/"+auth.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if auth.token != "" {
					request.Header.Set("authorization", "Bearer "+auth.token)
				}
				response, err := httpServer.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				assertRealHEADResponse(t, response, auth.wantStatus, auth.wantAllow, "application/json", true)
				_ = response.Body.Close()
			})
		}
	}
}

func TestAdminModelRoutingMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range adminModelRoutingMethodRoutes() {
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

func TestAdminModelRoutesPreserveStaticAndEscapedPathBoundaries(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "model-path-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "model-path-password")
	store.AddModel(Model{Name: "restore-defaults", Family: "reserved", Modality: "chat", Status: StatusActive})
	store.AddModel(Model{Name: "tenant/model", Family: "encoded", Modality: "chat", Status: StatusActive})

	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		response := methodRoutingJSONRequest(t, app, method, "/api/admin/models/restore-defaults", map[string]any{"family": "must-not-change"}, adminToken)
		assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		assertAllowHeader(t, response, http.MethodPost)
	}
	encodedStatic := methodRoutingRequest(app, http.MethodGet, "/api/admin/models/%72estore-defaults", adminToken)
	assertJSONError(t, encodedStatic, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, encodedStatic, http.MethodPost)
	reserved, ok := modelByNameForTest(store.ListModels(), "restore-defaults")
	if !ok || reserved.Family != "reserved" {
		t.Fatalf("reserved model was changed through the static route: %+v", reserved)
	}

	trailing := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/models/restore-defaults/", map[string]any{
		"family": "trailing-item", "modality": "chat", "status": StatusActive,
	}, adminToken)
	if trailing.Code != http.StatusOK {
		t.Fatalf("PATCH trailing restore-defaults: expected 200, got %d: %s", trailing.Code, trailing.Body.String())
	}
	encoded := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/models/tenant%2Fmodel", map[string]any{
		"family": "encoded-updated", "modality": "chat", "status": StatusActive,
	}, adminToken)
	if encoded.Code != http.StatusOK {
		t.Fatalf("PATCH encoded model: expected 200, got %d: %s", encoded.Code, encoded.Body.String())
	}
	raw := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/models/tenant/model", map[string]any{"family": "must-not-change"}, adminToken)
	assertJSONError(t, raw, http.StatusNotFound, "not_found")
	model, ok := modelByNameForTest(store.ListModels(), "tenant/model")
	if !ok || model.Family != "encoded-updated" {
		t.Fatalf("encoded model path behavior changed: %+v", model)
	}
}

func TestAdminModelRoutingPolicyPreservesEncodedModelNames(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "model-policy-path-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "model-policy-path-password")
	modelName := "tenant/policy-model"
	store.AddModel(Model{Name: modelName, Family: "policy", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_model_policy_encoded", Name: "Encoded Model Policy", Type: ProviderMock, Status: StatusActive, Healthy: true})
	route := store.AddRoute(ModelRoute{
		ID: "route_model_policy_encoded", ModelName: modelName, ProviderID: provider.ID,
		ProviderModel: "mock-chat", Priority: 1, Weight: 100, QualityScore: 50, CostScore: 50, Status: StatusActive,
	})
	payload := map[string]any{
		"strategy": RouteStrategyPriorityOnly,
		"routes":   []map[string]any{{"route_id": route.ID, "weight": 100, "quality_score": 50, "cost_score": 50}},
	}
	encoded := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/model-routing-policies/tenant%2Fpolicy-model", payload, adminToken)
	if encoded.Code != http.StatusOK {
		t.Fatalf("PATCH encoded model policy: expected 200, got %d: %s", encoded.Code, encoded.Body.String())
	}
	raw := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/model-routing-policies/tenant/policy-model", payload, adminToken)
	assertJSONError(t, raw, http.StatusNotFound, "not_found")
	assertModelRoutingAuditEvent(t, store.ListAuditEvents(), "update", "model_routing_policy", modelName)
}

func TestAdminRoutingPolicyAndRuleRoutesPreserveEscapedAndTrailingIDs(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "routing-path-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "routing-path-password")
	policy, err := store.CreateRoutingPolicy(scopedPolicyResource("tenant/policy", RoutingPolicyScopeUnbound, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	bound := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/routing-policies/tenant%2Fpolicy/bind", map[string]any{
		"scope": RoutingPolicyScopeGlobal,
	}, adminToken)
	if bound.Code != http.StatusOK {
		t.Fatalf("POST encoded policy bind: expected 200, got %d: %s", bound.Code, bound.Body.String())
	}
	unbound := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/routing-policies/tenant%2Fpolicy/unbind/", map[string]any{}, adminToken)
	if unbound.Code != http.StatusOK {
		t.Fatalf("POST trailing policy unbind: expected 200, got %d: %s", unbound.Code, unbound.Body.String())
	}
	unknown := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/routing-policies/"+policy.ID+"/unknown", map[string]any{}, adminToken)
	assertJSONError(t, unknown, http.StatusNotFound, "not_found")

	modelName := "routing-path-model"
	store.AddModel(Model{Name: modelName, Family: "routing", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_routing_path", Name: "Routing Path", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ID: "pmdl_routing_path", ProviderID: provider.ID, UpstreamModel: "mock-chat", DisplayName: "Mock Chat", Status: StatusActive})
	route := store.AddRoute(ModelRoute{
		ID: "tenant/route", ModelName: modelName, ProviderID: provider.ID,
		ProviderModel: "mock-chat", Priority: 1, Weight: 100, Status: StatusActive,
	})
	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/routing-rules/tenant%2Froute", map[string]any{"weight": 75}, adminToken)
	if patched.Code != http.StatusOK {
		t.Fatalf("PATCH encoded route: expected 200, got %d: %s", patched.Code, patched.Body.String())
	}
	patchedRaw := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/routing-rules/tenant/route/", map[string]any{"weight": 50}, adminToken)
	if patchedRaw.Code != http.StatusOK {
		t.Fatalf("PATCH raw trailing route: expected 200, got %d: %s", patchedRaw.Code, patchedRaw.Body.String())
	}
	explained := methodRoutingRequest(app, http.MethodGet, "/api/admin/routing-rules/tenant/route/explain?model="+modelName, adminToken)
	if explained.Code != http.StatusOK || !strings.Contains(explained.Body.String(), `"route_id":"`+route.ID+`"`) {
		t.Fatalf("GET raw route explain: status=%d body=%s", explained.Code, explained.Body.String())
	}
	assertModelRoutingAuditEvent(t, store.ListAuditEvents(), "bind", routingPolicyResourceKind, policy.ID)
	assertModelRoutingAuditEvent(t, store.ListAuditEvents(), "unbind", routingPolicyResourceKind, policy.ID)
	assertModelRoutingAuditEvent(t, store.ListAuditEvents(), "update", "routing_rule", route.ID)
}

func TestAdminModelAndRoutingRoutesPreserveSuccessfulMutationsAndAudits(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "model-routing-success-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "model-routing-success-password")

	createdModel := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name": "method-routing-model", "family": "routing", "modality": "chat", "status": StatusActive,
		"metadata": map[string]string{"title": "Temporary title"},
	}, adminToken)
	if createdModel.Code != http.StatusCreated {
		t.Fatalf("POST model: expected 201, got %d: %s", createdModel.Code, createdModel.Body.String())
	}
	patchedModel := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/models/method-routing-model", map[string]any{
		"family": "routing-updated", "modality": "chat", "status": StatusActive, "metadata": map[string]string{},
	}, adminToken)
	if patchedModel.Code != http.StatusOK {
		t.Fatalf("PATCH model: expected 200, got %d: %s", patchedModel.Code, patchedModel.Body.String())
	}
	modelFound := false
	for _, model := range store.ListModels() {
		if model.Name == "method-routing-model" {
			modelFound = true
		}
		if model.Name == "method-routing-model" && model.Metadata["title"] != "" {
			t.Fatalf("PATCH model retained the cleared display name: %#v", model.Metadata)
		}
	}
	if !modelFound {
		t.Fatal("PATCH model removed the model")
	}
	store.AddModel(Model{Name: "method-routing-delete", Family: "routing", Modality: "chat", Status: StatusActive})
	deletedModel := methodRoutingRequest(app, http.MethodDelete, "/api/admin/models/method-routing-delete", adminToken)
	if deletedModel.Code != http.StatusNoContent {
		t.Fatalf("DELETE model: expected 204, got %d: %s", deletedModel.Code, deletedModel.Body.String())
	}

	provider := store.AddProvider(Provider{ID: "prv_method_routing", Name: "Method Routing", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{
		ID: "pmdl_method_routing", ProviderID: provider.ID, UpstreamModel: "method-routing-upstream",
		DisplayName: "Method Routing Upstream", Status: StatusActive,
	})
	createdRoute := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": "method-routing-model", "provider_id": provider.ID,
		"provider_model": "method-routing-upstream", "status": StatusActive,
	}, adminToken)
	if createdRoute.Code != http.StatusCreated {
		t.Fatalf("POST routing rule: expected 201, got %d: %s", createdRoute.Code, createdRoute.Body.String())
	}
	var route ModelRoute
	if err := json.Unmarshal(createdRoute.Body.Bytes(), &route); err != nil {
		t.Fatal(err)
	}
	patchedRoute := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{
		"weight": 60,
	}, adminToken)
	if patchedRoute.Code != http.StatusOK {
		t.Fatalf("PATCH routing rule: expected 200, got %d: %s", patchedRoute.Code, patchedRoute.Body.String())
	}
	policy := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/model-routing-policies/method-routing-model", map[string]any{
		"strategy": RouteStrategyPriorityOnly,
		"routes":   []map[string]any{{"route_id": route.ID, "weight": 100, "quality_score": 50, "cost_score": 50}},
	}, adminToken)
	if policy.Code != http.StatusOK {
		t.Fatalf("PATCH model routing policy: expected 200, got %d: %s", policy.Code, policy.Body.String())
	}
	explained := methodRoutingRequest(app, http.MethodGet, "/api/admin/routing-rules/route-does-not-need-to-exist/explain?model=method-routing-model", adminToken)
	if explained.Code != http.StatusOK || !strings.Contains(explained.Body.String(), route.ID) {
		t.Fatalf("GET routing explain: status=%d body=%s", explained.Code, explained.Body.String())
	}
	deletedRoute := methodRoutingRequest(app, http.MethodDelete, "/api/admin/routing-rules/"+route.ID, adminToken)
	if deletedRoute.Code != http.StatusNoContent {
		t.Fatalf("DELETE routing rule: expected 204, got %d: %s", deletedRoute.Code, deletedRoute.Body.String())
	}

	for _, want := range []struct {
		action       string
		resourceType string
		resourceID   string
	}{
		{action: "create", resourceType: "model", resourceID: "method-routing-model"},
		{action: "update", resourceType: "model", resourceID: "method-routing-model"},
		{action: "delete", resourceType: "model", resourceID: "method-routing-delete"},
		{action: "create", resourceType: "routing_rule", resourceID: route.ID},
		{action: "update", resourceType: "routing_rule", resourceID: route.ID},
		{action: "delete", resourceType: "routing_rule", resourceID: route.ID},
		{action: "update", resourceType: "model_routing_policy", resourceID: "method-routing-model"},
	} {
		assertModelRoutingAuditEvent(t, store.ListAuditEvents(), want.action, want.resourceType, want.resourceID)
	}
}

func adminModelRoutingMethodRoutes() []adminModelRoutingMethodRoute {
	return []adminModelRoutingMethodRoute{
		{name: "models", path: "/api/admin/models", wrongMethod: http.MethodPut, allow: "GET, POST", userWantStatus: http.StatusForbidden},
		{name: "model_item", path: "/api/admin/models/model-missing", wrongMethod: http.MethodGet, allow: "PATCH, DELETE", userWantStatus: http.StatusMethodNotAllowed},
		{name: "model_routing_policy", path: "/api/admin/model-routing-policies/model-missing", wrongMethod: http.MethodGet, allow: http.MethodPatch, userWantStatus: http.StatusForbidden},
		{name: "routing_policy_bind", path: "/api/admin/routing-policies/policy-missing/bind", wrongMethod: http.MethodGet, allow: http.MethodPost, userWantStatus: http.StatusForbidden},
		{name: "routing_policy_unbind", path: "/api/admin/routing-policies/policy-missing/unbind", wrongMethod: http.MethodGet, allow: http.MethodPost, userWantStatus: http.StatusForbidden},
		{name: "routing_rules", path: "/api/admin/routing-rules", wrongMethod: http.MethodPut, allow: "GET, POST", userWantStatus: http.StatusForbidden},
		{name: "routing_rule_item", path: "/api/admin/routing-rules/route-missing", wrongMethod: http.MethodGet, allow: "PATCH, DELETE", userWantStatus: http.StatusForbidden},
		{name: "routing_rule_explain", path: "/api/admin/routing-rules/route-missing/explain?model=gpt-4.1-mini", wrongMethod: http.MethodPost, allow: http.MethodGet, userWantStatus: http.StatusForbidden},
	}
}

func unsupportedAdminModelRoutingMethods(allow string) []string {
	allowed := map[string]bool{}
	for _, method := range strings.Split(allow, ",") {
		allowed[strings.TrimSpace(method)] = true
	}
	methods := make([]string, 0, 5)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if !allowed[method] {
			methods = append(methods, method)
		}
	}
	return methods
}

func assertModelRoutingAuditEvent(t *testing.T, events []AuditEvent, action string, resourceType string, resourceID string) {
	t.Helper()
	for _, event := range events {
		if event.Action == action && event.ResourceType == resourceType && event.ResourceID == resourceID && event.Status == "success" {
			return
		}
	}
	t.Fatalf("missing successful %s audit for %s %q", action, resourceType, resourceID)
}
