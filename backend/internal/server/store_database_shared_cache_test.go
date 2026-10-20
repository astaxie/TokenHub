package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteSharedCacheDiskReadersBypassWriterLocks(t *testing.T) {
	for _, replaceConnections := range []bool{false, true} {
		name := "initial connections"
		if replaceConnections {
			name = "replacement connections"
		}
		t.Run(name, func(t *testing.T) {
			store, err := NewSQLiteStoreWithConfig("file:"+filepath.Join(t.TempDir(), "shared.db")+"?cache=shared", Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			user, err := store.CreateAdminUser(AdminUser{Username: "shared-reader", Email: "shared-reader@example.test", Status: StatusActive}, "test-password")
			if err != nil {
				t.Fatal(err)
			}
			_, session, err := store.CreateAdminSession(user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			setting := store.CreateResource("settings", AdminResource{Name: "Committed setting"})
			project := store.CreateProject(Project{Name: "Shared cache project", Status: StatusActive})
			key, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Shared cache key", Status: StatusActive}, "thk_shared_cache_reader")
			if err != nil {
				t.Fatal(err)
			}
			// Seed the timestamp throttle before occupying the writer connection.
			if _, _, err := store.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			record := usageSummaryRecord("shared_usage", project.ID, key.ID, user.ID, 10, time.Now().UTC())
			if err := store.db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			if replaceConnections {
				for _, named := range store.databasePools() {
					if named.name != "primary" {
						pool, err := named.db.DB()
						if err != nil {
							t.Fatal(err)
						}
						// Every following read must reopen its connection with the
						// same isolation guarantees as the original connection.
						pool.SetMaxIdleConns(0)
					}
				}
			}
			pool, err := store.db.DB()
			if err != nil {
				t.Fatal(err)
			}
			transaction, err := pool.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transaction.Rollback() }()
			for _, update := range []struct {
				query string
				args  []any
			}{
				{"UPDATE admin_users SET status = ? WHERE id = ?", []any{StatusDisabled, user.ID}},
				{"UPDATE api_keys SET status = ? WHERE id = ?", []any{StatusRevoked, key.ID}},
				{"UPDATE admin_resources SET name = ? WHERE id = ?", []any{"Uncommitted setting", setting.ID}},
				{"UPDATE usage_records SET total_tokens = ? WHERE id = ?", []any{99, record.ID}},
			} {
				if _, err := transaction.Exec(update.query, update.args...); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			view := store.WithContext(ctx)
			if _, ok := view.ValidateAdminSession(session.Token); !ok {
				t.Error("committed admin session was rejected while the writer held a table lock")
			}
			if _, _, err := view.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
				t.Errorf("committed API key was rejected while the writer held a table lock: %v", err)
			}
			settings, err := view.ListResourcesContext(ctx, "settings")
			if err != nil || len(settings) != 1 || settings[0].Name != "Committed setting" {
				t.Errorf("settings did not read committed data: %+v, error %v", settings, err)
			}
			query := UsageSummaryQuery{UsageRecords: UsageSummaryScope{Global: true}, RequestLogs: UsageSummaryScope{Global: true}}
			summary, err := view.QueryUsageSummary(ctx, query)
			if err != nil || summary.UsageRecordCount != 1 || summary.TotalTokens != record.TotalTokens {
				t.Errorf("analytics did not read committed usage: %+v, error %v", summary, err)
			}
			if err := transaction.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, ok := view.ValidateAdminSession(session.Token); ok {
				t.Error("admin session remained valid after the disable committed")
			}
			if _, _, err := view.ValidateAPIKey(secret, "127.0.0.1"); err == nil {
				t.Error("API key remained valid after the revocation committed")
			}
		})
	}
}
