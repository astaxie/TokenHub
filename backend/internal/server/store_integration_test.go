//go:build integration
// +build integration

package server

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestPostgreSQLIntegration tests PostgreSQL database integration.
func TestPostgreSQLIntegration(t *testing.T) {
	// Skip the test if PostgreSQL is not configured.
	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping PostgreSQL integration test")
	}

	config := Config{
		AdminToken:               "test_admin_token",
		DatabaseURL:              pgURL,
		SecretKey:                "test_secret_key",
		ResourceFailureThreshold: 3,
		ResourceCooldownSeconds:  300,
		DBMaxOpenConns:           10,
		DBMaxIdleConns:           2,
		DBConnMaxLifetimeMinutes: 30,
	}

	store, err := NewStoreWithDialect(pgURL, config)
	if err != nil {
		t.Fatalf("Failed to create PostgreSQL store: %v", err)
	}

	// Verify the database type.
	if !store.IsPostgreSQL() {
		t.Error("Expected PostgreSQL driver, got SQLite")
	}

	// Test basic CRUD operations.
	t.Run("CreateProject", func(t *testing.T) {
		project := Project{
			ID:     NewID("prj"),
			Name:   "Test Project",
			Status: StatusActive,
		}

		created := store.CreateProject(project)
		if created.ID != project.ID {
			t.Errorf("Expected project ID %s, got %s", project.ID, created.ID)
		}

		// Clean up.
		defer store.DeleteProject(created.ID)
	})

	t.Run("CreateAndValidateAPIKey", func(t *testing.T) {
		// Create the project first.
		project := store.CreateProject(Project{
			ID:     NewID("prj"),
			Name:   "API Key Test Project",
			Status: StatusActive,
		})
		defer store.DeleteProject(project.ID)

		// Create an API key.
		key := APIKey{
			Name:   "Test Key",
			Status: StatusActive,
			Group:  "default",
		}

		created, secret, err := store.CreateAPIKey(project.ID, key, "")
		if err != nil {
			t.Fatalf("Failed to create API key: %v", err)
		}

		// Validate the API key.
		validatedProj, validatedKey, err := store.ValidateAPIKey(secret, "127.0.0.1")
		if err != nil {
			t.Fatalf("Failed to validate API key: %v", err)
		}

		if validatedProj.ID != project.ID {
			t.Errorf("Expected project ID %s, got %s", project.ID, validatedProj.ID)
		}

		if validatedKey.ID != created.ID {
			t.Errorf("Expected key ID %s, got %s", created.ID, validatedKey.ID)
		}

		// Clean up.
		defer store.DeleteAPIKey(created.ID)
	})

	t.Run("AdminUserAuthentication", func(t *testing.T) {
		// Create an admin user.
		user := AdminUser{
			Username: "testuser",
			Email:    "test@example.com",
			Name:     "Test User",
			Role:     "admin",
			Status:   StatusActive,
		}

		created, err := store.CreateAdminUser(user, "password123")
		if err != nil {
			t.Fatalf("Failed to create admin user: %v", err)
		}
		defer store.DeleteAdminUser(created.ID)

		// Test authentication.
		authUser, session, err := store.AuthenticateAdminUser("test@example.com", "password123", 24*time.Hour)
		if err != nil {
			t.Fatalf("Failed to authenticate: %v", err)
		}

		if authUser.ID != created.ID {
			t.Errorf("Expected user ID %s, got %s", created.ID, authUser.ID)
		}

		// Validate the session.
		validatedUser, valid := store.ValidateAdminSession(session.Token)
		if !valid {
			t.Error("Session validation failed")
		}

		if validatedUser.ID != created.ID {
			t.Errorf("Expected user ID %s, got %s", created.ID, validatedUser.ID)
		}

		// Clean up.
		store.RevokeAdminSession(session.Token)
	})

	t.Run("BackupNotSupported", func(t *testing.T) {
		// PostgreSQL backup should return an error.
		_, err := store.CreateSQLiteBackup("test", 7)
		if err == nil {
			t.Error("Expected error for PostgreSQL backup, got nil")
		}

		httpErr := AsHTTPError(err)
		if httpErr.Status != 501 {
			t.Errorf("Expected status 501, got %d", httpErr.Status)
		}
	})

	t.Run("ConcurrentWrites", func(t *testing.T) {
		// Verify that PostgreSQL connection pooling handles concurrent writes correctly.
		const numGoroutines = 10
		const projectsPerGoroutine = 5

		type result struct {
			id  string
			err error
		}
		results := make(chan result, numGoroutines*projectsPerGoroutine)

		// Launch concurrent goroutines, each creating multiple projects.
		for i := 0; i < numGoroutines; i++ {
			go func(workerID int) {
				for j := 0; j < projectsPerGoroutine; j++ {
					project := Project{
						ID:     NewID("prj"),
						Name:   "Concurrent Test Project",
						Status: StatusActive,
					}
					created := store.CreateProject(project)
					if created.ID != project.ID {
						results <- result{id: "", err: fmt.Errorf("worker %d: created project ID %q does not match expected %q", workerID, created.ID, project.ID)}
					} else {
						results <- result{id: created.ID, err: nil}
					}
				}
			}(i)
		}

		// Collect all results and verify no errors.
		createdIDs := make([]string, 0, numGoroutines*projectsPerGoroutine)
		for i := 0; i < numGoroutines*projectsPerGoroutine; i++ {
			res := <-results
			if res.err != nil {
				t.Errorf("Concurrent write failed: %v", res.err)
			} else if res.id != "" {
				createdIDs = append(createdIDs, res.id)
			}
		}

		// Verify all projects were created.
		if len(createdIDs) != numGoroutines*projectsPerGoroutine {
			t.Errorf("Expected %d projects, got %d", numGoroutines*projectsPerGoroutine, len(createdIDs))
		}

		// Clean up.
		for _, id := range createdIDs {
			store.DeleteProject(id)
		}
	})
}

// TestSQLiteCompatibility ensures SQLite functionality still works correctly.
func TestSQLiteCompatibility(t *testing.T) {
	store := NewMemoryStore()

	// Verify the database type.
	if !store.IsSQLite() {
		t.Error("Expected SQLite driver for memory store")
	}

	// Test the backup feature.
	t.Run("SQLiteBackup", func(t *testing.T) {
		backup, err := store.CreateSQLiteBackup("test", 7)
		if err != nil {
			t.Fatalf("Failed to create SQLite backup: %v", err)
		}

		if backup.Status != "ready" && backup.Status != "creating" {
			t.Errorf("Unexpected backup status: %s", backup.Status)
		}
	})
}
