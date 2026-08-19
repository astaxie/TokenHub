package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type adminResourceMethodRoute struct {
	name        string
	path        string
	wrongMethod string
	allow       string
}

func TestAdminResourceMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "resource-routing-user",
		Name:     "Resource Routing User",
		Email:    "resource-routing-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "resource-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	resourcesBefore := len(store.ListResources("teams"))
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminResourceMethodRoutes() {
		t.Run(route.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, userSession.Token)
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}

	if got := len(store.ListResources("teams")); got != resourcesBefore {
		t.Fatalf("wrong methods changed team resources: got %d, want %d", got, resourcesBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong methods wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminResourceMethodRoutesPreserveReadAuthorizedWrongMethods(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-read-order-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "resource-routing-read-user",
		Name:     "Resource Routing Read User",
		Email:    "resource-routing-read-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "resource-routing-read-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	for _, route := range []adminResourceMethodRoute{
		{name: "invoice_item", path: "/api/admin/resources/invoices/invoice-missing", wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "invoice_confirm", path: "/api/admin/resources/invoices/invoice-missing/confirm", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "invoice_reject", path: "/api/admin/resources/invoices/invoice-missing/reject", wrongMethod: http.MethodGet, allow: http.MethodPost},
	} {
		t.Run(route.name, func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, userSession.Token)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}
}

func TestAdminResourceMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "resource-routing-methods-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-methods-password")
	for _, route := range adminResourceMethodRoutes() {
		for _, method := range unsupportedAdminResourceMethods(route.allow) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, adminToken)
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
}

func TestAdminResourceMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-head-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-head-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username: "resource-routing-head-user",
		Name:     "Resource Routing Head User",
		Email:    "resource-routing-head-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "resource-routing-head-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, userSession, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminResourceMethodRoutes() {
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

func TestAdminResourceMethodRoutesRejectTrailingSlashRealHEAD(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "resource-routing-trailing-head-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-trailing-head-password")
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range []adminResourceMethodRoute{
		{name: "collection", path: "/api/admin/resources/teams/", allow: "GET, POST"},
		{name: "item", path: "/api/admin/resources/teams/team-missing/", allow: "PATCH, DELETE"},
		{name: "invoice_confirm", path: "/api/admin/resources/invoices/invoice-missing/confirm/", allow: http.MethodPost},
		{name: "invoice_reject", path: "/api/admin/resources/invoices/invoice-missing/reject/", allow: http.MethodPost},
		{name: "monitor_run", path: "/api/admin/resources/monitors/monitor-missing/run/", allow: http.MethodPost},
	} {
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
			assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, route.allow, "application/json", true)
			_ = response.Body.Close()
		})
	}
}

func TestAdminResourceMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range adminResourceMethodRoutes() {
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

func TestAdminResourceMethodRoutesPreserveTrailingSlashMethodRejection(t *testing.T) {
	_, app := newMethodRoutingAdminServer(t, "resource-routing-trailing-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-trailing-password")
	for _, route := range []adminResourceMethodRoute{
		{name: "collection", path: "/api/admin/resources/teams/", wrongMethod: http.MethodPut, allow: "GET, POST"},
		{name: "item", path: "/api/admin/resources/teams/team-missing/", wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "invoice_action", path: "/api/admin/resources/invoices/invoice-missing/confirm/", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "monitor_action", path: "/api/admin/resources/monitors/monitor-missing/run/", wrongMethod: http.MethodGet, allow: http.MethodPost},
	} {
		t.Run(route.name, func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, adminToken)
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}
}

func TestAdminSettingsMethodRoutesPreserveValidationOnTrailingPaths(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-settings-password")
	setting := store.ListResources("settings")[0]
	setting.Fields[syntheticDNSEnabledField] = true
	setting.Fields[syntheticDNSCIDRsField] = "169.254.0.0/16"
	path := "/api/admin/resources/settings/" + gatewaySettingsID

	wrongMethod := methodRoutingRequest(app, http.MethodGet, path+"/", "dev_admin_token")
	assertJSONError(t, wrongMethod, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, wrongMethod, http.MethodPatch+", "+http.MethodDelete)

	patch := methodRoutingJSONRequest(t, app, http.MethodPatch, path+"/", setting, "dev_admin_token")
	if patch.Code != http.StatusBadRequest || !strings.Contains(patch.Body.String(), "provider_synthetic_dns_cidrs_not_allowed") {
		t.Fatalf("trailing settings patch bypassed Synthetic DNS validation: status=%d body=%s", patch.Code, patch.Body.String())
	}

	collection := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/settings/", setting, "dev_admin_token")
	if collection.Code != http.StatusBadRequest || !strings.Contains(collection.Body.String(), "provider_synthetic_dns_cidrs_not_allowed") {
		t.Fatalf("trailing settings create bypassed Synthetic DNS validation: status=%d body=%s", collection.Code, collection.Body.String())
	}
}

func TestAdminResourceMethodRoutesPreserveSuccessfulOperationsAndAudits(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-success-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-success-password")
	auditsBefore := len(store.ListAuditEvents())
	store.AddProvider(Provider{ID: "prv_method_routing", Name: "Method Routing Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})

	created := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/teams", map[string]any{
		"name": "Method Routing Team",
	}, adminToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create team: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var team AdminResource
	if err := json.NewDecoder(created.Body).Decode(&team); err != nil {
		t.Fatal(err)
	}
	if team.ID == "" {
		t.Fatalf("created team has no ID: %+v", team)
	}

	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/resources/teams/"+team.ID, map[string]any{
		"name": "Method Routing Team Updated",
	}, adminToken)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), "Method Routing Team Updated") {
		t.Fatalf("patch team: expected updated team, got %d: %s", patched.Code, patched.Body.String())
	}

	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/resources/teams/"+team.ID, adminToken)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete team: expected 204, got %d: %s", deleted.Code, deleted.Body.String())
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore+3 {
		t.Fatalf("resource mutations wrote %d audits, want %d", got-auditsBefore, 3)
	}

	monitors := methodRoutingRequest(app, http.MethodGet, "/api/admin/resources/monitors", adminToken)
	if monitors.Code != http.StatusOK || len(store.ListResources("monitors")) == 0 {
		t.Fatalf("monitor discovery changed: status=%d body=%s", monitors.Code, monitors.Body.String())
	}
	alertRules := methodRoutingRequest(app, http.MethodGet, "/api/admin/resources/alert-rules", adminToken)
	if alertRules.Code != http.StatusOK || len(store.ListResources("alert-rules")) == 0 {
		t.Fatalf("alert rule discovery changed: status=%d body=%s", alertRules.Code, alertRules.Body.String())
	}
}

func TestAdminResourceActionRoutesPreserveSuccessAndPathBoundaries(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-action-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-action-password")
	auditsBefore := len(store.ListAuditEvents())
	provider := store.AddProvider(Provider{ID: "prv_method_routing", Name: "Method Routing Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_method_routing",
		ProviderID:   provider.ID,
		Name:         "Method Routing Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
	}); err != nil {
		t.Fatal(err)
	}
	invoice := store.CreateResource("invoices", AdminResource{
		ID:     "inv_method_routing",
		Name:   "Method Routing Invoice",
		Status: "pending",
		Fields: map[string]any{"period": "2026-08", "cost_center": "CC-ROUTING", "amount_usd": 1.25},
	})
	confirmed := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{
		"invoice_note": "routed by method",
	}, adminToken)
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirm invoice: expected confirmed response, got %d: %s", confirmed.Code, confirmed.Body.String())
	}

	monitor := store.CreateResource("monitors", AdminResource{
		ID:     "mon_method_routing",
		Name:   "Method Routing Monitor",
		Status: StatusActive,
		Fields: map[string]any{"target_type": "resource", "provider_resource_id": "rsrc_method_routing"},
	})
	run := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, adminToken)
	if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), `"status":"ok"`) {
		t.Fatalf("run monitor: expected success, got %d: %s", run.Code, run.Body.String())
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore+2 {
		t.Fatalf("resource actions wrote %d audits, want %d", got-auditsBefore, 2)
	}

	store.CreateResource("teams", AdminResource{ID: "team/method-routing", Name: "Encoded Team", Status: StatusActive})
	encoded := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/resources/teams/team%2Fmethod-routing", map[string]any{"name": "Encoded Team Updated"}, adminToken)
	if encoded.Code != http.StatusOK || !strings.Contains(encoded.Body.String(), "Encoded Team Updated") {
		t.Fatalf("patch encoded resource ID: status=%d body=%s", encoded.Code, encoded.Body.String())
	}
	trailing := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/resources/teams/team%2Fmethod-routing/", map[string]any{"name": "Trailing Team Updated"}, adminToken)
	if trailing.Code != http.StatusOK || !strings.Contains(trailing.Body.String(), "Trailing Team Updated") {
		t.Fatalf("patch trailing resource ID: status=%d body=%s", trailing.Code, trailing.Body.String())
	}

	for _, path := range []string{
		"/api/admin/resources/invoices/" + invoice.ID + "/unknown",
		"/api/admin/resources/teams/team/method-routing",
		"/api/admin/resources/" + openAIAccountQuotaResetOperationKind,
	} {
		response := methodRoutingRequest(app, http.MethodPost, path, adminToken)
		assertJSONError(t, response, http.StatusNotFound, "not_found")
		assertAllowHeader(t, response, "")
	}
}

