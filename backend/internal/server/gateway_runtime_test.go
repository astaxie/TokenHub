package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMonitorRunUpdatesResourceAndCreatesAlert(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	monitor := store.CreateResource("monitors", AdminResource{
		Name:   "Mock resource monitor",
		Status: StatusActive,
		Fields: map[string]any{
			"target_type":          "resource",
			"provider_resource_id": "rsrc_mock_primary",
		},
	})
	app := New(store).Handler()

	okRun := doJSON(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, "")
	if okRun.Code != http.StatusOK || !strings.Contains(okRun.Body, `"status":"ok"`) {
		t.Fatalf("monitor ok run failed: %d %s", okRun.Code, okRun.Body)
	}
	updated, err := store.UpdateProviderResource("rsrc_mock_primary", ProviderResource{Status: StatusDisabled, Healthy: false})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusDisabled {
		t.Fatalf("expected disabled resource before failed monitor, got %+v", updated)
	}
	failedRun := doJSON(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, "")
	if failedRun.Code != http.StatusOK || !strings.Contains(failedRun.Body, `"status":"failed"`) || !strings.Contains(failedRun.Body, `"alert_id"`) {
		t.Fatalf("monitor failed run did not create alert: %d %s", failedRun.Code, failedRun.Body)
	}
	alerts := store.ListAlerts()
	if len(alerts) == 0 || alerts[0].Code != "monitor_check_failed" || alerts[0].ScopeID != monitor.ID {
		t.Fatalf("expected monitor alert, got %+v", alerts)
	}
	monitors := store.ListResources("monitors")
	var found AdminResource
	for _, item := range monitors {
		if item.ID == monitor.ID {
			found = item
			break
		}
	}
	if stringifyValueForTest(found.Fields["last_status"]) != "failed" || stringifyValueForTest(found.Fields["last_checked_at"]) == "" {
		t.Fatalf("monitor fields not updated: %+v", found.Fields)
	}
}

func TestMonitorRunInfersLegacyModelMonitor(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	monitor := store.CreateResource("monitors", AdminResource{
		Name:   "Legacy model monitor",
		Status: StatusActive,
		Fields: map[string]any{
			"provider": "mock",
			"model":    "gpt-4.1-mini",
		},
	})
	app := New(store).Handler()

	run := doJSON(t, app, http.MethodPost, "/api/admin/resources/monitors/"+monitor.ID+"/run", map[string]any{}, "")
	if run.Code != http.StatusOK || !strings.Contains(run.Body, `"target_type":"model"`) || !strings.Contains(run.Body, `"status":"ok"`) {
		t.Fatalf("expected legacy monitor to run as model monitor, got %d %s", run.Code, run.Body)
	}
}

func TestDefaultMonitorsAreAutoDiscovered(t *testing.T) {
	store := NewMemoryStore()
	if err := BootstrapBaseData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:       "prv_health_default",
		Name:     "Health Default Provider",
		Type:     ProviderMock,
		Status:   StatusActive,
		Healthy:  true,
		Priority: 1,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_health_default",
		ProviderID:   provider.ID,
		Name:         "Health Default Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		ID:       "health-default-model",
		Name:     "health-default-model",
		Family:   "test",
		Modality: "chat",
		Status:   StatusActive,
	})
	store.AddRoute(ModelRoute{
		ID:                 "route_health_default",
		ModelName:          "health-default-model",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "health-default-upstream",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
	})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodGet, "/api/admin/resources/monitors", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("default monitor list failed: %d %s", resp.Code, resp.Body)
	}
	monitors := store.ListResources("monitors")
	if len(monitors) != 3 {
		t.Fatalf("expected provider/resource/model monitors, got %d: %+v", len(monitors), monitors)
	}
	found := map[string]bool{}
	for _, monitor := range monitors {
		key := monitorTargetKey(monitor.Fields)
		found[key] = true
		if stringifyValueForTest(monitor.Fields["managed_by"]) != "tokenhub_auto" {
			t.Fatalf("expected auto-managed monitor, got %+v", monitor.Fields)
		}
		if stringifyValueForTest(monitor.Fields["last_status"]) != "ok" || stringifyValueForTest(monitor.Fields["last_checked_at"]) == "" {
			t.Fatalf("expected monitor to run immediately, got %+v", monitor.Fields)
		}
	}
	for _, key := range []string{"provider:" + provider.ID, "resource:" + resource.ID, "model:health-default-model"} {
		if !found[key] {
			t.Fatalf("missing default monitor target %s in %+v", key, found)
		}
	}

	resp = doJSON(t, app, http.MethodGet, "/api/admin/resources/monitors", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("second default monitor list failed: %d %s", resp.Code, resp.Body)
	}
	if got := len(store.ListResources("monitors")); got != len(monitors) {
		t.Fatalf("default monitor discovery should be idempotent, before=%d after=%d", len(monitors), got)
	}
}

