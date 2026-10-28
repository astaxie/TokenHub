package server

import (
	"path/filepath"
	"testing"
)

func TestSQLiteSubscriptionOAuthRegistrationMigration(t *testing.T) {
	testSubscriptionOAuthRegistrationMigration(t, "sqlite://"+filepath.Join(t.TempDir(), "subscription-oauth-upgrade.db"))
}

func testSubscriptionOAuthRegistrationMigration(t *testing.T, databaseURL string) {
	t.Helper()
	store, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := VerifySchemaSemantics(t.Context(), databaseURL); err != nil {
		t.Fatalf("fresh subscription OAuth schema: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO audit_events (id, action, created_at) VALUES ('existing-audit', 'test.read', CURRENT_TIMESTAMP)`,
		`DROP TABLE subscription_oauth_registrations`,
		`DROP TABLE subscription_oauth_hosts`,
		`DELETE FROM schema_migrations WHERE version = 10`,
	} {
		if err := store.db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatalf("upgrade version 9 database: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	if err := VerifySchemaSemantics(t.Context(), databaseURL); err != nil {
		t.Fatalf("upgraded subscription OAuth schema: %v", err)
	}
	if events := upgraded.ListAuditEvents(); len(events) != 1 || events[0].ID != "existing-audit" {
		t.Fatalf("existing audit event did not survive upgrade: %+v", events)
	}
	host := subscriptionOAuthHostRecord{ID: "test-provider", HostIDEncrypted: "encrypted-host-fixture"}
	registration := subscriptionOAuthRegistrationRecord{
		ID: "test-registration", ActorID: "test-admin", HostID: host.ID, SecretEncrypted: "encrypted-registration-fixture",
	}
	if err := upgraded.db.Create(&host).Error; err != nil {
		t.Fatalf("write OAuth host: %v", err)
	}
	if err := upgraded.db.Create(&registration).Error; err != nil {
		t.Fatalf("write OAuth registration: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO subscription_oauth_hosts (id) VALUES ('missing-host-secret')`,
		`INSERT INTO subscription_oauth_registrations (id, host_id, secret_encrypted) VALUES ('missing-actor', 'test-provider', 'fixture')`,
		`INSERT INTO subscription_oauth_registrations (id, actor_id, secret_encrypted) VALUES ('missing-host', 'test-admin', 'fixture')`,
		`INSERT INTO subscription_oauth_registrations (id, actor_id, host_id) VALUES ('missing-secret', 'test-admin', 'test-provider')`,
	} {
		if err := upgraded.db.Exec(statement).Error; err == nil {
			t.Fatalf("schema accepted an incomplete OAuth record: %s", statement)
		}
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewStoreWithDialect(databaseURL, Config{})
	if err != nil {
		t.Fatalf("restart upgraded database: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	var persistedHost subscriptionOAuthHostRecord
	if err := restarted.db.First(&persistedHost, "id = ?", host.ID).Error; err != nil || persistedHost != host {
		t.Fatalf("OAuth host did not survive restart: record = %+v, error = %v", persistedHost, err)
	}
	var persistedRegistration subscriptionOAuthRegistrationRecord
	if err := restarted.db.First(&persistedRegistration, "id = ?", registration.ID).Error; err != nil || persistedRegistration != registration {
		t.Fatalf("OAuth registration did not survive restart: record = %+v, error = %v", persistedRegistration, err)
	}
	var migrationCount int64
	if err := restarted.db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = 10 AND dirty = 0").Scan(&migrationCount).Error; err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("clean subscription OAuth migration rows = %d, want 1", migrationCount)
	}
	for _, table := range []string{"subscription_oauth_hosts", "subscription_oauth_registrations"} {
		if err := restarted.db.Exec("DROP TABLE " + table).Error; err != nil {
			t.Fatal(err)
		}
		if err := VerifySchemaSemantics(t.Context(), databaseURL); err == nil {
			t.Fatalf("schema verification accepted missing %s", table)
		}
		for _, statement := range subscriptionOAuthRegistrationMigration().Statements {
			if err := restarted.db.Exec(statement).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
}
