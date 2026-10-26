package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestCredentialReplacementRejectsLateSubscriptionQuota(t *testing.T) {
	for _, priorReplacements := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("after_%d_replacements", priorReplacements), func(t *testing.T) {
			testCredentialReplacementRejectsLateSubscriptionQuota(t, priorReplacements)
		})
	}
}

func testCredentialReplacementRejectsLateSubscriptionQuota(t *testing.T, priorReplacements int) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	definition := subscriptionWorkerDefinition{"qoder", "Qoder"}
	adapter, ok := resolveTypedAdapter[*SubscriptionWorkerAdapter](server.adapterRegistry, definition.providerType())
	if !ok {
		t.Fatal("subscription adapter is missing")
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	adapter.Pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if command["operation"] != "usage" {
				t.Errorf("unexpected account operation: %s", command["operation"])
			}
			close(started)
			<-release
			return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"plan":"previous-account-plan","windows":[{"used":100}]}`)}}
		}), nil
	}
	provider := store.AddProvider(Provider{Name: "Subscription replacement", Type: definition.providerType(), Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Subscription account", ResourceType: definition.resourceType(), Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"previous-account-key"}`}})
	if err != nil {
		t.Fatal(err)
	}
	for index := range priorReplacements {
		replaceSubscriptionObservationCredentials(t, server, resource.ID, fmt.Sprintf("prior-account-%d", index))
	}
	result := make(chan responseBody, 1)
	go func() {
		result <- doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+definition.providerType()+"/quota.read",
			map[string]any{"resource_id": resource.ID, "resource_type": definition.resourceType(), "refresh": true}, "dev_admin_token")
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("quota request did not start")
	}
	replaceSubscriptionObservationCredentials(t, server, resource.ID, "replacement-account-key")
	unblock()
	select {
	case response := <-result:
		if response.Code != http.StatusConflict {
			t.Errorf("late account observation returned status %d, want 409", response.Code)
		}
		updated, _ := store.GetProviderResource(resource.ID)
		if generation := providerCredentialReplacementGeneration(updated.Options); generation != int64(priorReplacements+1) {
			t.Errorf("replacement generation = %d, want %d", generation, priorReplacements+1)
		}
		if quota, cached := server.cachedProviderMonitoringQuota(resource.ID, 0); cached {
			t.Fatalf("late response cached previous account quota after replacement: status=%d quota=%+v", response.Code, quota)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quota request did not finish")
	}
}

func TestCredentialReplacementRejectsLateSubscriptionModels(t *testing.T) {
	for _, priorReplacements := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("after_%d_replacements", priorReplacements), func(t *testing.T) {
			testCredentialReplacementRejectsLateSubscriptionModels(t, priorReplacements)
		})
	}
}

func testCredentialReplacementRejectsLateSubscriptionModels(t *testing.T, priorReplacements int) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	definition := subscriptionWorkerDefinition{"qoder", "Qoder"}
	adapter, ok := resolveTypedAdapter[*SubscriptionWorkerAdapter](server.adapterRegistry, definition.providerType())
	if !ok {
		t.Fatal("subscription adapter is missing")
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	adapter.Pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if command["operation"] != "models" {
				t.Errorf("unexpected account operation: %s", command["operation"])
			}
			close(started)
			<-release
			return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"previous-account-model","name":"Previous account model","protocol":"chat"}]}`)}}
		}), nil
	}
	provider := store.AddProvider(Provider{Name: "Subscription replacement", Type: definition.providerType(), Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Subscription account", ResourceType: definition.resourceType(), Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"previous-account-key"}`}})
	if err != nil {
		t.Fatal(err)
	}
	for index := range priorReplacements {
		replaceSubscriptionObservationCredentials(t, server, resource.ID, fmt.Sprintf("prior-account-%d", index))
	}
	result := make(chan responseBody, 1)
	go func() {
		result <- doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+definition.providerType()+"/models.read",
			map[string]any{"resource_id": resource.ID, "resource_type": definition.resourceType()}, "dev_admin_token")
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model discovery did not start")
	}
	replaceSubscriptionObservationCredentials(t, server, resource.ID, "replacement-account-key")
	unblock()
	select {
	case response := <-result:
		if response.Code != http.StatusConflict {
			t.Errorf("late account observation returned status %d, want 409", response.Code)
		}
		updated, _ := store.GetProviderResource(resource.ID)
		if generation := providerCredentialReplacementGeneration(updated.Options); generation != int64(priorReplacements+1) {
			t.Errorf("replacement generation = %d, want %d", generation, priorReplacements+1)
		}
		if models, _, cached := providerResourceCachedModels(&updated); cached {
			t.Fatalf("late response cached previous account models after replacement: status=%d models=%v", response.Code, models)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("model discovery did not finish")
	}
}

