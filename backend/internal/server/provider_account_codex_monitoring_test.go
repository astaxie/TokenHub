package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderMonitoringUsesBackendProbeAndCachedQuota(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_monitoring",
		Name:    "Codex Monitoring",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_monitoring",
		ProviderID:   provider.ID,
		Name:         "Monitoring Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "access_monitoring", AccountID: "account_monitoring"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.RecordProviderObservation(ProviderObservation{
		ProviderID:  provider.ID,
		ResourceID:  resource.ID,
		AdapterType: provider.Type,
		Source:      "active_probe",
		Operation:   "responses",
		Success:     true,
		LatencyMS:   321,
	})
	quotaCalls := 0
	server := New(store)
	server.codexSubscription.QuotaURL = "https://chatgpt.example/backend-api/wham/usage"
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		quotaCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"plan_type":"pro","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":25,"reset_at":1999999999}}}`,
			)),
			Request: req,
		}, nil
	})}
	invoke := func() []ProviderMonitoringSnapshot {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/providers/monitoring", nil)
		request.Header.Set("Authorization", "Bearer dev_admin_token")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("monitoring request failed: %d %s", response.Code, response.Body.String())
		}
		var payload struct {
			Data []ProviderMonitoringSnapshot `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Data
	}
	first := invoke()
	second := invoke()
	if quotaCalls != 1 {
		t.Fatalf("quota cache did not prevent duplicate upstream requests: %d", quotaCalls)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("unexpected monitoring snapshots: first=%+v second=%+v", first, second)
	}
	snapshot := first[0]
	if snapshot.State != "healthy" || snapshot.ActiveProbe.Source != "active_probe" ||
		snapshot.ActiveProbe.LatencyMS != 321 || snapshot.Quota.RemainingPercent != 75 ||
		snapshot.Quota.SuccessfulAccounts != 1 {
		t.Fatalf("monitoring did not preserve source semantics or quota: %+v", snapshot)
	}
}

func TestProviderMonitoringRecoversFromHistoricalFailure(t *testing.T) {
	now := time.Now().UTC()
	signal := observationMonitoringSignal(now, "gateway_request", []ProviderObservation{
		{Success: false, ErrorCode: "internal_error", ObservedAt: now.Add(-time.Minute)},
		{Success: true, ObservedAt: now},
	})
	if signal.State != "degraded" || signal.SuccessRate != 50 {
		t.Fatalf("a successful latest request must recover Functional Down: %+v", signal)
	}
}

func TestCodexSubscriptionProbeAllowsFastModeForAnyModel(t *testing.T) {
	adapter := CodexSubscriptionAdapter{
		Client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			reasoning, _ := payload["reasoning"].(map[string]any)
			if payload["model"] != "gpt-5.4" || payload["service_tier"] != "priority" || reasoning["effort"] != "high" {
				t.Fatalf("unexpected fast probe payload: %#v", payload)
			}
			stream := strings.Join([]string{
				"event: response.output_text.delta",
				`data: {"type":"response.output_text.delta","delta":"Fast probe works."}`,
				"",
				"event: response.completed",
				`data: {"type":"response.completed","response":{"id":"resp_fast_probe","status":"completed","service_tier":"priority","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"",
			}, "\n")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(stream)),
				Request:    req,
			}, nil
		})},
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{AccessToken: "access_fast_probe", AccountID: "account_fast_probe"}, nil
		},
	}
	provider := Provider{
		ID:      "prv_fast_probe",
		Name:    "Fast Probe",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{"resource_id": "rsrc_fast_probe"},
	}
	resource := ProviderResource{
		ID:           "rsrc_fast_probe",
		ProviderID:   provider.ID,
		Name:         "Fast Probe Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}

	result, err := adapter.Probe(context.Background(), provider, resource, ProviderProbeRequest{
		Model:           "gpt-5.4",
		ReasoningEffort: "high",
		Speed:           "fast",
		Prompt:          "Confirm fast mode.",
	})
	if err != nil {
		t.Fatalf("fast probe with non-Luna model failed: %v", err)
	}
	if result.Model != "gpt-5.4" || result.Speed != "fast" || result.UpstreamServiceTier != "priority" || result.OutputText != "Fast probe works." {
		t.Fatalf("unexpected fast probe result: %+v", result)
	}
}

func TestProviderTestUsesCodexDefaultProbeProfile(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_default_probe",
		Name:    "Codex Default Probe",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_default_probe",
		ProviderID:   provider.ID,
		Name:         "Default Probe Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		Credentials:  &ProviderResourceCredentials{AccessToken: "access_probe", AccountID: "account_probe"},
	}); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	server.codexSubscription.Client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		reasoning, _ := payload["reasoning"].(map[string]any)
		if payload["model"] != openAICodexDefaultProbeModel || payload["service_tier"] != nil || reasoning["effort"] != "medium" {
			t.Fatalf("unexpected default probe profile: %#v", payload)
		}
		if _, ok := payload["background"]; ok {
			t.Fatalf("default Codex probe must omit unsupported background parameter: %#v", payload)
		}
		stream := strings.Join([]string{
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"Codex connection works."}`,
			"",
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_probe","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(stream)),
			Request:    req,
		}, nil
	})}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/providers/"+provider.ID+"/test", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer dev_admin_token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("provider probe failed: %d %s", response.Code, response.Body.String())
	}
	var result ProviderProbeBatchResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Succeeded != 1 || result.Failed != 0 || !result.Healthy ||
		len(result.Results) != 1 || result.Results[0].Speed != "standard" {
		t.Fatalf("unexpected provider probe result: %+v", result)
	}
}

