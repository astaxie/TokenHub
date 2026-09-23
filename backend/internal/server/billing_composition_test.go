package server

import (
	"net/http"
	"strings"
	"testing"
)

// Store decorators are allowed to override a narrow capability without also
// exposing private composition hooks. They must remain constructible by New.
type storeContractOnlyDecorator struct{ Store }

func TestNewAcceptsStoreContractOnlyDecorator(t *testing.T) {
	base := NewMemoryStore()
	server := New(storeContractOnlyDecorator{Store: base})
	if server == nil || server.billing == nil || server.reconciliation == nil {
		t.Fatal("expected server services to be initialized")
	}
	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/billing/connectors", nil, "dev_admin_token")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body, "billing_repository_unavailable") {
		t.Fatalf("missing billing composition response = %d %s", response.Code, response.Body)
	}
}

func TestNewWithConfigAndBillingDependenciesSupportsStoreDecorator(t *testing.T) {
	base := NewMemoryStoreWithConfig(Config{SecretKey: "billing-composition-test-secret"})
	decorated := storeContractOnlyDecorator{Store: base}
	app := NewWithConfigAndBillingDependencies(decorated, Config{
		AdminToken: "billing-composition-admin",
		SecretKey:  "billing-composition-test-secret",
	}, BillingDependencies{
		Repository:           base.BillingRepositoryForComposition(),
		ReconciliationReader: base.BillingReaderForComposition(),
	}).Handler()

	response := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Decorated store connector",
		"type":     BillingConnectorOneAPI,
		"base_url": "https://billing.example.test",
	}, "billing-composition-admin")
	if response.Code != http.StatusCreated {
		t.Fatalf("create billing connector through decorated store: %d %s", response.Code, response.Body)
	}
}

func TestNewWithConfigAndBillingDependenciesRejectsMissingBilling(t *testing.T) {
	server := NewWithConfigAndBillingDependencies(NewMemoryStore(), Config{AdminToken: "dev_admin_token"}, BillingDependencies{})
	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/billing/connectors", nil, "dev_admin_token")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body, "billing_repository_unavailable") {
		t.Fatalf("missing billing dependency response = %d %s", response.Code, response.Body)
	}
}
