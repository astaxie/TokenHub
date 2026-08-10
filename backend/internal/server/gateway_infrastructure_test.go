package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGatewayInfrastructureQueriesAreFilteredAndDoNotExposeSecrets(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "provider_gateway", Name: "Gateway OpenAI", Type: ProviderOpenAI,
		BaseURL: "https://provider.example/v1", APIKey: "provider-secret", Status: StatusActive,
		Healthy: true, Priority: 2, Headers: map[string]string{"authorization": "secret-header"},
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID: "resource_gateway", ProviderID: provider.ID, Name: "Primary resource",
		APIKey: "resource-secret", Status: StatusActive, Healthy: true,
	}); err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ID: "route_gateway", ModelName: "gpt-gateway", ProviderID: provider.ID,
		ProviderResourceID: "resource_gateway", ProviderModel: "gpt-provider",
		Priority: 1, Weight: 100, Strategy: RouteStrategyBalanced, Status: StatusActive,
	})
	app := NewWithConfig(store, Config{AdminToken: "admin_token", IntegrationToken: "integration_token", SecretKey: "test_secret"}).Handler()

	unauthorized := doJSON(t, app, http.MethodGet, "/api/internal/providers", nil, "admin_token")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected admin token rejection, got %d: %s", unauthorized.Code, unauthorized.Body)
	}
	providers := doJSON(t, app, http.MethodGet, "/api/internal/providers?name=GATEWAY&type=openai&status=active&health=healthy&page=1&page_size=10", nil, "integration_token")
	if providers.Code != http.StatusOK {
		t.Fatalf("expected provider list, got %d: %s", providers.Code, providers.Body)
	}
	if strings.Contains(providers.Body, "provider-secret") || strings.Contains(providers.Body, "resource-secret") || strings.Contains(providers.Body, "secret-header") || strings.Contains(providers.Body, "headers") || strings.Contains(providers.Body, "options") {
		t.Fatalf("provider response exposed sensitive configuration: %s", providers.Body)
	}
	var providerPayload struct {
		Data  []gatewayProviderItem `json:"data"`
		Total int                   `json:"total"`
	}
	if err := json.Unmarshal([]byte(providers.Body), &providerPayload); err != nil {
		t.Fatal(err)
	}
	if providerPayload.Total != 1 || len(providerPayload.Data) != 1 || providerPayload.Data[0].ResourceCount != 1 || providerPayload.Data[0].HealthyResourceCount != 1 {
		t.Fatalf("unexpected provider response: %+v", providerPayload)
	}

	routes := doJSON(t, app, http.MethodGet, "/api/internal/routes?model_name=GPT&provider_id=provider_gateway&strategy=balanced&status=active&page=1&page_size=10", nil, "integration_token")
	if routes.Code != http.StatusOK {
		t.Fatalf("expected route list, got %d: %s", routes.Code, routes.Body)
	}
	var routePayload struct {
		Data  []gatewayRouteItem `json:"data"`
		Total int                `json:"total"`
	}
	if err := json.Unmarshal([]byte(routes.Body), &routePayload); err != nil {
		t.Fatal(err)
	}
	if routePayload.Total != 1 || len(routePayload.Data) != 1 || routePayload.Data[0].ProviderName != provider.Name || routePayload.Data[0].ResourceName != "Primary resource" {
		t.Fatalf("unexpected route response: %+v", routePayload)
	}
	invalid := doJSON(t, app, http.MethodGet, "/api/internal/providers?health=unknown", nil, "integration_token")
	if invalid.Code != http.StatusBadRequest || !jsonBodyHasCode(invalid.Body, "invalid_provider_query") {
		t.Fatalf("expected invalid provider filter rejection, got %d: %s", invalid.Code, invalid.Body)
	}
	hugePage := doJSON(t, app, http.MethodGet, "/api/internal/routes?page=9223372036854775807&page_size=100", nil, "integration_token")
	if hugePage.Code != http.StatusBadRequest || !jsonBodyHasCode(hugePage.Body, "invalid_route_query") {
		t.Fatalf("expected oversized page rejection, got %d: %s", hugePage.Code, hugePage.Body)
	}
}

func TestGatewayIntegrationReadersPropagateDatabaseErrors(t *testing.T) {
	store := NewMemoryStore()
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := listGatewayModelsForIntegration(store); err == nil {
		t.Fatal("expected model query database error")
	}
	if _, err := listGatewayProvidersForIntegration(store); err == nil {
		t.Fatal("expected provider query database error")
	}
	if _, err := listGatewayProviderResourcesForIntegration(store); err == nil {
		t.Fatal("expected provider resource query database error")
	}
	if _, err := listGatewayRoutesForIntegration(store); err == nil {
		t.Fatal("expected route query database error")
	}
}