func TestDefaultAlertRulesAreAutoDiscovered(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodGet, "/api/admin/resources/alert-rules", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("default alert rule list failed: %d %s", resp.Code, resp.Body)
	}
	rules := store.ListResources("alert-rules")
	if len(rules) != 5 {
		t.Fatalf("expected default provider and quota alert rules, got %d: %+v", len(rules), rules)
	}
	found := map[string]bool{}
	for _, rule := range rules {
		key := alertRuleKey(rule.Fields)
		found[key] = true
		if stringifyValueForTest(rule.Fields["managed_by"]) != "tokenhub_auto" {
			t.Fatalf("expected auto-managed alert rule, got %+v", rule.Fields)
		}
		if stringifyValueForTest(rule.Fields["metric"]) == "" || stringifyValueForTest(rule.Fields["threshold"]) == "" {
			t.Fatalf("expected metric and threshold, got %+v", rule.Fields)
		}
	}
	for _, key := range []string{
		"provider_health_failed",
		"provider_resource_health_failed",
		"request_quota_near_limit",
		"token_quota_near_limit",
		"cost_quota_near_limit",
	} {
		if !found[key] {
			t.Fatalf("missing default alert rule %s in %+v", key, found)
		}
	}

	resp = doJSON(t, app, http.MethodGet, "/api/admin/resources/alert-rules", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("second default alert rule list failed: %d %s", resp.Code, resp.Body)
	}
	if got := len(store.ListResources("alert-rules")); got != len(rules) {
		t.Fatalf("default alert rule discovery should be idempotent, before=%d after=%d", len(rules), got)
	}
}