func replaceSubscriptionObservationCredentials(t *testing.T, server *Server, resourceID, key string) string {
	t.Helper()
	session := fmt.Sprintf(`{"type":"api","key":%q}`, key)
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resourceID,
		map[string]any{"credentials": map[string]any{"replace": true, "session_json": session}}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("credential replacement failed: status=%d body=%s", response.Code, response.Body)
	}
	return session
}

func TestCredentialReplacementGenerationAllowsObservationAfterSessionRotation(t *testing.T) {
	for _, capability := range []string{"quota.read", "models.read"} {
		t.Run(capability, func(t *testing.T) {
			store := NewMemoryStore()
			server := New(store)
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			definition := subscriptionWorkerDefinition{"qoder", "Qoder"}
			provider := store.AddProvider(Provider{Name: "Subscription rotation", Type: definition.providerType(), Status: StatusActive})
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Subscription account", ResourceType: definition.resourceType(), Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"initial-account-key"}`}})
			if err != nil {
				t.Fatal(err)
			}
			replaceSubscriptionObservationCredentials(t, server, resource.ID, "first-account-key")
			current := replaceSubscriptionObservationCredentials(t, server, resource.ID, "second-account-key")
			adapter, ok := resolveTypedAdapter[*SubscriptionWorkerAdapter](server.adapterRegistry, definition.providerType())
			if !ok {
				t.Fatal("subscription adapter is missing")
			}
			const rotated = `{"type":"api","key":"rotated-account-key"}`
			adapter.Pool.start = func(_ *http.Client, auth string, save func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					if auth != current {
						t.Error("observation loaded a different account")
					}
					if err := save(context.Background(), auth, rotated); err != nil {
						t.Errorf("session rotation failed: %v", err)
					}
					result := `{"plan":"current-account-plan","windows":[{"used":20}]}`
					if command["operation"] == "models" {
						result = `{"models":[{"id":"current-account-model","name":"Current account model","protocol":"chat"}]}`
					}
					return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(result)}}
				}), nil
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+definition.providerType()+"/"+capability,
				map[string]any{"resource_id": resource.ID, "resource_type": definition.resourceType(), "refresh": true}, "dev_admin_token")
			if response.Code != http.StatusOK {
				t.Fatalf("rotation prevented observation persistence: status=%d body=%s", response.Code, response.Body)
			}
			updated, _ := store.GetProviderResource(resource.ID)
			if generation := providerCredentialReplacementGeneration(updated.Options); generation != 2 {
				t.Errorf("session rotation changed generation: got %d, want 2", generation)
			}
			credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
			if err != nil || credentials.SessionJSON != rotated {
				t.Fatalf("rotated credentials were not saved: %v", err)
			}
			if capability == "quota.read" {
				if quota, cached := server.cachedProviderMonitoringQuota(resource.ID, 0); !cached || quota["plan_type"] != "current-account-plan" {
					t.Fatalf("current account quota was not cached: %+v", quota)
				}
			} else if models, _, cached := providerResourceCachedModels(&updated); !cached || len(models) != 1 || models[0] != "current-account-model" {
				t.Fatalf("current account models were not cached: %v", models)
			}
		})
	}
}
