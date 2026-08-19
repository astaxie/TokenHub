package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStartupBootstrapReloadsCatalogOnEveryStart(t *testing.T) {
	store := NewMemoryStore()
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	config := Config{
		BootstrapAdminPassword: "startup-bootstrap-test-password",
		ModelCatalogFile:       catalogPath,
	}
	writeCatalog := func(category string) {
		t.Helper()
		content := []byte("version: 1\nmodels:\n  - name: startup-reloaded-model\n    category: " + category + "\n")
		if err := os.WriteFile(catalogPath, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	modelCategory := func() string {
		t.Helper()
		for _, model := range store.ListModels() {
			if model.Name == "startup-reloaded-model" {
				return model.Category
			}
		}
		t.Fatal("startup catalog model was not loaded")
		return ""
	}

	writeCatalog("first-start")
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	if got := modelCategory(); got != "first-start" {
		t.Fatalf("unexpected first-start category %q", got)
	}

	writeCatalog("second-start")
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	if got := modelCategory(); got != "second-start" {
		t.Fatalf("catalog edit was not applied on restart: category=%q", got)
	}
}

func TestSeedDefaultModelCatalogReportsLegacyNameConflict(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{
		ID:       "legacy-catalog-id",
		Name:     "catalog-conflict-model",
		Modality: "chat",
		Status:   StatusActive,
	})
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte("version: 1\nmodels:\n  - name: catalog-conflict-model\n    modality: chat\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := seedDefaultModelCatalog(store, catalogPath)
	if err == nil {
		t.Fatal("expected catalog seed conflict to be reported")
	}
	if !strings.Contains(err.Error(), `seed catalog model "catalog-conflict-model"`) {
		t.Fatalf("expected model name in seed error, got %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "duplicated key") {
		t.Fatalf("expected database conflict cause in seed error, got %v", err)
	}
}

func TestRunStartupBootstrapUpgradesV040DatabaseWithDisabledDefaultTeam(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "v0.4.0.db")
	legacyDB, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`CREATE TABLE admin_resources (
		id text,
		kind text,
		name text,
		description text,
		status text,
		fields text,
		created_at datetime,
		updated_at datetime,
		PRIMARY KEY (id, kind)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`CREATE TABLE admin_users (
		id text primary key,
		username text,
		name text,
		email text,
		role text,
		team_id text,
		status text,
		password_hash text,
		created_at datetime,
		updated_at datetime,
		last_login_at datetime
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_resources
		(id, kind, name, status, fields, created_at, updated_at) VALUES
		('team_platform', 'teams', 'Platform Engineering Team', 'disabled', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		('team_custom', 'teams', 'Custom Team', 'active', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacyDB.Exec(`INSERT INTO admin_users
		(id, username, name, email, role, team_id, status, password_hash, created_at, updated_at) VALUES
		('usr_admin', 'admin', 'Platform Admin', 'admin@tokenhub.local', 'admin', 'team_custom', 'active', 'legacy-password-hash', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatal(err)
	}

	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	if err := os.WriteFile(catalogPath, []byte("version: 1\nmodels:\n  - name: startup-test-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		BootstrapAdminPassword: "startup-bootstrap-test-password",
		ModelCatalogFile:       catalogPath,
	}
	store, err := NewSQLiteStoreWithConfig("sqlite://"+databasePath, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatalf("v0.4.0 database upgrade should preserve the existing admin assignment: %v", err)
	}
	users := store.ListAdminUsers()
	if len(users) != 1 || users[0].ID != "usr_admin" || users[0].TeamID != "team_custom" || userHasTeam(users[0], "team_platform") {
		t.Fatalf("startup changed the existing admin assignment: %+v", users)
	}
	for _, team := range store.ListResources("teams") {
		if team.ID == "team_platform" && team.Status != StatusDisabled {
			t.Fatalf("startup re-enabled the disabled default team: %+v", team)
		}
	}
}

func TestRunStartupBootstrapPreservesExistingModelPrices(t *testing.T) {
	store := NewMemoryStore()
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	config := Config{
		BootstrapAdminPassword: "startup-price-test-password",
		ModelCatalogFile:       catalogPath,
	}
	writeCatalog := func(content string) {
		t.Helper()
		if err := os.WriteFile(catalogPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	modelsByName := func() map[string]Model {
		t.Helper()
		models := map[string]Model{}
		for _, model := range store.ListModels() {
			models[model.Name] = model
		}
		return models
	}

	writeCatalog(`
version: 1
models:
  - name: startup-priced-model
    category: first
    family: startup
    modality: chat
    context_window: 1000
    input_price_usd_per_1m: 1
    cache_read_price_usd_per_1m: 0.1
    output_price_usd_per_1m: 2
    embedding_price_usd_per_1m: 0.2
`)
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateModel("startup-priced-model", Model{
		InputPriceUSDPer1M:     7,
		CacheReadPriceUSDPer1M: 0,
		OutputPriceUSDPer1M:    9,
		EmbeddingPriceUSDPer1M: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	writeCatalog(`
version: 1
models:
  - name: startup-priced-model
    category: second
    family: startup
    modality: chat
    context_window: 2000
    input_price_usd_per_1m: 10
    cache_read_price_usd_per_1m: 1
    output_price_usd_per_1m: 20
    embedding_price_usd_per_1m: 2
  - name: startup-new-model
    category: second
    family: startup
    modality: chat
    context_window: 3000
    input_price_usd_per_1m: 11
    cache_read_price_usd_per_1m: 1.1
    output_price_usd_per_1m: 22
    embedding_price_usd_per_1m: 2.2
`)
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}

	models := modelsByName()
	priced := models["startup-priced-model"]
	if priced.InputPriceUSDPer1M != 7 || priced.CacheReadPriceUSDPer1M != 0 ||
		priced.OutputPriceUSDPer1M != 9 || priced.EmbeddingPriceUSDPer1M != 0.9 {
		t.Fatalf("startup replaced custom prices: %+v", priced)
	}
	if priced.Category != "second" || priced.ContextWindow != 2000 {
		t.Fatalf("startup did not refresh non-price catalog fields: %+v", priced)
	}
	newModel := models["startup-new-model"]
	if newModel.InputPriceUSDPer1M != 11 || newModel.CacheReadPriceUSDPer1M != 1.1 ||
		newModel.OutputPriceUSDPer1M != 22 || newModel.EmbeddingPriceUSDPer1M != 2.2 {
		t.Fatalf("new catalog model did not use catalog prices: %+v", newModel)
	}
}

func TestBootstrapBaseDataPreservesRoutesWhenRefreshingLegacyCatalogModel(t *testing.T) {
	store := NewMemoryStore()
	const modelName = "deepseek-v4-flash"
	store.AddModel(Model{
		Name:     modelName,
		Modality: "chat",
		Status:   StatusActive,
		Metadata: map[string]string{"source": "public-provider-conf"},
	})
	provider := store.AddProvider(Provider{
		ID:      "prv_catalog_route",
		Name:    "Catalog Route Provider",
		Type:    ProviderOpenAICompatible,
		Status:  StatusActive,
		Healthy: true,
	})
	route := store.AddRoute(ModelRoute{
		ID:            "route_catalog_model",
		ModelName:     modelName,
		ProviderID:    provider.ID,
		ProviderModel: modelName,
		Status:        StatusActive,
	})
	config := Config{
		BootstrapAdminPassword: "catalog-route-bootstrap-password",
		ModelCatalogFile:       "../../../data/model-catalog.yaml",
	}

	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}

	model, ok := modelByNameForTest(store.ListModels(), modelName)
	if !ok || model.Metadata["source"] != "tokenhub-standard-catalog" {
		t.Fatalf("expected standard catalog model after refresh, got %+v", model)
	}
	for _, existing := range store.ListRoutes() {
		if existing.ID == route.ID && existing.ModelName == modelName {
			return
		}
	}
	t.Fatalf("expected catalog refresh to preserve route %+v", route)
}