func TestProviderResourceCooldownAfterFailures(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, "failing_resource")
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"resource-ops-chat"}]}`))
	}))
	defer upstream.Close()
	if _, err := store.UpdateProviderResource(resourceID, ProviderResource{BaseURL: upstream.URL, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	store.failureThreshold = 2
	server := New(store)
	registerTestAdapter(server, "failing_resource", failingAdapter{})
	app := server.Handler()

	for i := 0; i < 2; i++ {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "gpt-4.1-mini",
			"messages": []map[string]any{
				{"role": "user", "content": "cooldown"},
			},
		}, secret)
		if resp.Code != http.StatusBadGateway {
			t.Fatalf("request %d expected 502 provider error, got %d: %s", i+1, resp.Code, resp.Body)
		}
	}

	resource := findResource(t, store, resourceID)
	if resource.FailureCount < 2 || resource.CooldownUntil == nil || resource.Healthy {
		t.Fatalf("expected resource in cooldown, got failures=%d healthy=%v cooldown=%v", resource.FailureCount, resource.Healthy, resource.CooldownUntil)
	}
	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/"+resourceID+"/test", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("resource test should clear cooldown, got %d: %s", resp.Code, resp.Body)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("expected one upstream probe, got %d", upstreamCalls.Load())
	}
	resource = findResource(t, store, resourceID)
	if resource.FailureCount != 0 || resource.CooldownUntil != nil || !resource.Healthy {
		t.Fatalf("expected test to restore resource, got failures=%d healthy=%v cooldown=%v", resource.FailureCount, resource.Healthy, resource.CooldownUntil)
	}
}

func TestProviderResourceRPMLimit(t *testing.T) {
	store, secret, resourceID := newResourceRoutedStore(t, ProviderMock)
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resourceID).Update("rate_limit_rpm", 1).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	first := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "first"},
		},
	}, secret)
	if first.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d: %s", first.Code, first.Body)
	}
	second := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "second"},
		},
	}, secret)
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body, "provider_resource_rpm_exceeded") {
		t.Fatalf("second request expected RPM limit, got %d: %s", second.Code, second.Body)
	}
}

func TestGatewayRoutesThroughProviderResource(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Resource Routed App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "resource-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_resource_route")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_resource", Name: "Resource Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_primary",
		ProviderID:   provider.ID,
		Name:         "Primary Resource",
		ResourceType: "mock",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_resource",
		ModelName:          "gpt-4.1-mini",
		ProviderID:         provider.ID,
		ProviderResourceID: resource.ID,
		ProviderModel:      "resource-chat",
		Priority:           1,
		Weight:             100,
		Status:             StatusActive,
	})
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "resource hit"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %d", len(logs))
	}
	if logs[0].ProviderID != provider.ID || logs[0].ProviderResourceID != resource.ID {
		t.Fatalf("expected provider resource audit log, got provider=%s resource=%s", logs[0].ProviderID, logs[0].ProviderResourceID)
	}
	resources := store.ListProviderResources()
	var touched bool
	for _, item := range resources {
		if item.ID == resource.ID && item.LastUsedAt != nil {
			touched = true
		}
	}
	if !touched {
		t.Fatalf("provider resource should be marked last used")
	}
}

func TestProviderHealthAffectsRouting(t *testing.T) {
	app := newTestServer()
	health := doJSON(t, app, http.MethodPost, "/api/admin/providers/prv_mock/health", map[string]any{
		"healthy": false,
	}, "")
	if health.Code != http.StatusOK {
		t.Fatalf("expected health update, got %d: %s", health.Code, health.Body)
	}

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, "thk_demo_local")
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "provider_unavailable") {
		t.Fatalf("expected provider_unavailable: %s", resp.Body)
	}
}

func TestGatewayFailoverUsesBackupRoute(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Failover App"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "failover-key",
		Allowed: []string{"gpt-4.1-mini"},
		Status:  StatusActive,
	}, "thk_failover")
	if err != nil {
		t.Fatal(err)
	}
	failing := store.AddProvider(Provider{ID: "prv_failing", Name: "Failing", Type: "failing_mock", Status: StatusActive, Healthy: true})
	backup := store.AddProvider(Provider{ID: "prv_backup", Name: "Backup", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_failing", ModelName: "gpt-4.1-mini", ProviderID: failing.ID, ProviderModel: "failing-chat", Priority: 1, Weight: 100, Status: StatusActive, Strategy: "priority_only"})
	store.AddRoute(ModelRoute{ID: "route_backup", ModelName: "gpt-4.1-mini", ProviderID: backup.ID, ProviderModel: "backup-chat", Priority: 2, Weight: 100, Status: StatusActive, Strategy: "priority_only"})

	server := New(store)
	registerTestAdapter(server, "failing_mock", failingAdapter{})
	app := server.Handler()

	resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-4.1-mini",
		"messages": []map[string]any{
			{"role": "user", "content": "fail over please"},
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, "Echo: fail over please") {
		t.Fatalf("expected backup mock response: %s", resp.Body)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %d", len(logs))
	}
	if logs[0].ProviderID != backup.ID || logs[0].ProviderModel != "backup-chat" {
		t.Fatalf("expected backup route audit log, got provider=%s model=%s", logs[0].ProviderID, logs[0].ProviderModel)
	}
	routes := store.ListRoutes()
	var backupTouched bool
	for _, route := range routes {
		if route.ID == "route_backup" && route.LastUsedAt != nil {
			backupTouched = true
		}
		if route.ID == "route_failing" && route.LastUsedAt != nil {
			t.Fatalf("failing route should not be marked last used")
		}
	}
	if !backupTouched {
		t.Fatalf("backup route should be marked last used")
	}

	detail := doJSON(t, app, http.MethodGet, "/api/admin/audit/requests/"+logs[0].RequestID, nil, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("request detail failed: %d %s", detail.Code, detail.Body)
	}
	if !strings.Contains(detail.Body, `"attempts"`) || !strings.Contains(detail.Body, `"route_failing"`) || !strings.Contains(detail.Body, `"route_backup"`) {
		t.Fatalf("expected route attempts in detail: %s", detail.Body)
	}
}

func TestModelRouterStrategiesRankCandidates(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Router Strategy App"})
	key := APIKey{ID: "key_router_strategy", ProjectID: project.ID, Name: "router-key", Status: StatusActive}
	model := Model{Name: "gpt-4.1-mini", Modality: "chat", Status: StatusActive}
	call := CallContext{RequestID: "req_router_strategy", Project: project, Key: key, Model: model}
	fast := store.AddProvider(Provider{ID: "prv_fast", Name: "Fast", Type: ProviderMock, Status: StatusActive, Healthy: true})
	cheap := store.AddProvider(Provider{ID: "prv_cheap", Name: "Cheap", Type: ProviderMock, Status: StatusActive, Healthy: true})
	quality := store.AddProvider(Provider{ID: "prv_quality", Name: "Quality", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(model)
	store.AddRoute(ModelRoute{ID: "route_fast", ModelName: model.Name, ProviderID: fast.ID, ProviderModel: "fast-chat", Priority: 1, Weight: 100, QualityScore: 50, CostScore: 50, Status: StatusActive, Strategy: RouteStrategyQuality})
	store.AddRoute(ModelRoute{ID: "route_cheap", ModelName: model.Name, ProviderID: cheap.ID, ProviderModel: "cheap-chat", Priority: 1, Weight: 80, QualityScore: 40, CostScore: 95, Status: StatusActive, Strategy: RouteStrategyQuality})
	store.AddRoute(ModelRoute{ID: "route_quality", ModelName: model.Name, ProviderID: quality.ID, ProviderModel: "quality-chat", Priority: 1, Weight: 60, QualityScore: 95, CostScore: 35, Status: StatusActive, Strategy: RouteStrategyQuality})

	server := New(store)
	candidates, err := store.SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	planned := server.planRouteOrder(call, candidates)
	if planned[0].Route.ID != "route_quality" {
		t.Fatalf("quality strategy should pick highest quality first, got %s", planned[0].Route.ID)
	}

	for _, route := range store.ListRoutes() {
		route.Strategy = RouteStrategyCost
		if _, err := store.UpdateRoute(route.ID, route); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err = store.SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	planned = server.planRouteOrder(call, candidates)
	if planned[0].Route.ID != "route_cheap" {
		t.Fatalf("cost strategy should pick highest cost score first, got %s", planned[0].Route.ID)
	}
}

func TestHealth(t *testing.T) {
	app := newTestServer()
	resp := doJSON(t, app, http.MethodGet, "/healthz", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
}

func TestReadinessFailsWhenDatabaseIsUnavailableButLivenessRemainsHealthy(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	ready := doJSON(t, app, http.MethodGet, "/readyz", nil, "")
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness 503 after database close, got %d: %s", ready.Code, ready.Body)
	}
	live := doJSON(t, app, http.MethodGet, "/livez", nil, "")
	if live.Code != http.StatusOK {
		t.Fatalf("expected liveness 200 after database close, got %d: %s", live.Code, live.Body)
	}
}

func TestClientIPIgnoresForwardedHeaderFromUntrustedPeer(t *testing.T) {
	server := &Server{config: Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "198.51.100.7:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.25")

	if got := server.clientIP(request); got != "198.51.100.7" {
		t.Fatalf("expected direct peer IP, got %q", got)
	}
}

func TestClientIPUsesForwardedChainFromTrustedProxy(t *testing.T) {
	server := &Server{config: Config{TrustedProxyCIDRs: []string{"10.0.0.0/8", "192.0.2.10"}}}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "10.0.0.8:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.25, 192.0.2.10")

	if got := server.clientIP(request); got != "203.0.113.25" {
		t.Fatalf("expected first untrusted address from the right, got %q", got)
	}
}

func TestClientIPRejectsMalformedForwardedChain(t *testing.T) {
	server := &Server{config: Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "10.0.0.8:4321"
	request.Header.Set("X-Forwarded-For", "203.0.113.25, not-an-ip")

	if got := server.clientIP(request); got != "10.0.0.8" {
		t.Fatalf("expected malformed chain to fall back to direct peer, got %q", got)
	}
}