func TestProviderAdapterCompatibilityAndLegacyMigration(t *testing.T) {
	store := NewMemoryStore()
	codex := store.AddProvider(Provider{
		ID:      "prv_strict_codex",
		Name:    "Strict Codex",
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID:   codex.ID,
		Name:         "Invalid API Key",
		ResourceType: ProviderResourceAPIKey,
		Status:       StatusActive,
		Healthy:      true,
	}); AsHTTPError(err).Code != "provider_adapter_resource_conflict" {
		t.Fatalf("Codex Provider accepted API-key resource: %v", err)
	}
	openAIWithKey := store.AddProvider(Provider{
		ID:      "prv_strict_openai",
		Name:    "Strict OpenAI",
		Type:    ProviderOpenAI,
		APIKey:  "upstream-real-key",
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID:   openAIWithKey.ID,
		Name:         "Invalid Subscription",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}); AsHTTPError(err).Code != "provider_adapter_resource_conflict" {
		t.Fatalf("OpenAI API Provider accepted subscription resource: %v", err)
	}
	emptyOpenAI := store.AddProvider(Provider{
		ID:      "prv_auto_codex",
		Name:    "Auto Codex",
		Type:    ProviderOpenAI,
		Status:  StatusActive,
		Healthy: true,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID:   emptyOpenAI.ID,
		Name:         "First Subscription",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
	}); err != nil {
		t.Fatal(err)
	}
	normalized, ok := integrationProvider(store, emptyOpenAI.ID)
	if !ok || normalized.Type != ProviderOpenAICodex || normalized.BaseURL != openAICodexBaseURL {
		t.Fatalf("empty OpenAI Provider was not normalized to Codex: %+v", normalized)
	}
	server := New(store)
	codexAdapter, ok := server.adapterRegistry.Describe(ProviderOpenAICodex)
	if !ok || codexAdapter.PluginID != "tokenhub.provider.openai-codex" {
		t.Fatalf("Codex adapter plugin mapping = %+v, ok=%v", codexAdapter, ok)
	}
	codexCatalog, ok := server.pluginProviderCatalogCapabilityEntryForType(ProviderOpenAICodex)
	if !ok || codexCatalog.ID != codexProviderCatalogID || codexCatalog.Type != ProviderOpenAICodex {
		t.Fatalf("Codex catalog mapping = %+v, ok=%v", codexCatalog, ok)
	}

	legacy := store.AddProvider(Provider{
		ID:               "prv_legacy_mixed",
		Name:             "Legacy Mixed",
		Type:             ProviderOpenAI,
		APIKey:           "legacy-upstream-key",
		Status:           StatusActive,
		Healthy:          true,
		Priority:         3,
		Headers:          map[string]string{"X-Tenant": "legacy-tenant-secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	})
	direct := ProviderResource{
		ID:           "rsrc_legacy_direct",
		ProviderID:   legacy.ID,
		Name:         "Legacy Direct",
		ResourceType: ProviderResourceAPIKey,
		Status:       StatusActive,
		Healthy:      true,
	}
	subscription := ProviderResource{
		ID:           "rsrc_legacy_subscription",
		ProviderID:   legacy.ID,
		Name:         "Legacy Subscription",
		ResourceType: ProviderResourceOpenAISubscription,
		Status:       StatusActive,
		Healthy:      true,
		APIKey:       store.encryptSecret("legacy-subscription-access"),
		CredentialBlob: store.encryptProviderResourceCredentialBlob(ProviderResourceCredentials{
			AuthType:     "personal_access_token",
			RefreshToken: "legacy-subscription-refresh",
			AccountID:    "acct_legacy_subscription",
			Email:        "legacy.subscription@example.com",
		}),
		FailureCount: 2,
		LastCheckedAt: func() *time.Time {
			v := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
			return &v
		}(),
	}
	if err := store.db.Create(&direct).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	quotaFetchedAt := time.Date(2026, time.August, 30, 9, 30, 0, 0, time.UTC)
	quotaSnapshot := OpenAIAccountQuota{
		UserID:    "usr_legacy_subscription",
		AccountID: "acct_legacy_subscription",
		Email:     "legacy.subscription@example.com",
		PlanType:  "plus",
		FetchedAt: quotaFetchedAt.Unix(),
	}
	encodedQuotaSnapshot, err := json.Marshal(quotaSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProviderResourceQuota(subscription.ID, legacy.Type, string(encodedQuotaSnapshot), quotaFetchedAt); err != nil {
		t.Fatal(err)
	}
	usageRecord := UsageRecord{
		ID:                 "usage_legacy_subscription",
		RequestID:          "req_legacy_subscription",
		ProjectID:          "prj_legacy_subscription",
		APIKeyID:           "thk_legacy_subscription",
		ModelName:          "gpt-legacy-codex",
		ProviderID:         legacy.ID,
		ProviderResourceID: subscription.ID,
		InputTokens:        11,
		OutputTokens:       7,
		TotalTokens:        18,
		CostUSD:            0.42,
		CreatedAt:          quotaFetchedAt,
	}
	if err := store.db.Create(&usageRecord).Error; err != nil {
		t.Fatal(err)
	}
	auditEvent := AuditEvent{
		ID:            "audit_legacy_subscription",
		ActorUserID:   "usr_legacy_admin",
		CorrelationID: "corr_legacy_subscription",
		Action:        "provider_resource_quota_refresh",
		ResourceType:  "provider_resource",
		ResourceID:    subscription.ID,
		Status:        "succeeded",
		Message:       "legacy quota snapshot recorded",
		CreatedAt:     quotaFetchedAt,
	}
	if err := store.db.Create(&auditEvent).Error; err != nil {
		t.Fatal(err)
	}
	requestLog := RequestLog{
		ID:                 "log_legacy_subscription",
		RequestID:          "req_legacy_subscription",
		ProjectID:          "prj_legacy_subscription",
		APIKeyID:           "thk_legacy_subscription",
		ModelName:          "gpt-legacy-codex",
		ProviderID:         legacy.ID,
		ProviderResourceID: subscription.ID,
		ProviderModel:      "gpt-legacy-codex",
		StatusCode:         http.StatusOK,
		LatencyMS:          12,
		CreatedAt:          quotaFetchedAt,
	}
	if err := store.db.Create(&requestLog).Error; err != nil {
		t.Fatal(err)
	}
	routeAttempt := RouteAttemptLog{
		ID:                 "attempt_legacy_subscription",
		RequestID:          requestLog.RequestID,
		AttemptIndex:       1,
		RouteID:            "route_legacy_subscription",
		ProviderID:         legacy.ID,
		ProviderResourceID: subscription.ID,
		ProviderModel:      "gpt-legacy-codex",
		StatusCode:         http.StatusOK,
		Invoked:            true,
		LatencyMS:          12,
		TotalTokens:        18,
		StartedAt:          quotaFetchedAt,
		EndedAt:            quotaFetchedAt,
		CreatedAt:          quotaFetchedAt,
	}
	if err := store.db.Create(&routeAttempt).Error; err != nil {
		t.Fatal(err)
	}
	providerObservation := ProviderObservation{
		ID:          "pob_legacy_subscription",
		ProviderID:  legacy.ID,
		ResourceID:  subscription.ID,
		AdapterType: legacy.Type,
		Source:      "gateway_request",
		Operation:   "inference",
		Success:     true,
		LatencyMS:   12,
		ObservedAt:  quotaFetchedAt,
	}
	if err := store.db.Create(&providerObservation).Error; err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "gpt-legacy-codex", Category: "codex", Family: "codex", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID:                 "route_legacy_subscription",
		ModelName:          "gpt-legacy-codex",
		ProviderID:         legacy.ID,
		ProviderResourceID: subscription.ID,
		ProviderModel:      "gpt-legacy-codex",
		Status:             StatusActive,
	})
	store.AddRoute(ModelRoute{
		ID:            "route_legacy_generic",
		ModelName:     "gpt-legacy-codex",
		ProviderID:    legacy.ID,
		ProviderModel: "gpt-legacy-codex",
		Status:        StatusActive,
	})
	if err := store.NormalizeProviderAdapterTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	providersAfterFirst := store.ListProviders()
	routesAfterFirst := store.ListRoutes()
	if err := store.NormalizeProviderAdapterTypes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.ListProviders()) != len(providersAfterFirst) || len(store.ListRoutes()) != len(routesAfterFirst) {
		t.Fatalf("legacy migration is not idempotent: providers %d/%d routes %d/%d",
			len(providersAfterFirst), len(store.ListProviders()), len(routesAfterFirst), len(store.ListRoutes()))
	}
	var splitProvider Provider
	for _, provider := range store.ListProviders() {
		if provider.Type == ProviderOpenAICodex && provider.ID != emptyOpenAI.ID && provider.ID != codex.ID {
			splitProvider = provider
		}
	}
	if splitProvider.ID == "" {
		t.Fatal("mixed legacy Provider was not split")
	}
	if len(splitProvider.Headers) != 0 || len(splitProvider.SensitiveHeaders) != 0 {
		t.Fatalf("Codex split inherited unsupported custom headers: %+v", splitProvider)
	}
	directProvider, ok := integrationProvider(store, legacy.ID)
	if !ok || directProvider.Headers["X-Tenant"] != "legacy-tenant-secret" {
		t.Fatalf("direct Provider lost its sensitive custom header: %+v", directProvider)
	}
	migratedSubscription, ok := integrationProviderResource(store, subscription.ID)
	if !ok || migratedSubscription.ProviderID != splitProvider.ID {
		t.Fatalf("subscription resource was not moved to Codex Provider: %+v", migratedSubscription)
	}
	if migratedSubscription.ID != subscription.ID || migratedSubscription.Healthy != subscription.Healthy || migratedSubscription.FailureCount != subscription.FailureCount {
		t.Fatalf("subscription resource state changed during migration: before=%+v after=%+v", subscription, migratedSubscription)
	}
	if migratedSubscription.LastCheckedAt == nil || !migratedSubscription.LastCheckedAt.Equal(*subscription.LastCheckedAt) {
		t.Fatalf("subscription last_checked_at changed during migration: before=%v after=%v", subscription.LastCheckedAt, migratedSubscription.LastCheckedAt)
	}
	creds := store.providerResourceCredentialsForRuntime(migratedSubscription)
	if creds.AccessToken != "legacy-subscription-access" || creds.RefreshToken != "legacy-subscription-refresh" || creds.AccountID != "acct_legacy_subscription" || creds.Email != "legacy.subscription@example.com" {
		t.Fatalf("subscription credentials were not preserved: %+v", creds)
	}
	quotaObservation, ok := store.GetProviderResourceObservation(subscription.ID)
	if !ok || quotaObservation.QuotaFetchedAt == nil || quotaObservation.QuotaFetchedAt.Unix() != quotaFetchedAt.Unix() {
		t.Fatalf("subscription quota observation was not preserved: %+v", quotaObservation)
	}
	if !strings.Contains(quotaObservation.QuotaSnapshot, `"plan_type":"plus"`) {
		t.Fatalf("subscription quota snapshot was not preserved: %+v", quotaObservation)
	}
	if cached, ok := New(store).cachedOpenAIAccountQuota(subscription.ID, 0); !ok || cached.PlanType != "plus" || cached.AccountID != "acct_legacy_subscription" {
		t.Fatalf("subscription cached quota was not readable after migration: ok=%v quota=%+v", ok, cached)
	}
	foundUsage := false
	for _, record := range store.ListUsageRecords() {
		if record.ID == usageRecord.ID {
			foundUsage = true
			if record.ProviderID != legacy.ID || record.ProviderResourceID != subscription.ID || record.TotalTokens != usageRecord.TotalTokens {
				t.Fatalf("usage record changed during migration: before=%+v after=%+v", usageRecord, record)
			}
		}
	}
	if !foundUsage {
		t.Fatalf("usage record was not preserved: %+v", usageRecord)
	}
	foundAudit := false
	for _, event := range store.ListAuditEvents() {
		if event.ID == auditEvent.ID {
			foundAudit = true
			if event.ResourceID != auditEvent.ResourceID || event.CorrelationID != auditEvent.CorrelationID || event.Action != auditEvent.Action {
				t.Fatalf("audit event changed during migration: before=%+v after=%+v", auditEvent, event)
			}
		}
	}
	if !foundAudit {
		t.Fatalf("audit event was not preserved: %+v", auditEvent)
	}
	requestDetail, err := store.GetRequestDetail(requestLog.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	detailLog, ok := requestDetail["log"].(RequestLog)
	if !ok || detailLog.ProviderID != legacy.ID || detailLog.ProviderResourceID != subscription.ID {
		t.Fatalf("request detail log was not preserved: %+v", requestDetail["log"])
	}
	attempts, ok := requestDetail["attempts"].([]RouteAttemptLog)
	if !ok || len(attempts) != 1 || attempts[0].ProviderID != legacy.ID || attempts[0].ProviderResourceID != subscription.ID {
		t.Fatalf("request detail attempts were not preserved: %+v", requestDetail["attempts"])
	}
	observations := store.ListProviderObservations(time.Time{})
	foundObservation := false
	for _, observation := range observations {
		if observation.ID == providerObservation.ID {
			foundObservation = true
			if observation.ProviderID != legacy.ID || observation.ResourceID != subscription.ID || observation.Operation != providerObservation.Operation {
				t.Fatalf("provider observation was not preserved: before=%+v after=%+v", providerObservation, observation)
			}
		}
	}
	if !foundObservation {
		t.Fatalf("provider observation was not preserved: %+v", providerObservation)
	}
	migratedRoutes := 0
	for _, route := range store.ListRoutes() {
		if route.ProviderID == splitProvider.ID {
			migratedRoutes++
		}
	}
	if migratedRoutes != 2 {
		t.Fatalf("expected resource route and cloned generic route on split Provider, got %d", migratedRoutes)
	}
	candidates, err := store.SelectRouteCandidates("gpt-legacy-codex")
	if err != nil {
		t.Fatal(err)
	}
	foundSubscription := false
	foundLegacyGeneric := false
	for _, candidate := range candidates {
		switch candidate.Route.ID {
		case "route_legacy_subscription":
			foundSubscription = true
			if candidate.Provider.ID != splitProvider.ID {
				t.Fatalf("migrated subscription route provider = %q, want %q", candidate.Provider.ID, splitProvider.ID)
			}
		case "route_legacy_generic":
			foundLegacyGeneric = true
			if candidate.Provider.ID != legacy.ID {
				t.Fatalf("legacy generic route provider = %q, want %q", candidate.Provider.ID, legacy.ID)
			}
		}
	}
	if !foundSubscription || !foundLegacyGeneric {
		t.Fatalf("legacy route candidates missing preserved routes: %+v", candidates)
	}
}
