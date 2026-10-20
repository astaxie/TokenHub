package server

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newSQLiteIsolationStore(t *testing.T) *GormStore {
	t.Helper()
	store, err := NewSQLiteStoreWithConfig(filepath.Join(t.TempDir(), "isolation.db"), Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteCriticalReadsBypassWriterAndAnalyticsContention(t *testing.T) {
	store := newSQLiteIsolationStore(t)
	setting := store.CreateResource("settings", AdminResource{Name: "Committed setting"})
	user, err := store.CreateAdminUser(AdminUser{Username: "reader", Email: "reader@example.test", Status: StatusActive, Role: "admin"}, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Reader project", Status: StatusActive})
	model := store.AddModel(Model{Name: "reader-model", Status: StatusActive})
	provider := store.AddProvider(Provider{Name: "Reader provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: provider.ID, ProviderModel: model.Name, Status: StatusActive, Priority: 1, Weight: 100})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Reader key", Status: StatusActive, Allowed: []string{model.Name}}, "thk_reader_isolation")
	if err != nil {
		t.Fatal(err)
	}

	writer, _ := store.db.DB()
	transaction, err := writer.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.Exec("UPDATE admin_resources SET name = ? WHERE id = ?", "Uncommitted setting", setting.ID); err != nil {
		t.Fatal(err)
	}
	analytics, _ := store.analyticsDB.DB()
	analyticsConnection, err := analytics.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer analyticsConnection.Close()
	store.mu.Lock()
	defer store.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	view := store.WithContext(ctx)
	if err := view.Ping(ctx); err != nil {
		t.Fatalf("readiness waited for a busy writer or analytics pool: %v", err)
	}
	settings, err := view.ListResourcesContext(ctx, "settings")
	if err != nil || len(settings) != 1 || settings[0].Name != "Committed setting" {
		t.Fatalf("settings did not read the committed WAL snapshot: %+v, %v", settings, err)
	}
	if projects := view.ListProjects(); len(projects) != 1 {
		t.Fatalf("project metadata waited for the writer: %+v", projects)
	}
	if keys := view.ListAPIKeys(); len(keys) != 1 {
		t.Fatalf("API key metadata waited for the writer: %+v", keys)
	}
	if got, ok := view.ValidateAdminSession(session.Token); !ok || got.ID != user.ID {
		t.Fatal("session validation waited for the writer or store mutex")
	}
	if _, _, err := view.ValidateAPIKey(secret, "127.0.0.1"); err != nil {
		t.Fatalf("API key read waited for the writer: %v", err)
	}
	if routes, err := view.SelectRouteCandidates(model.Name); err != nil || len(routes) != 1 {
		t.Fatalf("routing read waited for the writer: routes=%d err=%v", len(routes), err)
	}
	status, err := view.GetDatabaseStatus()
	if err != nil || status["connection_ok"] != true {
		t.Fatalf("database diagnostics failed during contention: %+v, %v", status, err)
	}
	pools := status["pools"].(map[string]any)
	if pools["primary"].(map[string]any)["in_use"] != 1 || pools["analytics"].(map[string]any)["in_use"] != 1 {
		t.Fatalf("pool diagnostics lost the occupied connections: %+v", pools)
	}
	if pools["read"].(map[string]any)["max_open_connections"] != sqliteCriticalReadConnections {
		t.Fatalf("read pool is not bounded: %+v", pools)
	}
	if status["sqlite_files"].(map[string]int64)["wal_bytes"] <= 0 {
		t.Fatalf("missing WAL size diagnostics: %+v", status)
	}
}

func TestSQLitePoolOptionsSurviveConnectionReplacement(t *testing.T) {
	store := newSQLiteIsolationStore(t)
	for _, named := range store.databasePools() {
		t.Run(named.name, func(t *testing.T) {
			pool, err := named.db.DB()
			if err != nil {
				t.Fatal(err)
			}
			// Closing idle connections forces database/sql to replace them.
			pool.SetMaxIdleConns(0)
			connections := make([]*sql.Conn, 0, pool.Stats().MaxOpenConnections)
			defer func() {
				for _, conn := range connections {
					_ = conn.Close()
				}
			}()
			for range pool.Stats().MaxOpenConnections {
				conn, err := pool.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				connections = append(connections, conn)
				wantQueryOnly, wantBusyTimeout := 1, 1000
				if named.name == "primary" {
					wantQueryOnly, wantBusyTimeout = 0, 5000
				}
				for pragma, want := range map[string]int{"foreign_keys": 1, "query_only": wantQueryOnly, "busy_timeout": wantBusyTimeout} {
					var got int
					if err := conn.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
						t.Fatalf("%s = %d, want %d (error %v)", pragma, got, want, err)
					}
				}
				if wantQueryOnly == 1 {
					if _, err := conn.ExecContext(context.Background(), "DELETE FROM audit_events"); err == nil {
						t.Fatal("read-only pool accepted a write")
					}
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.WithContext(ctx).ListResourcesChecked("settings"); !errors.Is(err, context.Canceled) {
		t.Fatalf("read pool lost the store context: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, named := range store.databasePools() {
		pool, _ := named.db.DB()
		if err := pool.Ping(); err == nil {
			t.Fatalf("%s pool stayed open after store.Close", named.name)
		}
	}
}

func TestSQLiteLoginPreservesSessionsDuringConcurrentAuditWrites(t *testing.T) {
	store := newSQLiteIsolationStore(t)
	user, err := store.CreateAdminUser(AdminUser{Username: "login-reader", Email: "login@example.test", Status: StatusActive}, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	var writers sync.WaitGroup
	for range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			for range 25 {
				store.WithContext(ctx).RecordAuditEvent(AuditEvent{Action: "contention-test", AfterSnapshot: strings.Repeat("x", 8*1024)})
			}
		}()
	}
	close(start)
	for range 3 {
		got, session, err := store.WithContext(ctx).AuthenticateAdminUser(user.Username, "test-password", time.Hour)
		if err != nil || got.ID != user.ID {
			t.Fatalf("login failed during bounded concurrent writes: user=%s err=%v", got.ID, err)
		}
		if _, ok := store.WithContext(ctx).ValidateAdminSession(session.Token); !ok {
			t.Fatal("successful login did not persist a usable session")
		}
	}
	writers.Wait()
	var count int64
	if err := store.db.Model(&AuditEvent{}).Where("action = ?", "contention-test").Count(&count).Error; err != nil || count != 100 {
		t.Fatalf("concurrent auditing lost writes: count=%d err=%v", count, err)
	}
}

func TestSQLiteMemoryStoresKeepPrimaryReadSemantics(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{})
	t.Cleanup(func() { _ = store.Close() })
	if store.readDB != nil || store.criticalReadDatabase() != store.db {
		t.Fatal("in-memory store unexpectedly created a WAL read pool")
	}
	for _, dsn := range []string{":memory:", ":memory:?_busy_timeout=1", "file::memory:?cache=shared", "file:named?mode=memory&cache=shared"} {
		if sqliteSupportsWAL(dsn) {
			t.Fatalf("in-memory DSN %q incorrectly enables WAL", dsn)
		}
	}
}

func TestSQLiteSessionReadRejectsExpiredRevokedAndDisabledUsers(t *testing.T) {
	store := newSQLiteIsolationStore(t)
	user, err := store.CreateAdminUser(AdminUser{Username: "session-reader", Email: "session@example.test", Status: StatusActive}, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range []string{"expired", "revoked", "disabled"} {
		t.Run(condition, func(t *testing.T) {
			_, session, err := store.CreateAdminSession(user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			switch condition {
			case "expired":
				err = store.db.Model(&AdminSession{}).Where("token = ?", session.Token).Update("expires_at", time.Now().Add(-time.Minute)).Error
			case "revoked":
				store.RevokeAdminSession(session.Token)
			case "disabled":
				err = store.db.Model(&AdminUser{}).Where("id = ?", user.ID).Update("status", StatusDisabled).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := store.ValidateAdminSession(session.Token); ok {
				t.Fatalf("accepted a %s session", condition)
			}
		})
	}
}
