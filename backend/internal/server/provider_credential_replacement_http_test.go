package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCredentialReplacementHTTPPreservesOmittedResourceSettings(t *testing.T) {
	for _, explicitReset := range []bool{false, true} {
		name := "credentials only"
		if explicitReset {
			name = "explicit setting resets"
		}
		t.Run(name, func(t *testing.T) {
			store := NewMemoryStore()
			server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "replacement-test-secret"})
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			provider := store.AddProvider(Provider{Name: "Replacement provider", Type: ProviderGitHubCopilotSubscription, Status: StatusActive, Healthy: true})
			resource, err := store.AddProviderResource(ProviderResource{
				ProviderID: provider.ID, Name: "Replacement account", Group: "production", ResourceType: ProviderResourceGitHubCopilotSubscription,
				BaseURL: "https://api.githubcopilot.com/custom", Region: "us", Environment: "production", Status: StatusActive,
				Healthy: true, Priority: 7, Weight: 80, RateLimitRPM: 15, TokenLimitTPM: 4000, MaxConcurrency: 2,
				Options: map[string]string{"operator_note": "retained"}, Credentials: &ProviderResourceCredentials{AccessToken: "old-account-token"},
			})
			if err != nil {
				t.Fatal(err)
			}
			patch := map[string]any{"credentials": map[string]any{"replace": true, "access_token": "replacement-account-token"}}
			if explicitReset {
				for _, field := range []string{"base_url", "region", "environment"} {
					patch[field] = ""
				}
				patch["healthy"] = false
				for _, field := range []string{"rate_limit_rpm", "token_limit_tpm", "max_concurrency"} {
					patch[field] = 0
				}
			}
			response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, patch, "")
			if response.Code != http.StatusOK {
				t.Fatalf("credential replacement failed: status=%d body=%s", response.Code, response.Body)
			}
			updated, ok := store.GetProviderResource(resource.ID)
			if !ok {
				t.Fatal("replacement removed the resource")
			}
			want := resource
			if explicitReset {
				want.BaseURL, want.Region, want.Environment = copilotSubscriptionBaseURL, "", ""
				want.Healthy, want.RateLimitRPM, want.TokenLimitTPM, want.MaxConcurrency = false, 0, 0, 3
			}
			settings := func(value ProviderResource) any {
				return []any{value.ProviderID, value.Name, value.Group, value.ResourceType, value.BaseURL, value.Region, value.Environment,
					value.Status, value.Healthy, value.Priority, value.Weight, value.RateLimitRPM, value.TokenLimitTPM, value.MaxConcurrency, value.Options["operator_note"]}
			}
			if !reflect.DeepEqual(settings(updated), settings(want)) {
				t.Fatalf("replacement changed resource settings: got=%v want=%v", settings(updated), settings(want))
			}
			credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
			if err != nil || credentials.AccessToken != "replacement-account-token" {
				t.Fatalf("replacement credentials were not saved: %v", err)
			}
			if strings.Contains(response.Body, "account-token") || strings.Contains(response.Body, "replacementFields") {
				t.Fatal("replacement exposed credentials or operation metadata")
			}
		})
	}
}

func TestCredentialReplacementInvalidatesCachedQuotaWithoutClearingHealth(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "replacement-quota-test-secret"})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	provider := store.AddProvider(Provider{Name: "Quota replacement", Type: providerClaudeSubscription, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Quota replacement account", ResourceType: providerResourceClaudeSubscription,
		Healthy: true, Credentials: &ProviderResourceCredentials{AccessToken: "old-account-token"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	quota, _ := json.Marshal(OpenAIAccountQuota{PlanType: "old-plan", FetchedAt: now.Unix()})
	if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, string(quota), now); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&ProviderResourceObservation{}).Where("resource_id = ?", resource.ID).Update("served_model", "historical-model").Error; err != nil {
		t.Fatal(err)
	}
	if _, ok := server.cachedProviderMonitoringQuota(resource.ID, 0); !ok {
		t.Fatal("test quota was not cached")
	}
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID,
		map[string]any{"credentials": map[string]any{"replace": true, "access_token": "replacement-account-token"}}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("replacement failed: status=%d body=%s", response.Code, response.Body)
	}
	if _, ok := server.cachedProviderMonitoringQuota(resource.ID, 0); ok {
		t.Error("replacement retained the previous account's cached quota")
	}
	observation, ok := store.GetProviderResourceObservation(resource.ID)
	if !ok || observation.ServedModel != "historical-model" || observation.QuotaSnapshot != "" || observation.QuotaFetchedAt != nil {
		t.Errorf("quota invalidation changed unrelated observation fields: %+v", observation)
	}
	updated, _ := store.GetProviderResource(resource.ID)
	if !updated.Healthy {
		t.Error("replacement changed resource health")
	}
}
