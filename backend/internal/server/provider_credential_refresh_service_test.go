package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func addOpenAIAccountRefreshProvider(store *GormStore, name string) Provider {
	store.ConfigureProviderResourceCredentialIdentityProfiles(map[string]string{
		ProviderResourceOpenAISubscription: providerResourceIdentityProfileOpenAIIDToken,
	})
	return store.AddProvider(Provider{
		Name:    name,
		Type:    ProviderOpenAICodex,
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{
			providerCredentialRefreshProfileOption: providerCredentialRefreshProfileOpenAIAccountOAuth,
		},
	})
}

func TestProviderCredentialRefreshServiceRenewsExpiringOpenAIAccounts(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Codex OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Codex OAuth Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "access-before-renewal", RefreshToken: "refresh-before-renewal", ClientID: openAIAccountOAuthClientID,
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-before-renewal" {
			t.Fatalf("unexpected refresh request: %v", r.Form)
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-after-renewal", "refresh_token": "refresh-after-renewal", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	service := newProviderCredentialRefreshService(store)
	service.RunDue(context.Background())
	if requests.Load() != 1 {
		t.Fatalf("expected one renewal request, got %d", requests.Load())
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "access-after-renewal" || credentials.RefreshToken != "refresh-after-renewal" {
		t.Fatalf("expected rotated credentials to be stored, got %+v", credentials)
	}
}

func TestServerCredentialRefreshSchedulerSkipsProviderBackgroundRefreshJobs(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Codex Background OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Codex Background OAuth Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "access-before-background", RefreshToken: "refresh-before-background", ClientID: openAIAccountOAuthClientID,
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-before-background" {
			t.Fatalf("unexpected refresh request: %v", r.Form)
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-after-background", "refresh_token": "refresh-after-background", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	server.credentialRefresh.RunDue(context.Background())
	if requests.Load() != 0 {
		t.Fatalf("core credential scheduler sent %d token requests, want background job ownership", requests.Load())
	}

	record, err := server.pluginBackgroundRunner.Run(context.Background(), pluginmeta.BackgroundJobInvocation{
		PluginID: "tokenhub.provider.openai-codex",
		JobID:    "openai_codex.credentials.refresh_due",
		Trigger:  "manual",
	})
	if err != nil {
		t.Fatalf("run credential refresh background job: %v", err)
	}
	data := record.Result.Data.(map[string]int)
	if data["refreshed"] != 1 || data["failed"] != 0 {
		t.Fatalf("credential refresh job result = %+v", data)
	}
	if requests.Load() != 1 {
		t.Fatalf("background credential job sent %d token requests, want 1", requests.Load())
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "access-after-background" || credentials.RefreshToken != "refresh-after-background" {
		t.Fatalf("expected background-refreshed credentials to be stored, got %+v", credentials)
	}
}

func TestProviderQuotaRefreshBackgroundJobPersistsQuotaSnapshots(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Codex Background Quota", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Codex Background Quota Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "quota-access", AccountID: "acc_quota_background", ClientID: openAIAccountOAuthClientID,
			ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	quotaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("authorization") != "Bearer quota-access" || r.Header.Get("chatgpt-account-id") != "acc_quota_background" {
			t.Fatalf("unexpected quota headers: authorization=%q account=%q", r.Header.Get("authorization"), r.Header.Get("chatgpt-account-id"))
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(OpenAIAccountQuota{
			UserID:    "usr_quota_background",
			AccountID: "acc_quota_background",
			Email:     "quota.owner@example.com",
			PlanType:  "plus",
		})
	}))
	defer quotaServer.Close()
	server.codexSubscription.QuotaURL = quotaServer.URL

	record, err := server.pluginBackgroundRunner.Run(context.Background(), pluginmeta.BackgroundJobInvocation{
		PluginID: "tokenhub.provider.openai-codex",
		JobID:    "openai_codex.quota.refresh_due",
		Trigger:  "manual",
	})
	if err != nil {
		t.Fatalf("run quota refresh background job: %v", err)
	}
	data := record.Result.Data.(map[string]int)
	if data["refreshed"] != 1 || data["failed"] != 0 {
		t.Fatalf("quota refresh job result = %+v", data)
	}
	if requests.Load() != 1 {
		t.Fatalf("quota job sent %d upstream requests, want 1", requests.Load())
	}
	observation, ok := store.GetProviderResourceObservation(resource.ID)
	if !ok || observation.QuotaFetchedAt == nil || !strings.Contains(observation.QuotaSnapshot, "quota.owner@example.com") {
		t.Fatalf("quota observation = %+v, %v", observation, ok)
	}
}

