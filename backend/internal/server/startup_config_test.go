package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareForStartupGeneratesPersistentSecretForNewSQLite(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tokenhub.db")
	config := Config{
		Environment:            "prod",
		AdminToken:             "dev_admin_token",
		BootstrapAdminPassword: "admin123456",
		SecretKey:              "change-me-tokenhub-secret-key",
		DatabaseURL:            "sqlite://" + databasePath,
	}

	prepared, err := config.PrepareForStartup()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.AdminToken != "" || prepared.BootstrapAdminPassword != "" {
		t.Fatalf("known optional placeholders were not disabled: %+v", prepared)
	}
	if len(prepared.SecretKey) != 64 {
		t.Fatalf("expected a 32-byte hex secret, got %d characters", len(prepared.SecretKey))
	}
	if err := prepared.ValidateForStartup(); err != nil {
		t.Fatalf("prepared production config should be valid: %v", err)
	}

	keyPath := databasePath + sqliteSecretKeySuffix
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("secret key permissions = %o, want 600", permissions)
	}
	restarted, err := config.PrepareForStartup()
	if err != nil {
		t.Fatal(err)
	}
	if restarted.SecretKey != prepared.SecretKey {
		t.Fatal("restart did not reuse the persisted SQLite secret key")
	}
}

func TestPrepareForStartupGeneratesPersistentSecretForSQLiteFileURI(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tokenhub.db")
	config := Config{
		Environment: "prod",
		SecretKey:   "change-me-tokenhub-secret-key",
		DatabaseURL: "file:" + filepath.ToSlash(databasePath) + "?cache=shared&mode=rwc",
	}

	prepared, err := config.PrepareForStartup()
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.SecretKey) != 64 {
		t.Fatalf("expected a 32-byte hex secret, got %d characters", len(prepared.SecretKey))
	}
	if _, err := os.Stat(databasePath + sqliteSecretKeySuffix); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteDatabaseFilePathClassifiesFileURIs(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		wantPath string
		wantFile bool
	}{
		{name: "absolute file", dsn: "file:/app/data/tokenhub.db?cache=shared", wantPath: "/app/data/tokenhub.db", wantFile: true},
		{name: "relative file", dsn: "file:tokenhub.db?mode=rwc", wantPath: "tokenhub.db", wantFile: true},
		{name: "memory name", dsn: "file:memdb1?mode=memory&cache=shared"},
		{name: "memory sentinel", dsn: "file::memory:?cache=shared"},
		{name: "plain memory", dsn: ":memory:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, fileBacked, err := sqliteDatabaseFilePath(test.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if path != test.wantPath || fileBacked != test.wantFile {
				t.Fatalf("sqliteDatabaseFilePath(%q) = (%q, %v), want (%q, %v)", test.dsn, path, fileBacked, test.wantPath, test.wantFile)
			}
		})
	}
}

func TestPrepareForStartupRefusesNewKeyForExistingSQLite(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tokenhub.db")
	if err := os.WriteFile(databasePath, []byte("existing database"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Environment: "prod",
		SecretKey:   "dev_tokenhub_secret_key",
		DatabaseURL: "sqlite://" + databasePath,
	}

	_, err := config.PrepareForStartup()
	if err == nil || !strings.Contains(err.Error(), "existing SQLite database") {
		t.Fatalf("expected existing database protection, got %v", err)
	}
	if _, statErr := os.Stat(databasePath + sqliteSecretKeySuffix); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe key file was created: %v", statErr)
	}
}

func TestPrepareForStartupKeepsPostgresSecretExplicit(t *testing.T) {
	config := Config{
		Environment:            "prod",
		AdminToken:             "change-me-tokenhub-admin-token",
		BootstrapAdminPassword: "change-me-tokenhub-admin-password",
		SecretKey:              "change-me-tokenhub-secret-key",
		DatabaseURL:            "postgres://tokenhub@example.test/tokenhub",
	}

	prepared, err := config.PrepareForStartup()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.AdminToken != "" || prepared.BootstrapAdminPassword != "" {
		t.Fatalf("optional placeholders were not disabled: %+v", prepared)
	}
	validationErr := prepared.ValidateForStartup()
	if validationErr == nil || !strings.Contains(validationErr.Error(), "TOKENHUB_SECRET_KEY") {
		t.Fatalf("expected PostgreSQL secret-key requirement, got %v", validationErr)
	}
	if strings.Contains(validationErr.Error(), "TOKENHUB_ADMIN_TOKEN") || strings.Contains(validationErr.Error(), "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD") {
		t.Fatalf("disabled optional credentials were still rejected: %v", validationErr)
	}
}