func TestAdminResourceActionRoutesPreserveEscapedAndTrailingIDs(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "resource-routing-escaped-action-password")
	adminToken, _ := loginMethodRoutingAdmin(t, app, "resource-routing-escaped-action-password")
	provider := store.AddProvider(Provider{ID: "prv_escaped_actions", Name: "Escaped Action Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_escaped_actions",
		ProviderID:   provider.ID,
		Name:         "Escaped Action Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
	}); err != nil {
		t.Fatal(err)
	}
	auditsBefore := len(store.ListAuditEvents())

	store.CreateResource("invoices", AdminResource{ID: "inv/confirm", Name: "Escaped Confirm Invoice", Status: "pending"})
	confirmed := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/invoices/inv%2Fconfirm/confirm", map[string]any{}, adminToken)
	if confirmed.Code != http.StatusOK || !strings.Contains(confirmed.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirm encoded invoice ID: status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}

	store.CreateResource("invoices", AdminResource{ID: "inv/reject", Name: "Escaped Reject Invoice", Status: "pending"})
	rejected := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/invoices/inv%2Freject/reject/", map[string]any{}, adminToken)
	if rejected.Code != http.StatusOK || !strings.Contains(rejected.Body.String(), `"status":"rejected"`) {
		t.Fatalf("reject trailing encoded invoice ID: status=%d body=%s", rejected.Code, rejected.Body.String())
	}

	store.CreateResource("monitors", AdminResource{
		ID:     "mon/encoded",
		Name:   "Encoded Monitor",
		Status: StatusActive,
		Fields: map[string]any{"target_type": "resource", "provider_resource_id": "rsrc_escaped_actions"},
	})
	encodedRun := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/monitors/mon%2Fencoded/run", map[string]any{}, adminToken)
	if encodedRun.Code != http.StatusOK || !strings.Contains(encodedRun.Body.String(), `"status":"ok"`) {
		t.Fatalf("run encoded monitor ID: status=%d body=%s", encodedRun.Code, encodedRun.Body.String())
	}

	store.CreateResource("monitors", AdminResource{
		ID:     "mon/trailing",
		Name:   "Trailing Monitor",
		Status: StatusActive,
		Fields: map[string]any{"target_type": "resource", "provider_resource_id": "rsrc_escaped_actions"},
	})
	trailingRun := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/resources/monitors/mon%2Ftrailing/run/", map[string]any{}, adminToken)
	if trailingRun.Code != http.StatusOK || !strings.Contains(trailingRun.Body.String(), `"status":"ok"`) {
		t.Fatalf("run trailing encoded monitor ID: status=%d body=%s", trailingRun.Code, trailingRun.Body.String())
	}

	if got := len(store.ListAuditEvents()); got != auditsBefore+4 {
		t.Fatalf("encoded and trailing actions wrote %d audits, want %d", got-auditsBefore, 4)
	}
}

func adminResourceMethodRoutes() []adminResourceMethodRoute {
	return []adminResourceMethodRoute{
		{name: "collection", path: "/api/admin/resources/teams", wrongMethod: http.MethodPut, allow: "GET, POST"},
		{name: "item", path: "/api/admin/resources/teams/team-missing", wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "settings_item", path: "/api/admin/resources/settings/" + gatewaySettingsID, wrongMethod: http.MethodGet, allow: "PATCH, DELETE"},
		{name: "invoice_confirm", path: "/api/admin/resources/invoices/invoice-missing/confirm", wrongMethod: http.MethodPut, allow: http.MethodPost},
		{name: "invoice_reject", path: "/api/admin/resources/invoices/invoice-missing/reject", wrongMethod: http.MethodPut, allow: http.MethodPost},
		{name: "monitor_run", path: "/api/admin/resources/monitors/monitor-missing/run", wrongMethod: http.MethodPut, allow: http.MethodPost},
	}
}

func unsupportedAdminResourceMethods(allow string) []string {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodTrace, http.MethodConnect}
	unsupported := make([]string, 0, len(methods))
	for _, method := range methods {
		if !strings.Contains(","+strings.ReplaceAll(allow, " ", "")+",", ","+method+",") {
			unsupported = append(unsupported, method)
		}
	}
	return unsupported
}
