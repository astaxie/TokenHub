package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type adminBillingMethodRoute struct {
	name    string
	path    string
	allowed []string
	allow   string
}

func TestAdminBillingMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingBillingServer(t, "billing-routing-authorization-password")
	ordinaryToken := createAdminOperationMethodRoutingSession(t, store, "billing-routing-user", "user")
	securityToken := createAdminOperationMethodRoutingSession(t, store, "billing-routing-security", "security_admin")
	auditsBefore := len(store.ListAuditEvents())

	for _, route := range adminBillingMethodRoutes() {
		wrongMethod := unsupportedAdminBillingMethodRoutes(route)[0]
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantCode   string
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized, wantCode: "invalid_admin_token"},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusForbidden, wantCode: "admin_forbidden"},
			{name: "admin", token: "dev_admin_token", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: route.allow},
		} {
			t.Run(route.name+"/"+auth.name, func(t *testing.T) {
				response := methodRoutingRequest(app, wrongMethod, route.path, auth.token)
				assertJSONError(t, response, auth.wantStatus, auth.wantCode)
				assertAllowHeader(t, response, auth.wantAllow)
			})
		}
	}

	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("wrong methods wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminBillingMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	store, app := newMethodRoutingBillingServer(t, "billing-routing-methods-password")
	auditsBefore := len(store.ListAuditEvents())
	for _, route := range adminBillingMethodRoutes() {
		for _, method := range unsupportedAdminBillingMethodRoutes(route) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, "dev_admin_token")
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore {
		t.Fatalf("unsupported methods wrote audit events: got %d, want %d", got, auditsBefore)
	}
}

func TestAdminBillingMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingBillingServer(t, "billing-routing-head-password")
	ordinaryToken := createAdminOperationMethodRoutingSession(t, store, "billing-routing-head-user", "user")
	securityToken := createAdminOperationMethodRoutingSession(t, store, "billing-routing-head-security", "security_admin")
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminBillingMethodRoutes() {
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden},
			{name: "security_admin", token: securityToken, wantStatus: http.StatusForbidden},
			{name: "admin", token: "dev_admin_token", wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
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

func TestAdminBillingMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, app := newMethodRoutingBillingServer(t, "billing-routing-cors-password")
	for _, route := range adminBillingMethodRoutes() {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.allowed[0])
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

func TestAdminBillingMethodRoutesPreservePathBoundaries(t *testing.T) {
	_, app := newMethodRoutingBillingServer(t, "billing-routing-boundaries-password")

	unauthorizedBlank := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/connectors/", "")
	assertJSONError(t, unauthorizedBlank, http.StatusUnauthorized, "invalid_admin_token")
	assertAllowHeader(t, unauthorizedBlank, "")

	blank := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/connectors/", "dev_admin_token")
	assertJSONError(t, blank, http.StatusNotFound, "billing_connector_not_found")
	assertAllowHeader(t, blank, "")

	unknownAction := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/connectors/missing/unknown", "dev_admin_token")
	assertJSONError(t, unknownAction, http.StatusNotFound, "billing_action_not_found")
	assertAllowHeader(t, unknownAction, "")

	unknownActionWrongMethod := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/connectors/missing/unknown", "dev_admin_token")
	assertJSONError(t, unknownActionWrongMethod, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, unknownActionWrongMethod, http.MethodPost)

	extraSegment := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/connectors/missing/test/extra", "dev_admin_token")
	assertJSONError(t, extraSegment, http.StatusNotFound, "billing_connector_not_found")
	assertAllowHeader(t, extraSegment, "")

	trailingItem := methodRoutingRequest(app, http.MethodPost, "/api/admin/billing/connectors/missing/", "dev_admin_token")
	assertJSONError(t, trailingItem, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, trailingItem, "GET, PATCH, DELETE")

	trailingAction := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/connectors/missing/test/", "dev_admin_token")
	assertJSONError(t, trailingAction, http.StatusMethodNotAllowed, "method_not_allowed")
	assertAllowHeader(t, trailingAction, http.MethodPost)
}

func TestAdminBillingMethodRoutesPreserveDynamicPayloadValidation(t *testing.T) {
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.SecretKey = "billing-method-routing-validation-secret"
	config.MaxJSONRequestBytes = 1 << 10
	store := NewMemoryStoreWithConfig(config)
	connector, err := store.CreateBillingConnector(BillingConnector{
		ID:      "billing-validation",
		Name:    "Billing Validation Connector",
		Type:    BillingConnectorOneAPI,
		BaseURL: "https://billing.example.test",
		Status:  StatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, config).Handler()
	itemPath := "/api/admin/billing/connectors/" + connector.ID

	for _, test := range []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "patch_malformed_json",
			method:     http.MethodPatch,
			path:       itemPath,
			body:       "{",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_billing_connector",
		},
		{
			name:       "patch_payload_too_large",
			method:     http.MethodPatch,
			path:       itemPath,
			body:       `{"name":"` + strings.Repeat("x", 4096) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "payload_too_large",
		},
		{
			name:       "sync_malformed_json",
			method:     http.MethodPost,
			path:       itemPath + "/sync",
			body:       "{",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_billing_sync",
		},
		{
			name:       "sync_payload_too_large",
			method:     http.MethodPost,
			path:       itemPath + "/sync",
			body:       `{"from":"` + strings.Repeat("x", 4096) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "payload_too_large",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := billingMethodRoutingBodyRequest(app, test.method, test.path, test.body, "dev_admin_token")
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, "")
		})
	}
}

