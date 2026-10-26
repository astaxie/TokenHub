package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestSubscriptionObservationsRejectRecreatedOrMovedAccount(t *testing.T) {
	for _, operation := range []string{"quota.read", "models.read"} {
		for _, mutation := range []string{"recreate", "move"} {
			t.Run(operation+"/"+mutation, func(t *testing.T) {
				store := NewMemoryStore()
				server := New(store)
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				definition := subscriptionWorkerDefinition{"qoder", "Qoder"}
				provider := store.AddProvider(Provider{Name: "Original provider", Type: definition.providerType(), Status: StatusActive})
				other := store.AddProvider(Provider{Name: "Other provider", Type: definition.providerType(), Status: StatusActive})
				resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Original account", ResourceType: definition.resourceType(), Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"old-account"}`}})
				if err != nil {
					t.Fatal(err)
				}
				adapter, ok := resolveTypedAdapter[*SubscriptionWorkerAdapter](server.adapterRegistry, definition.providerType())
				if !ok {
					t.Fatal("subscription adapter missing")
				}
				started, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				adapter.Pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
					return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
						close(started)
						<-release
						result := `{"plan":"old-account-plan","windows":[{"used":100}]}`
						if command["operation"] == "models" {
							result = `{"models":[{"id":"old-account-model","protocol":"chat"}]}`
						}
						return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(result)}}
					}), nil
				}
				responses := make(chan responseBody, 1)
				go func() {
					responses <- doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+definition.providerType()+"/"+operation, map[string]any{"resource_id": resource.ID, "resource_type": resource.ResourceType, "refresh": true}, "dev_admin_token")
				}()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("observation did not start")
				}
				if mutation == "recreate" {
					if err := store.DeleteProviderResource(resource.ID); err != nil {
						t.Fatal(err)
					}
					_, err = store.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: "Replacement account", ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"new-account"}`}})
				} else {
					_, err = store.UpdateProviderResource(resource.ID, ProviderResource{ProviderID: other.ID})
				}
				if err != nil {
					t.Fatal(err)
				}
				unblock()
				select {
				case response := <-responses:
					if response.Code != http.StatusConflict {
						t.Errorf("stale observation status = %d, want 409", response.Code)
					}
					current, _ := store.GetProviderResource(resource.ID)
					if models, _, cached := providerResourceCachedModels(&current); cached {
						t.Errorf("stale models persisted: %v", models)
					}
					if quota, cached := server.cachedProviderMonitoringQuota(resource.ID, 0); cached {
						t.Errorf("stale quota persisted: %+v", quota)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("observation did not finish")
				}
			})
		}
	}
}

type observationIdentityModelAdapter struct {
	duringRequest func()
}

func (a observationIdentityModelAdapter) ResourceModels(context.Context, Provider, ProviderResource, string) (ProviderCatalogEntry, int, error) {
	a.duringRequest()
	return ProviderCatalogEntry{}, http.StatusNotModified, nil
}

func TestRecreatedAccountDoesNotReuseNotModifiedCatalog(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	provider := store.AddProvider(Provider{Name: "Cached subscription", Type: ProviderChatGPTSubscription, Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Original account", ResourceType: ProviderResourceChatGPTSubscription, Credentials: &ProviderResourceCredentials{AccessToken: "synthetic-original"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.persistProviderResourceModelsIfCurrent(context.Background(), resource.ID, []ProviderCatalogModel{{ID: "original-model", Metadata: map[string]string{"models_etag": "original-etag"}}}, time.Now(), resource); err != nil {
		t.Fatal(err)
	}
	registerTestAdapter(server, provider.Type, observationIdentityModelAdapter{duringRequest: func() {
		if err := store.DeleteProviderResource(resource.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: "Replacement account", ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{AccessToken: "synthetic-replacement"}}); err != nil {
			t.Fatal(err)
		}
	}})
	catalog, _, err := server.queryProviderResourceModelsForCatalog(context.Background(), provider.Type, resource.ID)
	if err == nil || AsHTTPError(err).Status != http.StatusConflict || len(catalog.Models) != 0 {
		t.Fatalf("recreated account received old cached catalog: %+v, %v", catalog, err)
	}
}
