package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCatalogCalendarDatesPreserveUnspecifiedShutdownTime(t *testing.T) {
	metadata := map[string]string{"lifecycle_status": "deprecated", "shutdown_date": "2026-11-18"}
	for _, test := range []struct {
		at      string
		retired bool
	}{
		{"2026-11-18T00:00:00Z", false},
		{"2026-11-19T11:59:59Z", false},
		{"2026-11-19T12:00:00Z", true},
	} {
		at, err := time.Parse(time.RFC3339, test.at)
		if err != nil {
			t.Fatal(err)
		}
		if catalogModelRetired(metadata, at) != test.retired {
			t.Fatalf("wrong date-only retirement at %s", test.at)
		}
	}
	metadata["shutdown_date"] = "2026-02-30"
	if catalogModelRetired(metadata, time.Now()) {
		t.Fatal("invalid calendar date became a shutdown")
	}
	metadata["shutdown_date"], metadata["shutdown_at"] = "2026-11-18", "2026-11-18T10:00:00+08:00"
	at, _ := time.Parse(time.RFC3339, "2026-11-18T02:00:00Z")
	if !catalogModelRetired(metadata, at) {
		t.Fatal("precise vendor timestamp did not take precedence")
	}
}

func TestCalendarDatePolicyBlocksOnlyNewRoutesAndClearsWithdrawnDates(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "calendar", Type: ProviderOpenAICompatible, Status: StatusActive, Options: map[string]string{"catalog_id": "official"}})
	store.AddModel(Model{Name: "calendar-chat", Modality: "chat", Status: StatusActive})
	stored := store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "model", Modality: "chat", InputPriceUSDPer1M: 2, Status: StatusActive})
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	policy := map[string]string{"lifecycle_status": "deprecated", "shutdown_date": "2020-01-01"}
	if err := store.SaveProviderCatalogSnapshot([]ProviderCatalogEntry{{ID: "official", Models: []ProviderCatalogModel{{ID: "model", Metadata: policy}}}}, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	route := ModelRoute{ModelName: "calendar-chat", ProviderID: provider.ID, ProviderModel: "model", Weight: 100, Status: StatusActive}
	response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, `"code":"provider_model_retired"`) {
		t.Fatalf("past calendar date allowed new route: %d %s", response.Code, response.Body)
	}
	route.ID = "existing-calendar"
	store.AddRoute(route)
	route.Priority = 2
	if err := app.validateRetrievalRoute(route, nil, provider); err != nil {
		t.Fatalf("existing route edit blocked: %v", err)
	}
	stored.Metadata = map[string]string{"shutdown_date": "2020-01-01", "redirect_date": "2020-01-02", "lifecycle_status": "deprecated"}
	effective := (catalogAdvisoryIndex{"official": {"model": {"lifecycle_status": "active"}}}).apply(provider, stored)
	if effective.Metadata["shutdown_date"] != "" || effective.Metadata["redirect_date"] != "" || stored.Metadata["shutdown_date"] == "" || effective.InputPriceUSDPer1M != 2 {
		t.Fatal("date overlay retained stale dates or changed saved inventory")
	}
}