func TestAdminBillingMethodRoutesPreserveSuccessfulOperationsAndSideEffects(t *testing.T) {
	createdAt := time.Now().UTC().Add(-30 * time.Minute)
	upstream := newBillingMethodRoutingUpstream(t, createdAt)
	store, app := newMethodRoutingBillingServer(t, "billing-routing-success-password")
	auditsBefore := len(store.ListAuditEvents())

	createdResponse := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Method Routing Connector",
		"type":     BillingConnectorOneAPI,
		"base_url": upstream.URL,
		"config": map[string]string{
			"quota_per_unit": "500000",
			"max_retries":    "1",
			"retry_base_ms":  "1",
		},
		"credentials": map[string]string{"api_token": "billing-routing-secret"},
	}, "dev_admin_token")
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create connector: status=%d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	if strings.Contains(createdResponse.Body.String(), "billing-routing-secret") {
		t.Fatalf("create response exposed credentials: %s", createdResponse.Body.String())
	}
	var connector BillingConnector
	if err := json.NewDecoder(createdResponse.Body).Decode(&connector); err != nil {
		t.Fatal(err)
	}

	listed := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/connectors", "dev_admin_token")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), connector.ID) {
		t.Fatalf("list connectors: status=%d body=%s", listed.Code, listed.Body.String())
	}
	item := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/connectors/"+connector.ID, "dev_admin_token")
	if item.Code != http.StatusOK || !strings.Contains(item.Body.String(), connector.ID) {
		t.Fatalf("get connector: status=%d body=%s", item.Code, item.Body.String())
	}
	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, "/api/admin/billing/connectors/"+connector.ID, map[string]any{"name": "Method Routing Connector Updated"}, "dev_admin_token")
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), "Method Routing Connector Updated") {
		t.Fatalf("patch connector: status=%d body=%s", patched.Code, patched.Body.String())
	}

	tested := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/connectors/"+connector.ID+"/test", map[string]any{}, "dev_admin_token")
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"ok":true`) {
		t.Fatalf("test connector: status=%d body=%s", tested.Code, tested.Body.String())
	}
	synced := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/billing/connectors/"+connector.ID+"/sync", map[string]any{}, "dev_admin_token")
	if synced.Code != http.StatusOK || !strings.Contains(synced.Body.String(), `"status":"succeeded"`) {
		t.Fatalf("sync connector: status=%d body=%s", synced.Code, synced.Body.String())
	}

	records := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/records?connector_id="+url.QueryEscape(connector.ID)+"&limit=1", "dev_admin_token")
	if records.Code != http.StatusOK || !strings.Contains(records.Body.String(), connector.ID) || strings.Contains(records.Body.String(), "raw_payload") {
		t.Fatalf("list billing records: status=%d body=%s", records.Code, records.Body.String())
	}
	runs := methodRoutingRequest(app, http.MethodGet, "/api/admin/billing/sync-runs?connector_id="+url.QueryEscape(connector.ID)+"&limit=1", "dev_admin_token")
	if runs.Code != http.StatusOK || !strings.Contains(runs.Body.String(), connector.ID) || !strings.Contains(runs.Body.String(), `"trigger":"manual"`) {
		t.Fatalf("list billing sync runs: status=%d body=%s", runs.Code, runs.Body.String())
	}

	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/billing/connectors/"+connector.ID, "dev_admin_token")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete connector: status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if len(store.ListBillingRecords(connector.ID, 10)) != 1 || len(store.ListBillingSyncRuns(connector.ID, 10)) != 1 {
		t.Fatal("deleting the connector removed billing records or sync history")
	}
	audits := store.ListAuditEvents()
	if len(audits) != auditsBefore+5 {
		t.Fatalf("billing operations wrote %d audits, want 5", len(audits)-auditsBefore)
	}
	for _, action := range []string{"create", "update", "test", "sync", "delete"} {
		if !hasMethodRoutingAudit(audits, action, connector.ID) {
			t.Fatalf("missing %s audit for connector %s", action, connector.ID)
		}
	}
}

func TestAdminBillingMethodRoutesPreserveEncodedAndTrailingIDs(t *testing.T) {
	upstream := newBillingMethodRoutingUpstream(t, time.Now().UTC().Add(-30*time.Minute))
	store, app := newMethodRoutingBillingServer(t, "billing-routing-encoded-password")
	connector, err := store.CreateBillingConnector(BillingConnector{
		ID:      "billing/encoded",
		Name:    "Encoded Billing Connector",
		Type:    BillingConnectorOneAPI,
		BaseURL: upstream.URL,
		Status:  StatusActive,
		Config:  map[string]string{"max_retries": "1", "retry_base_ms": "1"},
		Credentials: map[string]string{
			"api_token": "encoded-billing-token",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedID := strings.ReplaceAll(url.PathEscape(connector.ID), "/", "%2F")
	itemPath := "/api/admin/billing/connectors/" + encodedID

	item := methodRoutingRequest(app, http.MethodGet, itemPath, "dev_admin_token")
	if item.Code != http.StatusOK || !strings.Contains(item.Body.String(), connector.ID) {
		t.Fatalf("get encoded connector: status=%d body=%s", item.Code, item.Body.String())
	}
	trailingItem := methodRoutingRequest(app, http.MethodGet, itemPath+"/", "dev_admin_token")
	if trailingItem.Code != http.StatusOK || !strings.Contains(trailingItem.Body.String(), connector.ID) {
		t.Fatalf("get trailing encoded connector: status=%d body=%s", trailingItem.Code, trailingItem.Body.String())
	}
	patched := methodRoutingJSONRequest(t, app, http.MethodPatch, itemPath+"/", map[string]any{"name": "Encoded Billing Connector Updated"}, "dev_admin_token")
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), "Encoded Billing Connector Updated") {
		t.Fatalf("patch trailing encoded connector: status=%d body=%s", patched.Code, patched.Body.String())
	}
	tested := methodRoutingJSONRequest(t, app, http.MethodPost, itemPath+"/test/", map[string]any{}, "dev_admin_token")
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"ok":true`) {
		t.Fatalf("test trailing encoded connector: status=%d body=%s", tested.Code, tested.Body.String())
	}
}

