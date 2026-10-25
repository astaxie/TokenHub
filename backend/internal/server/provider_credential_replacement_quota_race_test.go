package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestCredentialReplacementRejectsLateSubscriptionQuota(t *testing.T) {
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
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID,
		map[string]any{"credentials": map[string]any{"replace": true, "session_json": `{"type":"api","key":"replacement-account-key"}`}}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("credential replacement failed: status=%d body=%s", response.Code, response.Body)
	}
	unblock()
	select {
	case response := <-result:
		if quota, cached := server.cachedProviderMonitoringQuota(resource.ID, 0); cached {
			t.Fatalf("late response cached previous account quota after replacement: status=%d quota=%+v", response.Code, quota)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quota request did not finish")
	}
}

func TestCredentialReplacementRejectsLateSubscriptionModels(t *testing.T) {
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
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID,
		map[string]any{"credentials": map[string]any{"replace": true, "session_json": `{"type":"api","key":"replacement-account-key"}`}}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("credential replacement failed: status=%d body=%s", response.Code, response.Body)
	}
	unblock()
	select {
	case response := <-result:
		updated, _ := store.GetProviderResource(resource.ID)
		if models, _, cached := providerResourceCachedModels(&updated); cached {
			t.Fatalf("late response cached previous account models after replacement: status=%d models=%v", response.Code, models)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("model discovery did not finish")
	}
}
