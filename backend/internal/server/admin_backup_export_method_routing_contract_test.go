package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type adminBackupExportMethodRoute struct {
	name        string
	path        string
	wrongMethod string
	allow       string
}

func TestAdminBackupExportMethodRoutesPreserveAuthorizationOrder(t *testing.T) {
	store, app := newMethodRoutingBackupExportServer(t)
	ordinaryToken := createAdminBackupExportSession(t, store, "backup-export-ordinary", "user")

	for _, route := range adminBackupExportMethodRoutes() {
		t.Run(route.name+"/no_token", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_admin_token")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/ordinary_user", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, ordinaryToken)
			assertJSONError(t, response, http.StatusForbidden, "admin_forbidden")
			assertAllowHeader(t, response, "")
		})
		t.Run(route.name+"/admin", func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrongMethod, route.path, "dev_admin_token")
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.allow)
		})
	}
}

func TestAdminBackupExportMethodRoutesRejectEveryUnsupportedMethod(t *testing.T) {
	_, app := newMethodRoutingBackupExportServer(t)
	for _, route := range adminBackupExportMethodRoutes() {
		for _, method := range unsupportedAdminBackupExportMethods(route.allow) {
			t.Run(route.name+"/"+method, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, "dev_admin_token")
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.allow)
			})
		}
	}
}

func TestAdminBackupExportMethodRoutesRejectRealHEAD(t *testing.T) {
	store, app := newMethodRoutingBackupExportServer(t)
	ordinaryToken := createAdminBackupExportSession(t, store, "backup-export-head-user", "user")
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range adminBackupExportMethodRoutes() {
		for _, auth := range []struct {
			name       string
			token      string
			wantStatus int
			wantAllow  string
		}{
			{name: "no_token", wantStatus: http.StatusUnauthorized},
			{name: "ordinary_user", token: ordinaryToken, wantStatus: http.StatusForbidden},
			{name: "admin", token: "dev_admin_token", wantStatus: http.StatusMethodNotAllowed, wantAllow: route.allow},
		} {
			t.Run(route.name+"/"+auth.name, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if auth.token != "" {
					request.Header.Set("authorization", "Bearer "+auth.token)
				}
				response, err := httpServer.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				assertRealHEADResponse(t, response, auth.wantStatus, auth.wantAllow, "application/json", true)
				_ = response.Body.Close()
			})
		}
	}
}

func TestAdminBackupExportMethodRoutesRejectTrailingSlashRealHEAD(t *testing.T) {
	_, app := newMethodRoutingBackupExportServer(t)
	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)

	for _, route := range []adminBackupExportMethodRoute{
		{name: "backup_item", path: "/api/admin/sqlite/backups/backup-missing/", allow: "GET, DELETE"},
		{name: "backup_download", path: "/api/admin/sqlite/backups/backup-missing/download/", allow: http.MethodGet},
		{name: "backup_restore", path: "/api/admin/sqlite/backups/backup-missing/restore/", allow: http.MethodPost},
		{name: "export", path: "/api/admin/export/usage/", allow: http.MethodGet},
	} {
		t.Run(route.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodHead, httpServer.URL+route.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("authorization", "Bearer dev_admin_token")
			response, err := httpServer.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			assertRealHEADResponse(t, response, http.StatusMethodNotAllowed, route.allow, "application/json", true)
			_ = response.Body.Close()
		})
	}
}

