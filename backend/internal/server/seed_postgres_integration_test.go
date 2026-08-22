//go:build integration

package server

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testPostgresV040BootstrapUpgrade(t *testing.T, adminStore *GormStore, config Config) {
	t.Helper()
	schema := fmt.Sprintf("tokenhub_e2e_bootstrap_upgrade_%d", time.Now().UnixNano())
	if err := adminStore.db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create bootstrap upgrade schema: %v", err)
	}
	defer func() {
		if err := adminStore.db.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop bootstrap upgrade schema: %v", err)
		}
	}()

	legacySchema := []string{
		fmt.Sprintf(`CREATE TABLE %s.admin_resources (
			id text,
			kind text,
			name text,
			description text,
			status text,
			fields text,
			created_at timestamptz,
			updated_at timestamptz,
			PRIMARY KEY (id, kind)
		)`, schema),
		fmt.Sprintf(`CREATE TABLE %s.admin_users (
			id text PRIMARY KEY,
			username text,
			name text,
			email text,
			role text,
			team_id text,
			status text,
			password_hash text,
			created_at timestamptz,
			updated_at timestamptz,
			last_login_at timestamptz
		)`, schema),
		fmt.Sprintf(`INSERT INTO %s.admin_resources
			(id, kind, name, status, fields, created_at, updated_at) VALUES
			('team_platform', 'teams', 'Platform Engineering Team', 'disabled', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('team_custom', 'teams', 'Custom Team', 'active', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, schema),
		fmt.Sprintf(`INSERT INTO %s.admin_users
			(id, username, name, email, role, team_id, status, password_hash, created_at, updated_at) VALUES
			('usr_admin', 'admin', 'Platform Admin', 'admin@tokenhub.local', 'admin', 'team_custom', 'active', 'legacy-password-hash', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, schema),
	}
	for _, statement := range legacySchema {
		if err := adminStore.db.Exec(statement).Error; err != nil {
			t.Fatalf("create v0.4 bootstrap fixture: %v", err)
		}
	}

	parsedURL, err := url.Parse(config.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	config.DatabaseURL = parsedURL.String()
	config.ModelCatalogFile = filepath.Join(t.TempDir(), "model-catalog.yaml")
	if err := os.WriteFile(config.ModelCatalogFile, []byte("version: 1\nmodels:\n  - name: postgres-upgrade-test-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	upgradedStore, err := NewStoreWithDialect(config.DatabaseURL, config)
	if err != nil {
		t.Fatalf("upgrade v0.4 PostgreSQL database: %v", err)
	}
	defer closePostgresUpgradeStore(upgradedStore)
	if err := RunStartupBootstrap(t.Context(), upgradedStore, config); err != nil {
		t.Fatalf("bootstrap upgraded v0.4 PostgreSQL database: %v", err)
	}
	assigned, err := upgradedStore.CreateAdminUser(AdminUser{
		ID:       "usr_custom_member",
		Username: "custom-member",
		Name:     "Custom Team Member",
		Email:    "custom-member@tokenhub.local",
		Role:     "user",
		TeamID:   "team_custom",
		Status:   StatusActive,
	}, "postgres-upgrade-test-password")
	if err != nil {
		t.Fatalf("assign user to active custom team after upgrade: %v", err)
	}
	if assigned.TeamID != "team_custom" || !userHasTeam(assigned, "team_custom") {
		t.Fatalf("post-upgrade user assignment = %+v", assigned)
	}

	var existingAdmin AdminUser
	if err := upgradedStore.db.First(&existingAdmin, "id = ?", "usr_admin").Error; err != nil {
		t.Fatal(err)
	}
	if existingAdmin.TeamID != "team_custom" || !userHasTeam(existingAdmin, "team_custom") || userHasTeam(existingAdmin, "team_platform") {
		t.Fatalf("upgrade changed the existing admin assignment: %+v", existingAdmin)
	}
	var defaultTeam AdminResource
	if err := upgradedStore.db.First(&defaultTeam, "kind = ? AND id = ?", "teams", "team_platform").Error; err != nil {
		t.Fatal(err)
	}
	if defaultTeam.Status != StatusDisabled {
		t.Fatalf("upgrade changed the disabled default team: %+v", defaultTeam)
	}
}

func closePostgresUpgradeStore(store *GormStore) {
	for _, database := range []interface{ DB() (*sql.DB, error) }{store.db, store.analyticsDB} {
		sqlDB, err := database.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
}