func TestProviderCredentialRefreshServiceUsesNativeRefreshProfilePolicy(t *testing.T) {
	store := NewMemoryStore()
	providerType := "profile_oauth_subscription"
	resourceType := "profile_oauth_account"
	store.ConfigureProviderResourceTypePolicy(map[string][]string{providerType: []string{resourceType}})
	provider := store.AddProvider(Provider{
		Name:    "Profile OAuth",
		Type:    providerType,
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{
			providerCredentialRefreshProfileOption: providerCredentialRefreshProfileOpenAIAccountOAuth,
		},
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Profile OAuth Account", ResourceType: resourceType, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "profile-access-before", RefreshToken: "profile-refresh-before", ClientID: openAIAccountOAuthClientID,
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("refresh_token") != "profile-refresh-before" {
			t.Fatalf("unexpected refresh token: %v", r.Form)
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "profile-access-after", "refresh_token": "profile-refresh-after", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	newProviderCredentialRefreshService(store).RunDue(context.Background())
	if requests.Load() != 1 {
		t.Fatalf("expected one native refresh profile renewal request, got %d", requests.Load())
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "profile-access-after" || credentials.RefreshToken != "profile-refresh-after" {
		t.Fatalf("expected profile-refreshed credentials to be stored, got %+v", credentials)
	}
}

func TestProviderCredentialRefreshRequiresNativeRefreshProfilePolicy(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Legacy Codex Without Policy", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Legacy Codex Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "legacy-access-before", RefreshToken: "legacy-refresh-before", ClientID: openAIAccountOAuthClientID,
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected token renewal", http.StatusInternalServerError)
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("provider without refresh profile sent %d token requests, want 0", requests.Load())
	}
	if credentials.AccessToken != "legacy-access-before" || credentials.RefreshToken != "legacy-refresh-before" {
		t.Fatalf("unsupported provider credentials changed: %+v", credentials)
	}
}

func TestProviderCredentialRefreshServiceRunsPluginCredentialRefreshActions(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_kimi_scheduler", Name: "Kimi", Type: "kimi_subscription", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_kimi_scheduler",
		ProviderID:   provider.ID,
		Name:         "Kimi Scheduler Account",
		ResourceType: "kimi_oauth_account",
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AuthType:     "oauth",
			AccessToken:  "old-plugin-access",
			RefreshToken: "old-plugin-refresh",
			ExpiresAt:    time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-refresh-admin"})
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.kimi",
		Name:    "Kimi",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
	}, AdapterRegistration{
		Type:         "kimi_subscription",
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityOAuth},
	}); err != nil {
		t.Fatalf("register Kimi adapter plugin: %v", err)
	}
	var calls atomic.Int64
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi",
		ActionID:   "kimi.credentials.refresh",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "credentials.refresh",
		Subject:    "kimi_subscription",
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		calls.Add(1)
		if invocation.Actor.ID != "system" || invocation.Actor.Role != "system" {
			t.Fatalf("unexpected scheduler actor: %+v", invocation.Actor)
		}
		var payload struct {
			ResourceID string `json:"resource_id"`
			Force      bool   `json:"force"`
		}
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode scheduler refresh payload: %v", err)
		}
		if payload.ResourceID != resource.ID || payload.Force {
			t.Fatalf("unexpected scheduler refresh payload: %+v", payload)
		}
		return pluginmeta.ActionResult{Data: map[string]any{
			"credentials": map[string]string{
				"auth_type":     "oauth",
				"access_token":  "new-plugin-access",
				"refresh_token": "new-plugin-refresh",
				"email":         "scheduler@example.com",
			},
		}}, nil
	})); err != nil {
		t.Fatalf("register Kimi refresh action: %v", err)
	}

	newProviderCredentialRefreshService(store, server.refreshProviderResourceCredentialsWithPluginAction).RunDue(context.Background())

	if calls.Load() != 1 {
		t.Fatalf("expected one plugin refresh action call, got %d", calls.Load())
	}
	stored, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("expected refreshed resource to exist")
	}
	credentials := store.providerResourceCredentialsForRuntime(stored)
	if credentials.AccessToken != "new-plugin-access" || credentials.RefreshToken != "new-plugin-refresh" {
		t.Fatalf("expected plugin-refreshed credentials to be stored, got %+v", credentials)
	}
	if stored.CredentialSummary["credential_source"] != "kimi_oauth_account" ||
		stored.CredentialSummary["has_refresh_token"] != "true" ||
		stored.CredentialSummary["account_email"] != "scheduler@example.com" {
		t.Fatalf("expected plugin credential summary to be stored, got %+v", stored.CredentialSummary)
	}
}

