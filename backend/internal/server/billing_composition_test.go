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
	dependencies := ApplicationDependenciesForStore(base)
	dependencies.ReconciliationStore = nil
	app := NewWithConfigAndBillingDependencies(decorated, Config{
		AdminToken: "billing-composition-admin",
		SecretKey:  "billing-composition-test-secret",
	}, dependencies).Handler()

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
	reconciliation := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/billing/reconciliations", nil, "dev_admin_token")
	if reconciliation.Code != http.StatusServiceUnavailable || !strings.Contains(reconciliation.Body, "reconciliation_store_unavailable") {
		t.Fatalf("missing reconciliation dependency response = %d %s", reconciliation.Code, reconciliation.Body)
	}
}

func TestReconciliationCompositionAllowsReadEndpointsWithoutBillingReader(t *testing.T) {
	base := NewMemoryStore()
	dependencies := ApplicationDependenciesForStore(base)
	dependencies.Repository = nil
	dependencies.ReconciliationReader = nil
	server := NewWithConfigAndBillingDependencies(base, Config{AdminToken: "read-only-admin"}, dependencies)
	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/billing/reconciliation-rules", nil, "read-only-admin")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, `"data"`) {
		t.Fatalf("read-only reconciliation composition response = %d %s", response.Code, response.Body)
	}
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/billing/reconciliation-rules/missing"},
		{http.MethodGet, "/api/admin/billing/reconciliations"},
		{http.MethodGet, "/api/admin/billing/reconciliations/missing"},
		{http.MethodPost, "/api/admin/billing/reconciliations/missing/lock"},
		{http.MethodGet, "/api/admin/billing/reconciliations/missing/export"},
	} {
		got := doJSON(t, server.Handler(), route.method, route.path, nil, "read-only-admin")
		if got.Code == http.StatusServiceUnavailable {
			t.Fatalf("read-only endpoint was disabled: %s %s", route.method, route.path)
		}
	}
	write := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/billing/reconciliation-rules", map[string]any{"name": "blocked"}, "read-only-admin")
	if write.Code != http.StatusServiceUnavailable || !strings.Contains(write.Body, "reconciliation_store_unavailable") {
		t.Fatalf("read-only reconciliation execution response = %d %s", write.Code, write.Body)
	}
}