func TestAdminBackupExportMethodRoutesPreserveCORSPreflight(t *testing.T) {
	_, app := newMethodRoutingBackupExportServer(t)
	for _, route := range adminBackupExportMethodRoutes() {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.wrongMethod)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
				t.Fatalf("access-control-allow-methods = %q", got)
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestAdminBackupExportMethodRoutesPreserveSuccessHeadersAndAudits(t *testing.T) {
	store, app := newMethodRoutingBackupExportServer(t)
	auditsBefore := len(store.ListAuditEvents())

	created := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/sqlite/backups", map[string]any{"expire_days": 7}, "dev_admin_token")
	if created.Code != http.StatusCreated {
		t.Fatalf("create backup: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var backup SQLiteBackupRecord
	if err := json.NewDecoder(created.Body).Decode(&backup); err != nil {
		t.Fatal(err)
	}
	if backup.ID == "" || backup.Status != "ready" {
		t.Fatalf("created backup = %+v", backup)
	}

	listed := methodRoutingRequest(app, http.MethodGet, "/api/admin/sqlite/backups", "dev_admin_token")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), backup.ID) {
		t.Fatalf("list backups: status=%d body=%s", listed.Code, listed.Body.String())
	}
	item := methodRoutingRequest(app, http.MethodGet, "/api/admin/sqlite/backups/"+backup.ID, "dev_admin_token")
	if item.Code != http.StatusOK || !strings.Contains(item.Body.String(), backup.ID) {
		t.Fatalf("get backup: status=%d body=%s", item.Code, item.Body.String())
	}

	download := methodRoutingRequest(app, http.MethodGet, "/api/admin/sqlite/backups/"+backup.ID+"/download", "dev_admin_token")
	if download.Code != http.StatusOK || !strings.Contains(download.Body.String(), "SQLite format") {
		t.Fatalf("download backup: status=%d body=%q", download.Code, download.Body.String())
	}
	if got := download.Header().Get("content-type"); got != "application/vnd.sqlite3" {
		t.Fatalf("download content-type = %q", got)
	}
	if got := download.Header().Get("content-disposition"); got != `attachment; filename="`+backup.FileName+`"` {
		t.Fatalf("download content-disposition = %q", got)
	}

	trailingDownload := methodRoutingRequest(app, http.MethodGet, "/api/admin/sqlite/backups/"+backup.ID+"/download/", "dev_admin_token")
	if trailingDownload.Code != http.StatusOK || trailingDownload.Header().Get("content-type") != "application/vnd.sqlite3" {
		t.Fatalf("trailing download: status=%d content-type=%q", trailingDownload.Code, trailingDownload.Header().Get("content-type"))
	}

	exported := methodRoutingRequest(app, http.MethodGet, "/api/admin/export/usage?period=2026-08", "dev_admin_token")
	if exported.Code != http.StatusOK || !strings.HasPrefix(exported.Body.String(), "dimension,id,") {
		t.Fatalf("export usage: status=%d body=%s", exported.Code, exported.Body.String())
	}
	if got := exported.Header().Get("content-type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("export content-type = %q", got)
	}
	if got := exported.Header().Get("content-disposition"); got != `attachment; filename="tokenhub-usage.csv"` {
		t.Fatalf("export content-disposition = %q", got)
	}
	trailingExport := methodRoutingRequest(app, http.MethodGet, "/api/admin/export/usage/", "dev_admin_token")
	if trailingExport.Code != http.StatusOK || !strings.HasPrefix(trailingExport.Body.String(), "dimension,id,") {
		t.Fatalf("trailing export: status=%d body=%s", trailingExport.Code, trailingExport.Body.String())
	}

	deleted := methodRoutingRequest(app, http.MethodDelete, "/api/admin/sqlite/backups/"+backup.ID, "dev_admin_token")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete backup: status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if got := len(store.ListAuditEvents()); got != auditsBefore+6 {
		t.Fatalf("backup/export operations wrote %d audits, want %d", got-auditsBefore, 6)
	}
}

func TestAdminBackupExportMethodRoutesPreserveRestoreAndPathBoundaries(t *testing.T) {
	store, app := newMethodRoutingBackupExportServer(t)
	created := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/sqlite/backups", map[string]any{}, "dev_admin_token")
	if created.Code != http.StatusCreated {
		t.Fatalf("create backup: status=%d body=%s", created.Code, created.Body.String())
	}
	var backup SQLiteBackupRecord
	if err := json.NewDecoder(created.Body).Decode(&backup); err != nil {
		t.Fatal(err)
	}

	invalidRestore := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/sqlite/backups/"+backup.ID+"/restore", map[string]any{"confirmation": "RESTORE wrong"}, "dev_admin_token")
	assertJSONError(t, invalidRestore, http.StatusBadRequest, "invalid_restore_confirmation")
	restored := methodRoutingJSONRequest(t, app, http.MethodPost, "/api/admin/sqlite/backups/"+backup.ID+"/restore", map[string]any{"confirmation": "RESTORE " + backup.ID}, "dev_admin_token")
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), `"status":"restored"`) {
		t.Fatalf("restore backup: status=%d body=%s", restored.Code, restored.Body.String())
	}

	ordinaryToken := createAdminBackupExportSession(t, store, "backup-export-usage", "user")
	usage := methodRoutingRequest(app, http.MethodGet, "/api/admin/export/usage", ordinaryToken)
	if usage.Code != http.StatusOK || !strings.HasPrefix(usage.Body.String(), "dimension,id,") {
		t.Fatalf("ordinary usage export: status=%d body=%s", usage.Code, usage.Body.String())
	}
	forbiddenExport := methodRoutingRequest(app, http.MethodGet, "/api/admin/export/invoices", ordinaryToken)
	assertJSONError(t, forbiddenExport, http.StatusForbidden, "export_forbidden")
	assertAllowHeader(t, forbiddenExport, "")
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/admin/sqlite/backups/"},
		{method: http.MethodGet, path: "/api/admin/sqlite/backups/" + backup.ID + "/unknown"},
		{method: http.MethodGet, path: "/api/admin/sqlite/backups/" + backup.ID + "/extra/path"},
		{method: http.MethodGet, path: "/api/admin/sqlite/backups/backup%2Fid"},
		{method: http.MethodPost, path: "/api/admin/sqlite/backups/backup%2Fid"},
		{method: http.MethodPost, path: "/api/admin/sqlite/backups/backup%2Fid/download"},
		{method: http.MethodGet, path: "/api/admin/export/"},
		{method: http.MethodGet, path: "/api/admin/export/usage/extra"},
		{method: http.MethodGet, path: "/api/admin/export/usage%2Fextra"},
	} {
		response := methodRoutingRequest(app, test.method, test.path, "dev_admin_token")
		assertJSONError(t, response, http.StatusNotFound, "not_found")
		assertAllowHeader(t, response, "")
	}
}