func TestProviderCredentialRefreshServicePersistsPluginReauthorizationMarker(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_kimi_scheduler_reauth", Name: "Kimi", Type: "kimi_subscription", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_kimi_scheduler_reauth",
		ProviderID:   provider.ID,
		Name:         "Kimi Scheduler Account",
		ResourceType: "kimi_oauth_account",
		Status:       StatusActive,
		Healthy:      true,
		Credentials: &ProviderResourceCredentials{
			AuthType:     "oauth",
			AccessToken:  "old-plugin-access",
			RefreshToken: "old-plugin-refresh",
			ExpiresAt:    time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-refresh-admin"})
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.kimi",
		Name:    "Kimi",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
	}, AdapterRegistration{
		Type:         "kimi_subscription",
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityOAuth},
	}); err != nil {
		t.Fatalf("register Kimi adapter plugin: %v", err)
	}
	var calls atomic.Int64
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi",
		ActionID:   "kimi.credentials.refresh",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "credentials.refresh",
		Subject:    "kimi_subscription",
		Metadata:   map[string]string{"provider_resource_type": "kimi_oauth_account"},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		calls.Add(1)
		return pluginmeta.ActionResult{Data: map[string]any{"reauthorization_required": true}}, nil
	})); err != nil {
		t.Fatalf("register Kimi refresh action: %v", err)
	}

	service := newProviderCredentialRefreshService(store, server.refreshProviderResourceCredentialsWithPluginAction)
	service.RunDue(context.Background())
	service.RunDue(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("expected scheduler to stop retrying after reauthorization marker, got %d calls", calls.Load())
	}
	stored, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("expected provider resource to exist")
	}
	if stored.CredentialSummary[providerResourceReauthorizationRequiredOption] != "true" {
		t.Fatalf("expected plugin refresh to mark reauthorization, got %+v", stored.CredentialSummary)
	}
}

func TestProviderCredentialRefreshUpdatesDerivedIdentityClaims(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "Codex OAuth Identity")
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Codex OAuth Identity Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "identity-access-before", RefreshToken: "identity-refresh-before", ClientID: openAIAccountOAuthClientID,
			IDToken: testJWT(map[string]any{
				"email": "old@example.com",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "account-old", "user_id": "user-old", "chatgpt_plan_type": "free",
					"organizations": []map[string]any{{"id": "org-old", "is_default": true}},
				},
			}),
			AccountID: "account-old", UserID: "user-old", Email: "old@example.com", OrganizationID: "org-old", PlanType: "free",
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	refreshedIDToken := testJWT(map[string]any{
		"email": "new@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-new", "user_id": "user-new", "chatgpt_plan_type": "plus",
			"organizations": []map[string]any{{"id": "org-new", "is_default": true}},
		},
	})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "identity-access-after", "refresh_token": "identity-refresh-after", "id_token": refreshedIDToken, "expires_in": 3600,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccountID != "account-new" || credentials.UserID != "user-new" || credentials.Email != "new@example.com" ||
		credentials.OrganizationID != "org-new" || credentials.PlanType != "plus" {
		t.Fatalf("refreshed identity claims were not persisted: %+v", credentials)
	}
}

func TestProviderCredentialRefreshServiceSkipsHealthyOrUnsupportedResources(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "OAuth Resources")
	unsupportedProvider := store.AddProvider(Provider{Name: "API Key Resources", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true})
	for _, resource := range []ProviderResource{
		{ProviderID: provider.ID, Name: "No Refresh Token", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true, Credentials: &ProviderResourceCredentials{AccessToken: "access-no-refresh", ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}},
		{ProviderID: provider.ID, Name: "Disabled", ResourceType: ProviderResourceOpenAISubscription, Status: StatusDisabled, Healthy: true, Credentials: &ProviderResourceCredentials{AccessToken: "access-disabled", RefreshToken: "refresh-disabled", ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)}},
		{ProviderID: unsupportedProvider.ID, Name: "API Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, APIKey: "api-key"},
	} {
		if _, err := store.AddProviderResource(resource); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected token renewal", http.StatusInternalServerError)
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	newProviderCredentialRefreshService(store).RunDue(context.Background())
	if requests.Load() != 0 {
		t.Fatalf("expected no renewal requests, got %d", requests.Load())
	}
}

