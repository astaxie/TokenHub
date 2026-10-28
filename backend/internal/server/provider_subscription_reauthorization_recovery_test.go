package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionReauthorizationPreservesAccountAndRecoversAfterProbe(t *testing.T) {
	for _, status := range []string{StatusActive, StatusDisabled} {
		t.Run(status, func(t *testing.T) {
			store := NewMemoryStore()
			store.failureThreshold = 2
			server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "reauthorization-recovery-test-secret"})
			t.Cleanup(func() { _ = server.Shutdown(context.Background()); _ = store.Close() })
			var exchanges, completions atomic.Int32
			adapter, _ := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					exchanges.Add(1)
					if r.Header.Get("Authorization") != "token synthetic-replacement-token" {
						t.Error("Probe exchanged the previous account credentials")
					}
					_, _ = fmt.Fprintf(w, `{"token":"synthetic-probe-session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
				case "/models":
					_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model","supported_endpoints":["/chat/completions"]}]}`)
				case "/chat/completions":
					completions.Add(1)
					if r.Header.Get("Authorization") != "Bearer synthetic-probe-session" {
						t.Error("Probe did not use the reauthorized account session")
					}
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
				default:
					t.Errorf("Unexpected reauthorization probe path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			adapter.RefreshCredentials = providerCredentialLoaderForType(store, ProviderGitHubCopilotSubscription)
			registerTestAdapter(server, ProviderGitHubCopilotSubscription, adapter)
			provider := store.AddProvider(Provider{Name: "Reauthorization recovery provider", Type: ProviderGitHubCopilotSubscription, Status: StatusActive, Healthy: true})
			resource, err := store.AddProviderResource(ProviderResource{
				ProviderID: provider.ID, Name: "Reauthorization recovery account", ResourceType: ProviderResourceGitHubCopilotSubscription,
				Group: "production", Status: StatusActive, Healthy: true, Priority: 7, Weight: 80,
				RateLimitRPM: 15, TokenLimitTPM: 4000, MaxConcurrency: 2, Region: "us", Environment: "production",
				Options:     map[string]string{"operator_note": "retained"},
				Credentials: &ProviderResourceCredentials{AccessToken: "synthetic-expired-token", ExpiresAt: "2000-01-01T00:00:00Z"},
			})
			if err != nil {
				t.Fatal(err)
			}
			store.AddModel(Model{Name: "reauthorization-model", Modality: "chat", Status: StatusActive})
			store.AddRoute(ModelRoute{ModelName: "reauthorization-model", ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "fixture-model", Priority: 4, Weight: 100, Status: StatusActive})
			routes := store.ListRoutes()
			tripBreaker(t, store, resource.ID)
			current := findResource(t, store, resource.ID)
			options := cloneStringMap(current.Options)
			staleKeys := []string{providerResourceReauthorizationRequiredOption, providerResourceSupportedModelsOption, providerResourceModelsFetchedAtOption, providerResourceModelCatalogOption}
			options[staleKeys[0]] = "true"
			options[staleKeys[1]] = `["old-model"]`
			options[staleKeys[2]] = time.Now().UTC().Format(time.RFC3339Nano)
			options[staleKeys[3]] = `[{"id":"old-model"}]`
			encodedOptions, err := json.Marshal(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Updates(map[string]any{"options": string(encodedOptions), "status": status}).Error; err != nil {
				t.Fatal(err)
			}
			if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, `{"remaining_percent":0}`, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			before := findResource(t, store, resource.ID)
			if before.Healthy || before.FailureCount != 2 || before.CooldownUntil == nil || before.Options[providerResourceReauthorizationRequiredOption] != "true" {
				t.Fatal("Fixture did not retain expired authorization and open breaker state")
			}
			response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, map[string]any{
				"credentials": map[string]any{"replace": true, "access_token": "synthetic-replacement-token", "expires_at": "2099-01-01T00:00:00Z"},
			}, "dev_admin_token")
			if response.Code != http.StatusOK {
				t.Fatalf("Reauthorization save failed: status=%d body=%s", response.Code, response.Body)
			}
			saved := findResource(t, store, resource.ID)
			settings := func(value ProviderResource) any {
				return []any{value.ID, value.ProviderID, value.Name, value.ResourceType, value.Group, value.BaseURL, value.Region, value.Environment,
					value.Status, value.Priority, value.Weight, value.RateLimitRPM, value.TokenLimitTPM, value.MaxConcurrency, value.Options["operator_note"]}
			}
			if !reflect.DeepEqual(settings(before), settings(saved)) || !reflect.DeepEqual(routes, store.ListRoutes()) {
				t.Fatal("Reauthorization changed the account identity, route, or scheduling configuration")
			}
			for _, key := range staleKeys {
				if saved.Options[key] != "" {
					t.Errorf("Reauthorization retained stale account state: %s", key)
				}
			}
			observation, ok := store.GetProviderResourceObservation(resource.ID)
			if !ok || observation.QuotaSnapshot != "" || observation.QuotaFetchedAt != nil {
				t.Fatal("Reauthorization retained the previous account quota")
			}
			if saved.Healthy || saved.FailureCount != before.FailureCount || saved.CooldownUntil == nil || !saved.CooldownUntil.Equal(*before.CooldownUntil) {
				t.Fatal("Saving credentials cleared the breaker without a successful upstream probe")
			}
			response = doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+provider.Type+"/probe.run", map[string]any{
				"resource_id": resource.ID, "resource_type": resource.ResourceType, "model": "fixture-model", "prompt": "Reply with OK.",
			}, "dev_admin_token")
			if response.Code != http.StatusOK || exchanges.Load() != 1 || completions.Load() != 1 {
				t.Fatalf("Reauthorized account probe failed: status=%d exchanges=%d completions=%d body=%s", response.Code, exchanges.Load(), completions.Load(), response.Body)
			}
			recovered := findResource(t, store, resource.ID)
			if !reflect.DeepEqual(settings(before), settings(recovered)) || !reflect.DeepEqual(routes, store.ListRoutes()) {
				t.Fatal("Successful probe changed the account identity, route, or scheduling configuration")
			}
			if status == StatusActive {
				if !recovered.Healthy || recovered.FailureCount != 0 || recovered.CooldownUntil != nil {
					t.Fatal("Successful probe did not recover the active reauthorized account")
				}
			} else if recovered.Healthy || recovered.Status != StatusDisabled || recovered.FailureCount != before.FailureCount || recovered.CooldownUntil == nil || !recovered.CooldownUntil.Equal(*before.CooldownUntil) {
				t.Fatal("Successful probe enabled or reset an administrator-disabled account")
			}
		})
	}
}
