package server

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
)

const modelCreateOnlyAdminToken = "strict-create-test-admin-token"

func TestAdminModelCreateOnlyPublishesAndRejectsStaleRetry(t *testing.T) {
	for _, source := range []string{"", "tokenhub-standard-catalog", "public-provider-conf"} {
		t.Run("source_"+source, func(t *testing.T) {
			store, app, provider := modelCreateOnlyHTTPFixture(t)
			if source != "" {
				store.AddModel(Model{Name: "selected-model", InputPriceUSDPer1M: 99, Metadata: map[string]string{"source": source}})
			}
			payload := map[string]any{
				"create_only": true, "name": "selected-model", "modality": "chat", "input_price_usd_per_1m": 2,
				"routes": []ModelRoute{{ProviderID: provider.ID, ProviderModel: "selected-model", Weight: 100, Status: StatusActive}},
			}
			response := methodRoutingJSONRequest(t, app.Handler(), http.MethodPost, "/api/admin/models", payload, modelCreateOnlyAdminToken)
			if response.Code != http.StatusCreated {
				t.Fatalf("strict creation failed: %d %s", response.Code, response.Body)
			}
			models, routes := store.ListModels(), store.ListRoutes()
			if len(models) != 1 || models[0].Metadata["directory_role"] != "external" || models[0].InputPriceUSDPer1M != 2 || len(routes) != 1 || routes[0].ModelName != "selected-model" || routes[0].ProviderID != provider.ID || routes[0].ProviderModel != "selected-model" {
				t.Fatalf("wrong publication: models=%+v routes=%+v", models, routes)
			}
			payload["input_price_usd_per_1m"] = 123
			response = methodRoutingJSONRequest(t, app.Handler(), http.MethodPost, "/api/admin/models", payload, modelCreateOnlyAdminToken)
			assertJSONError(t, response, http.StatusConflict, "model_conflict")
			if !reflect.DeepEqual(models, store.ListModels()) || !reflect.DeepEqual(routes, store.ListRoutes()) {
				t.Fatal("a stale HTTP retry overwrote prices or changed routes")
			}
		})
	}
}

func TestAdminModelCreateOnlyDoesNotOverwriteUnrelatedRoutes(t *testing.T) {
	store, app, provider := modelCreateOnlyHTTPFixture(t)
	store.AddModel(Model{Name: "original-model", Modality: "chat"})
	existing := store.AddRoute(ModelRoute{ID: "original-route", ModelName: "original-model", ProviderID: provider.ID, ProviderModel: "selected-model", Weight: 37})
	response := methodRoutingJSONRequest(t, app.Handler(), http.MethodPost, "/api/admin/models", map[string]any{
		"create_only": true, "name": "new-model", "modality": "chat",
		"routes": []ModelRoute{{ID: existing.ID, ProviderID: provider.ID, ProviderModel: "selected-model", Weight: 100}},
	}, modelCreateOnlyAdminToken)
	if response.Code != http.StatusCreated {
		t.Fatalf("strict creation failed: %d %s", response.Code, response.Body)
	}
	var saved ModelRoute
	if err := store.db.First(&saved, "id = ?", existing.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, existing) || len(store.ListRoutes()) != 2 {
		t.Fatal("a supplied route ID changed an unrelated route")
	}
}

func TestAdminModelCreateOnlyRetainsValidationAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   string
		status int
	}{
		{"unimported", "provider_model_not_imported", http.StatusConflict},
		{"unknown_cost", "provider_model_price_required", http.StatusBadRequest},
		{"protocol", "route_protocol_mismatch", http.StatusBadRequest},
		{"duplicate_routes", "model_route_conflict", http.StatusConflict},
		{"storage_failure", "model_create_failed", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, app, provider := modelCreateOnlyHTTPFixture(t)
			payload := map[string]any{"create_only": true, "name": "selected-model", "modality": "chat"}
			route := ModelRoute{ProviderID: provider.ID, ProviderModel: "selected-model", Status: StatusActive}
			switch tc.name {
			case "unimported":
				route.ProviderModel = "not-imported"
			case "unknown_cost":
				inventory := store.ListProviderModels()[0]
				if _, err := store.UpdateProviderModel(inventory.ID, ProviderModel{Metadata: map[string]string{"pricing_status": "unverified"}}); err != nil {
					t.Fatal(err)
				}
			case "protocol":
				payload["metadata"] = map[string]string{"endpoints": "unsupported-protocol"}
			case "storage_failure":
				callback := "test:strict-create-route-failure"
				if err := store.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "model_routes" {
						_ = tx.AddError(errors.New("synthetic route failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.db.Callback().Create().Remove(callback) })
			}
			payload["routes"] = []ModelRoute{route}
			if tc.name == "duplicate_routes" {
				payload["routes"] = []ModelRoute{route, route}
			}
			models, routes := store.ListModels(), store.ListRoutes()
			response := methodRoutingJSONRequest(t, app.Handler(), http.MethodPost, "/api/admin/models", payload, modelCreateOnlyAdminToken)
			assertJSONError(t, response, tc.status, tc.code)
			if !reflect.DeepEqual(models, store.ListModels()) || !reflect.DeepEqual(routes, store.ListRoutes()) {
				t.Fatal("a rejected HTTP creation partially persisted")
			}
		})
	}
}

func TestAdminModelCreateOnlyRetainsLegacyUpsert(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		store, app, _ := modelCreateOnlyHTTPFixture(t)
		store.AddModel(withExternalModelRole(Model{Name: "existing-model", InputPriceUSDPer1M: 1}))
		payload := map[string]any{"name": "existing-model", "modality": "chat", "input_price_usd_per_1m": 4}
		if explicit {
			payload["create_only"] = false
		}
		response := methodRoutingJSONRequest(t, app.Handler(), http.MethodPost, "/api/admin/models", payload, modelCreateOnlyAdminToken)
		if response.Code != http.StatusCreated || store.ListModels()[0].InputPriceUSDPer1M != 4 {
			t.Fatalf("legacy creation no longer upserts: %d %s", response.Code, response.Body)
		}
	}
}

func TestAdminModelCreateOnlyRequiresModelWriteAccess(t *testing.T) {
	store, app := newMethodRoutingAdminServer(t, "strict-model-password")
	user, err := store.CreateAdminUser(AdminUser{Username: "strict-model-user", Name: "Strict model user", Email: "strict-model-user@tokenhub.local", Role: "user", Status: StatusActive}, "strict-model-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	models, routes := store.ListModels(), store.ListRoutes()
	response := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/models", map[string]any{"create_only": true, "name": "unauthorized-model"}, session.Token)
	assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
	if !reflect.DeepEqual(models, store.ListModels()) || !reflect.DeepEqual(routes, store.ListRoutes()) {
		t.Fatal("an unauthorized request changed models or routes")
	}
}

func modelCreateOnlyHTTPFixture(t *testing.T) (*GormStore, *Server, Provider) {
	t.Helper()
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "selected-provider", Name: "Selected provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "selected-model", Modality: "chat", Status: StatusActive})
	app := NewWithConfig(store, Config{AdminToken: modelCreateOnlyAdminToken})
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	return store, app, provider
}