func TestProviderCredentialRefreshServiceStopsRetryingInvalidatedRefreshTokens(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "Invalidated OAuth")
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Invalidated Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{AccessToken: "expired-access", RefreshToken: "invalidated-refresh", ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"` + strings.Repeat("x", 260) + `","code":"refresh_token_invalidated"}}`))
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	if _, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true); AsHTTPError(err).Code != "provider_resource_reauthorization_required" {
		t.Fatalf("expected manual refresh to require reauthorization, got %v", err)
	}
	if _, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true); AsHTTPError(err).Code != "provider_resource_reauthorization_required" {
		t.Fatalf("expected marked credentials to require reauthorization without another request, got %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("marked credentials sent %d token requests, want 1", requests.Load())
	}
	service := newProviderCredentialRefreshService(store)
	service.RunDue(context.Background())
	if requests.Load() != 1 {
		t.Fatalf("expected one renewal attempt after token invalidation, got %d", requests.Load())
	}
	var stored *ProviderResource
	for _, candidate := range store.ListProviderResources() {
		if candidate.ID == resource.ID {
			stored = &candidate
			break
		}
	}
	if stored == nil {
		t.Fatal("expected provider resource to exist")
	}
	if stored.CredentialSummary[openAIAccountReauthorizationRequiredOption] != "true" {
		t.Fatalf("expected resource to require reauthorization, got %+v", stored.CredentialSummary)
	}
}

func TestProviderCredentialRefreshDoesNotInvalidateReplacementCredentials(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "Reauthorized OAuth")
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Reauthorized Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "access-before-reauthorization", RefreshToken: "refresh-before-reauthorization",
			ExpiresAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int64
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse refresh request: %v", err)
		}
		if token := r.Form.Get("refresh_token"); token != "refresh-before-reauthorization" {
			t.Errorf("unexpected refresh token: %q", token)
		}
		close(started)
		<-release
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_invalidated"}}`))
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	refreshDone := make(chan error, 1)
	go func() {
		_, refreshErr := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
		refreshDone <- refreshErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old credential refresh did not reach the token endpoint")
	}
	replacementExpiry := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{
		ProviderID:   provider.ID,
		Name:         resource.Name,
		ResourceType: ProviderResourceOpenAISubscription,
		BaseURL:      resource.BaseURL,
		Status:       StatusActive,
		Healthy:      true,
		Weight:       resource.Weight,
		Options:      map[string]string{"auth_type": "oauth"},
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "access-after-reauthorization", RefreshToken: "refresh-after-reauthorization", ExpiresAt: replacementExpiry,
		},
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-refreshDone; AsHTTPError(err).Code != "provider_resource_reauthorization_required" {
		t.Fatalf("expected the old refresh request to fail, got %v", err)
	}
	current, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("reauthorized resource disappeared")
	}
	if current.Options[openAIAccountReauthorizationRequiredOption] != "" {
		t.Fatalf("stale failure marked replacement credentials for reauthorization: %+v", current.Options)
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "access-after-reauthorization" || credentials.RefreshToken != "refresh-after-reauthorization" {
		t.Fatalf("stale failure changed replacement credentials: %+v", credentials)
	}
	if requests.Load() != 1 {
		t.Fatalf("replacement credential verification sent %d token requests, want 1", requests.Load())
	}
}