func adminBillingMethodRoutes() []adminBillingMethodRoute {
	return []adminBillingMethodRoute{
		{name: "connector_collection", path: "/api/admin/billing/connectors", allowed: []string{http.MethodGet, http.MethodPost}, allow: "GET, POST"},
		{name: "connector_item", path: "/api/admin/billing/connectors/missing", allowed: []string{http.MethodGet, http.MethodPatch, http.MethodDelete}, allow: "GET, PATCH, DELETE"},
		{name: "connector_test", path: "/api/admin/billing/connectors/missing/test", allowed: []string{http.MethodPost}, allow: http.MethodPost},
		{name: "connector_sync", path: "/api/admin/billing/connectors/missing/sync", allowed: []string{http.MethodPost}, allow: http.MethodPost},
		{name: "billing_records", path: "/api/admin/billing/records", allowed: []string{http.MethodGet}, allow: http.MethodGet},
		{name: "billing_sync_runs", path: "/api/admin/billing/sync-runs", allowed: []string{http.MethodGet}, allow: http.MethodGet},
	}
}

func unsupportedAdminBillingMethodRoutes(route adminBillingMethodRoute) []string {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodTrace, http.MethodConnect}
	unsupported := make([]string, 0, len(methods))
	for _, method := range methods {
		allowed := false
		for _, candidate := range route.allowed {
			if candidate == method {
				allowed = true
				break
			}
		}
		if !allowed {
			unsupported = append(unsupported, method)
		}
	}
	return unsupported
}

func billingMethodRoutingBodyRequest(handler http.Handler, method string, path string, body string, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("content-type", "application/json")
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func newMethodRoutingBillingServer(t *testing.T, password string) (*GormStore, http.Handler) {
	t.Helper()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = password
	config.SecretKey = "billing-method-routing-secret"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	return store, NewWithConfig(store, config).Handler()
}

func newBillingMethodRoutingUpstream(t *testing.T, createdAt time.Time) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("authorization") != "Bearer billing-routing-secret" && r.Header.Get("authorization") != "Bearer encoded-billing-token" {
			http.Error(w, "invalid billing request", http.StatusUnauthorized)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"billing-method-route-record","created_at":` + strconv.FormatInt(createdAt.Unix(), 10) + `,"model_name":"deepseek-chat","quota":500000}],"total":1}}`))
	}))
	t.Cleanup(upstream.Close)
	return upstream
}
