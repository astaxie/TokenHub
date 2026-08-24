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
)

func TestProviderCredentialRefreshServiceRenewsExpiringOpenAIAccounts(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Codex OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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

func TestProviderCredentialRefreshUpdatesDerivedIdentityClaims(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Codex OAuth Identity", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "OAuth Resources", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "Invalidated OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "Reauthorized OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "OAuth Metadata", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "Concurrent OAuth Metadata", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "Concurrent OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
	provider := store.AddProvider(Provider{Name: "Logging OAuth", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
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
