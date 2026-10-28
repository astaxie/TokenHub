package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestExpiredSubscriptionManagementKeepsAdministratorAuthenticated(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	definition := subscriptionWorkerDefinition{"qoder", "Qoder"}
	adapter, ok := resolveTypedAdapter[*SubscriptionWorkerAdapter](server.adapterRegistry, definition.providerType())
	if !ok {
		t.Fatal("subscription adapter is missing")
	}
	adapter.Pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if command["operation"] != "usage" && command["operation"] != "checkin" {
				t.Errorf("unexpected account operation: %s", command["operation"])
			}
			return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"signIn":"expired","error":"synthetic-secret"}`)}}
		}), nil
	}
	provider := store.AddProvider(Provider{Name: "Expired subscription", Type: definition.providerType(), Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Expired account", ResourceType: definition.resourceType(), Credentials: &ProviderResourceCredentials{SessionJSON: `{"type":"api","key":"synthetic-secret"}`}})
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range []string{"quota.read", "checkin.run"} {
		t.Run(capability, func(t *testing.T) {
			path := "/api/admin/provider-actions/" + definition.providerType() + "/" + capability
			result := doJSON(t, server.Handler(), http.MethodPost, path, map[string]any{"resource_id": resource.ID, "resource_type": definition.resourceType(), "refresh": true}, "dev_admin_token")
			if result.Code != http.StatusConflict || !strings.Contains(result.Body, "provider_resource_reauthorization_required") {
				t.Fatalf("provider expiry must not invalidate the administrator session: status=%d body=%s", result.Code, result.Body)
			}
			if strings.Contains(result.Body, "synthetic-secret") {
				t.Fatal("provider expiry exposed upstream credentials")
			}
		})
	}
}