func TestProviderResourceCredentialMetadataPreservesOAuthState(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "OAuth Metadata")
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "OAuth Metadata Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "metadata-access", RefreshToken: "metadata-refresh", AccountID: "metadata-account",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.UpdateProviderResourceOptions(resource.ID, map[string]string{
		openAIAccountReauthorizationRequiredOption: "true",
		codexImageCapabilityOption:                 codexImageCapabilitySupported,
		codexImageCapabilityCheckedAtOption:        checkedAt,
	}); err != nil {
		t.Fatal(err)
	}

	updated, err := store.UpdateProviderResource(resource.ID, ProviderResource{
		ProviderID: resource.ProviderID, Name: resource.Name, ResourceType: resource.ResourceType,
		BaseURL: resource.BaseURL, Status: StatusActive, Healthy: true, Weight: resource.Weight,
		Credentials: &ProviderResourceCredentials{
			ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), Scopes: "openid profile",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CredentialSummary["has_refresh_token"] != "true" || updated.CredentialSummary[openAIAccountReauthorizationRequiredOption] != "true" {
		t.Fatalf("credential metadata edit lost OAuth state: %+v", updated.CredentialSummary)
	}
	if updated.Options[codexImageCapabilityOption] != codexImageCapabilitySupported || updated.Options[codexImageCapabilityCheckedAtOption] != checkedAt {
		t.Fatalf("credential metadata edit lost image capability: %+v", updated.Options)
	}
	persisted, _ := store.GetProviderResource(resource.ID)
	credentials := store.providerResourceCredentialsForRuntime(persisted)
	if credentials.AccessToken != "metadata-access" || credentials.RefreshToken != "metadata-refresh" || credentials.AccountID != "metadata-account" || credentials.Scopes != "openid profile" {
		t.Fatalf("credential metadata edit overwrote authentication material: %+v", credentials)
	}
}

func TestProviderCredentialRefreshSurvivesConcurrentMetadataEdit(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "Concurrent OAuth Metadata")
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Before Metadata Edit", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "metadata-old-access", RefreshToken: "metadata-old-refresh", AccountID: "metadata-account",
			ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	refreshedIDToken := testJWT(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "metadata-refreshed-account"},
	})
	started := make(chan struct{})
	release := make(chan struct{})
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "metadata-rotated-access", "refresh_token": "metadata-rotated-refresh", "id_token": refreshedIDToken, "expires_in": 3600,
		})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	refreshDone := make(chan error, 1)
	go func() {
		_, refreshErr := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
		refreshDone <- refreshErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("credential refresh did not reach the token endpoint")
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{
		ProviderID: resource.ProviderID, Name: "After Metadata Edit", ResourceType: resource.ResourceType,
		BaseURL: resource.BaseURL, Status: StatusActive, Healthy: true, Weight: resource.Weight,
		Credentials: &ProviderResourceCredentials{Scopes: "openid profile", AccountID: "metadata-operator-account"},
	}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	stored, _ := store.GetProviderResource(resource.ID)
	credentials := store.providerResourceCredentialsForRuntime(stored)
	if stored.Name != "After Metadata Edit" || credentials.AccessToken != "metadata-rotated-access" ||
		credentials.RefreshToken != "metadata-rotated-refresh" || credentials.AccountID != "metadata-operator-account" || credentials.Scopes != "openid profile" {
		t.Fatalf("concurrent metadata edit or rotated credentials were lost: resource=%+v credentials=%+v", stored, credentials)
	}
}

func TestProviderCredentialRefreshServiceRenewsAccountsConcurrently(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "Concurrent OAuth")
	for index, refreshToken := range []string{"blocked-1", "blocked-2", "blocked-3", "ready"} {
		if _, err := store.AddProviderResource(ProviderResource{
			ProviderID: provider.ID, Name: refreshToken, ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true, Priority: index,
			Credentials: &ProviderResourceCredentials{AccessToken: "expired-" + refreshToken, RefreshToken: refreshToken, ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	blocked := make(chan struct{}, 3)
	ready := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("refresh_token") == "ready" {
			ready <- struct{}{}
		} else {
			blocked <- struct{}{}
			<-release
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "renewed", "expires_in": 3600})
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

	done := make(chan struct{})
	go func() {
		newProviderCredentialRefreshService(store).RunDue(context.Background())
		close(done)
	}()
	defer func() {
		releaseAll()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("credential refresh workers did not finish")
		}
	}()
	for range 3 {
		select {
		case <-blocked:
		case <-time.After(time.Second):
			t.Fatal("expected the blocked account renewals to start")
		}
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("ready account was blocked behind slow account renewals")
	}
}

func TestProviderCredentialRefreshServiceDoesNotLogOAuthResponse(t *testing.T) {
	store := NewMemoryStore()
	provider := addOpenAIAccountRefreshProvider(store, "Logging OAuth")
	if _, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Logging Account", ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{AccessToken: "expired-access", RefreshToken: "refresh-secret", ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339)},
	}); err != nil {
		t.Fatal(err)
	}
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream-secret-session","token":"upstream-secret-token"}}`))
	}))
	defer tokenServer.Close()
	previousEndpoint := openAIAccountOAuthTokenEndpoint
	openAIAccountOAuthTokenEndpoint = tokenServer.URL
	defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousLogOutput)

	newProviderCredentialRefreshService(store).RunDue(context.Background())
	if output := logs.String(); strings.Contains(output, "upstream-secret") || strings.Contains(output, "refresh-secret") {
		t.Fatalf("OAuth response detail leaked to logs: %s", output)
	}
	if !strings.Contains(logs.String(), "code=oauth_token_failed") {
		t.Fatalf("expected stable OAuth error code in logs, got %s", logs.String())
	}
}