func adminBackupExportMethodRoutes() []adminBackupExportMethodRoute {
	return []adminBackupExportMethodRoute{
		{name: "backup_collection", path: "/api/admin/sqlite/backups", wrongMethod: http.MethodPut, allow: "GET, POST"},
		{name: "backup_item", path: "/api/admin/sqlite/backups/backup-missing", wrongMethod: http.MethodPost, allow: "GET, DELETE"},
		{name: "backup_download", path: "/api/admin/sqlite/backups/backup-missing/download", wrongMethod: http.MethodPost, allow: http.MethodGet},
		{name: "backup_restore", path: "/api/admin/sqlite/backups/backup-missing/restore", wrongMethod: http.MethodGet, allow: http.MethodPost},
		{name: "export", path: "/api/admin/export/usage", wrongMethod: http.MethodPost, allow: http.MethodGet},
	}
}

func unsupportedAdminBackupExportMethods(allow string) []string {
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodTrace, http.MethodConnect}
	unsupported := make([]string, 0, len(methods))
	for _, method := range methods {
		if !strings.Contains(","+strings.ReplaceAll(allow, " ", "")+",", ","+method+",") {
			unsupported = append(unsupported, method)
		}
	}
	return unsupported
}

func newMethodRoutingBackupExportServer(t *testing.T) (*GormStore, http.Handler) {
	t.Helper()
	tmp := t.TempDir()
	config := ConfigFromEnv()
	config.AdminToken = "dev_admin_token"
	config.BootstrapAdminPassword = "backup-export-routing-password"
	config.DatabaseURL = "sqlite:" + filepath.Join(tmp, "tokenhub.db")
	config.SQLiteBackupDir = filepath.Join(tmp, "backups")
	config.SecretKey = "backup-export-routing-secret"
	store, err := NewSQLiteStoreWithConfig(config.DatabaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	return store, NewWithConfig(store, config).Handler()
}

func createAdminBackupExportSession(t *testing.T, store *GormStore, username string, role string) string {
	t.Helper()
	user, err := store.CreateAdminUser(AdminUser{
		Username: username,
		Name:     username,
		Email:    username + "@tokenhub.local",
		Role:     role,
		Status:   StatusActive,
	}, "backup-export-routing-user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}
