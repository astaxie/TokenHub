package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type replacementModelFailureAdapter struct {
	duringRequest func()
}

func (a replacementModelFailureAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	a.duringRequest()
	return nil, Usage{}, &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadGateway, "provider_model_not_found", "Previous account does not support this model"),
		Disposition: ProviderErrorModelUnsupported,
	}
}

func TestModelRejectionFromPreviousAccountPreservesReplacementCatalog(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	provider := store.AddProvider(Provider{Name: "Replacement catalog", Type: ProviderChatGPTSubscription, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Subscription account", ResourceType: ProviderResourceChatGPTSubscription,
		Status: StatusActive, Healthy: true, Credentials: &ProviderResourceCredentials{AccessToken: "previous-access"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "shared-model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "shared-model", ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "shared-model", Status: StatusActive})
	registerTestAdapter(server, provider.Type, replacementModelFailureAdapter{duringRequest: func() {
		response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID,
			map[string]any{"credentials": map[string]any{"replace": true, "access_token": "replacement-access"}}, "dev_admin_token")
		if response.Code != http.StatusOK {
			t.Fatalf("account replacement failed: %s", response.Body)
		}
		current, _ := store.GetProviderResource(resource.ID)
		if err := server.persistProviderResourceModelsIfCurrent(context.Background(), resource.ID, []ProviderCatalogModel{{ID: "shared-model", DisplayName: "Replacement model"}}, time.Now(), current); err != nil {
			t.Fatal(err)
		}
	}})
	routes, err := store.SelectRouteCandidates("shared-model")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = server.executeRoutedResponses(httptest.NewRequest(http.MethodPost, "/v1/responses", nil), RoutedCall{
		Call: CallContext{RequestID: "replacement-catalog", Model: Model{Name: "shared-model", Status: StatusActive}}, Routes: routes,
	}, ResponsesRequest{Model: "shared-model", Input: "Hello"})
	if !providerResourceModelUnsupportedError(err) {
		t.Fatalf("expected previous account model rejection: %v", err)
	}
	current, _ := store.GetProviderResource(resource.ID)
	models, _, cached := providerResourceCachedModels(&current)
	if !cached || !providerResourceModelInList("shared-model", models) {
		t.Fatalf("previous account rejection removed replacement account model: cached=%v models=%v", cached, models)
	}
}
