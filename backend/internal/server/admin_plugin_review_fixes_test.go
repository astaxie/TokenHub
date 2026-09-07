package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginLifecycleRejectsUnsatisfiedDependencies(t *testing.T) {
	t.Run("enable", func(t *testing.T) {
		pluginDir := t.TempDir()
		consumerDir := filepath.Join(pluginDir, "consumer")
		writeServerPluginManifest(t, consumerDir, serverDependencyManifest("tokenhub.consumer", "1.0.0", "tokenhub.missing", "^1.0.0"))
		if err := os.WriteFile(filepath.Join(consumerDir, "plugin.state.json"), []byte(`{"status":"disabled"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

		response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.consumer/state", map[string]any{"status": "enabled"}, "dev_admin_token")
		assertResponseBodyJSONError(t, response, http.StatusConflict, "plugin_dependency_unsatisfied")
		consumer, ok := server.pluginRegistry.Describe("tokenhub.consumer")
		if !ok || consumer.Status != pluginmeta.StatusDisabled {
			t.Fatalf("consumer descriptor = %+v, %t; want disabled", consumer, ok)
		}
	})

	t.Run("disable and uninstall", func(t *testing.T) {
		pluginDir := t.TempDir()
		writeServerPluginManifest(t, filepath.Join(pluginDir, "core"), serverDependencyManifest("tokenhub.core", "1.4.0", "", ""))
		writeServerPluginManifest(t, filepath.Join(pluginDir, "consumer"), serverDependencyManifest("tokenhub.consumer", "1.0.0", "tokenhub.core", "^1.0.0"))
		server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

		disable := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.core/state", map[string]any{"status": "disabled"}, "dev_admin_token")
		assertResponseBodyJSONError(t, disable, http.StatusConflict, "plugin_dependency_in_use")
		uninstall := doJSON(t, server.Handler(), http.MethodDelete, "/api/admin/plugin-packages/tokenhub.core", nil, "dev_admin_token")
		assertResponseBodyJSONError(t, uninstall, http.StatusConflict, "plugin_dependency_in_use")
		if _, err := os.Stat(filepath.Join(pluginDir, "core", "plugin.yaml")); err != nil {
			t.Fatalf("required dependency was removed: %v", err)
		}
	})
}

func TestAdminPluginUpdateRejectsMismatchedPluginID(t *testing.T) {
	pluginDir := t.TempDir()
	currentDir := filepath.Join(pluginDir, "current")
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.other", "Other Plugin", "2.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, currentDir, adminPluginManifestWithTestDistribution("tokenhub.current", "Current Plugin", "1.0.0", upstream.URL+"/other.zip", adminSHA256Hex(archive), ""))
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.current/update", map[string]any{}, "dev_admin_token")
	assertResponseBodyJSONError(t, response, http.StatusBadRequest, "plugin_id_mismatch")
	if _, err := os.Stat(filepath.Join(currentDir, "plugin.yaml")); err != nil {
		t.Fatalf("target package changed after ID mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "tokenhub.other")); !os.IsNotExist(err) {
		t.Fatalf("mismatched package was installed: %v", err)
	}
}

func TestAdminPluginUpdatePublishesNewActions(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginActionManifest("tokenhub.hot-reload", "1.1.0", "sync.new", "", ""),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "hot-reload"), adminPluginActionManifest("tokenhub.hot-reload", "1.0.0", "sync.old", upstream.URL+"/hot-reload.zip", adminSHA256Hex(archive)))
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	before := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-actions", nil, "dev_admin_token")
	if !strings.Contains(before.Body, `"action_id":"sync.old"`) {
		t.Fatalf("old action missing before update: %s", before.Body)
	}
	update := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.hot-reload/update", map[string]any{}, "dev_admin_token")
	if update.Code != http.StatusOK {
		t.Fatalf("update plugin: expected 200, got %d: %s", update.Code, update.Body)
	}
	after := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-actions", nil, "dev_admin_token")
	if !strings.Contains(after.Body, `"action_id":"sync.new"`) || strings.Contains(after.Body, `"action_id":"sync.old"`) {
		t.Fatalf("runtime actions were not replaced after update: %s", after.Body)
	}
}

func TestAdminPluginUpdateRecoversQuarantinedPackage(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.recovery", "Recovered Plugin", "2.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	currentDir := filepath.Join(pluginDir, "recovery")
	writeServerPluginManifest(t, currentDir, adminPluginCommandManifest(
		"tokenhub.recovery",
		"Quarantined Plugin",
		"1.0.0",
		upstream.URL+"/recovery.zip",
		adminSHA256Hex(archive),
	))
	if err := os.WriteFile(filepath.Join(currentDir, "run.sh"), []byte("#!/bin/sh\nprintf '{}'"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	if _, ok := server.pluginRegistry.Describe("tokenhub.recovery"); ok {
		t.Fatal("quarantined package was registered before recovery")
	}
	available := pluginmeta.Descriptor{
		ID:      "tokenhub.recovery",
		Name:    "Recovered Plugin",
		Version: "2.0.0",
		Distribution: &pluginmeta.Distribution{
			DownloadURL:    upstream.URL + "/recovery.zip",
			ChecksumSHA256: adminSHA256Hex(archive),
		},
	}
	marketplace, err := server.annotatePluginMarketplace([]pluginmeta.Descriptor{available})
	if err != nil {
		t.Fatalf("annotate marketplace: %v", err)
	}
	if len(marketplace) != 1 || !marketplace[0].Installed || !marketplace[0].UpdateAvailable || marketplace[0].InstalledVersion != "1.0.0" {
		t.Fatalf("quarantined marketplace annotation = %+v", marketplace)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.recovery/update", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("update quarantined plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode recovered plugin response: %v", err)
	}
	plugin := body.Data.Plugin
	if plugin.Version != "2.0.0" || plugin.Status != pluginmeta.StatusEnabled || !plugin.Loadable ||
		!plugin.Lifecycle.ActiveEnabled || plugin.Lifecycle.DesiredVersion != "2.0.0" || plugin.Lifecycle.ActiveVersion != "2.0.0" ||
		plugin.Reason != "" || plugin.LastErrorCode != "" {
		t.Fatalf("recovered plugin response = %+v", plugin)
	}
	active, ok := server.pluginRegistry.Describe("tokenhub.recovery")
	if !ok || active.Version != "2.0.0" {
		t.Fatalf("recovered active descriptor = %+v, %t", active, ok)
	}
}

func TestAdminPluginUpdateRecoversStructurallyInvalidManifest(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.invalid-recovery", "Recovered Invalid Plugin", "2.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "invalid-recovery"), `
schema_version: 99
id: tokenhub.invalid-recovery
name: Invalid Plugin
version: 1.0.0
distribution:
  download_url: `+upstream.URL+`/invalid-recovery.zip
  checksum_sha256: `+adminSHA256Hex(archive)+`
tokenhub:
  plugin_api: v2
kinds: [extension]
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	if _, ok := server.pluginRegistry.Describe("tokenhub.invalid-recovery"); ok {
		t.Fatal("invalid package was registered before recovery")
	}
	packages, err := pluginmeta.NewRuntime(pluginDir).DiscoverRecoverable()
	if err != nil {
		t.Fatalf("discover invalid package: %v", err)
	}
	if len(packages) != 1 || packages[0].Manifest.ID != "tokenhub.invalid-recovery" || !packages[0].State.FailedValidation() {
		t.Fatalf("recoverable invalid packages = %+v", packages)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.invalid-recovery/update", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("update invalid plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode recovered invalid plugin response: %v", err)
	}
	plugin := body.Data.Plugin
	if plugin.Version != "2.0.0" || plugin.Status != pluginmeta.StatusEnabled || !plugin.Loadable ||
		!plugin.Lifecycle.ActiveEnabled || plugin.Lifecycle.ActiveVersion != "2.0.0" ||
		plugin.Reason != "" || plugin.LastErrorCode != "" || plugin.RollbackAvailable || plugin.RollbackVersion != "" {
		t.Fatalf("recovered invalid plugin response = %+v", plugin)
	}
}

func TestAdminPluginUpdateDoesNotPreserveFailedValidationPackageForRollback(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.schema-recovery", "Recovered Schema Plugin", "2.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	currentDir := filepath.Join(pluginDir, "tokenhub.schema-recovery")
	writeServerPluginManifest(t, currentDir, `
schema_version: 2
id: tokenhub.schema-recovery
name: Invalid Schema Plugin
version: 1.0.0
summary: Exercises recovery from a missing frontend schema.
category: ui_template
distribution:
  download_url: `+upstream.URL+`/schema-recovery.zip
  checksum_sha256: `+adminSHA256Hex(archive)+`
tokenhub:
  plugin_api: v2
kinds: [admin_ui]
placement: [presentation]
entry:
  frontend:
    schema: ui/missing.json
permissions:
  data:
    read: []
    write: []
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	packages, err := pluginmeta.NewRuntime(pluginDir).DiscoverRecoverable()
	if err != nil || len(packages) != 1 || !packages[0].State.FailedValidation() {
		t.Fatalf("discover failed-validation package = %+v, err=%v", packages, err)
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.schema-recovery/update", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("recover failed-validation plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode recovered plugin response: %v", err)
	}
	if body.Data.Plugin.Version != "2.0.0" || body.Data.Plugin.RollbackAvailable || body.Data.Plugin.RollbackVersion != "" {
		t.Fatalf("recovered plugin response = %+v, want no rollback to invalid package", body.Data.Plugin)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, ".rollback", "tokenhub.schema-recovery")); !os.IsNotExist(err) {
		t.Fatalf("failed-validation package was preserved for rollback: %v", err)
	}
}

func TestAdminPluginUpdateRetainsLastKnownGoodRollbackWhileRecoveringFailedUpdate(t *testing.T) {
	pluginDir := t.TempDir()
	recoveredArchive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.rollback-recovery", "Recovered Plugin", "3.0.0"),
	})
	var upstream *httptest.Server
	failedArchive := []byte(nil)
	upstream = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/failed.zip":
			_, _ = w.Write(failedArchive)
		case "/recovered.zip":
			_, _ = w.Write(recoveredArchive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	failedArchive = adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginCommandManifest(
			"tokenhub.rollback-recovery",
			"Failed Update",
			"2.0.0",
			upstream.URL+"/recovered.zip",
			adminSHA256Hex(recoveredArchive),
		),
		"run.sh": "#!/bin/sh\nprintf '{}'",
	})
	writeServerPluginManifest(t, filepath.Join(pluginDir, "tokenhub.rollback-recovery"), adminPluginManifestWithTestDistribution(
		"tokenhub.rollback-recovery",
		"Last Known Good Plugin",
		"1.0.0",
		upstream.URL+"/failed.zip",
		adminSHA256Hex(failedArchive),
		"automation",
	))
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	failedUpdate := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.rollback-recovery/update", map[string]any{}, "dev_admin_token")
	if failedUpdate.Code != http.StatusOK {
		t.Fatalf("install failed update: expected 200, got %d: %s", failedUpdate.Code, failedUpdate.Body)
	}
	failed, found, err := pluginmeta.NewRuntime(pluginDir).DescribeInstalledPackage("tokenhub.rollback-recovery")
	if err != nil || !found || !failed.State.FailedStartup() || failed.State.RollbackVersion != "1.0.0" {
		t.Fatalf("failed update package = %+v, found=%t, err=%v", failed, found, err)
	}

	recovery := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.rollback-recovery/update", map[string]any{}, "dev_admin_token")
	if recovery.Code != http.StatusOK {
		t.Fatalf("recover failed update: expected 200, got %d: %s", recovery.Code, recovery.Body)
	}
	rollback, found, err := pluginmeta.NewRuntime(pluginDir).DescribeRollbackPackage("tokenhub.rollback-recovery")
	if err != nil || !found {
		t.Fatalf("inspect retained rollback package: found=%t err=%v", found, err)
	}
	if rollback.Manifest.Version != "1.0.0" {
		t.Fatalf("rollback version = %q, want last-known-good 1.0.0", rollback.Manifest.Version)
	}
	current, found, err := pluginmeta.NewRuntime(pluginDir).DescribeInstalledPackage("tokenhub.rollback-recovery")
	if err != nil || !found || current.Manifest.Version != "3.0.0" || current.State.RollbackVersion != "1.0.0" {
		t.Fatalf("recovered package = %+v, found=%t, err=%v", current, found, err)
	}
}

func TestAdminPluginUpdateRejectsDependencyBreakingVersion(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": serverDependencyManifest("tokenhub.core", "2.0.0", "", ""),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "core"), adminPluginManifestWithTestDistribution("tokenhub.core", "Core Plugin", "1.4.0", upstream.URL+"/core.zip", adminSHA256Hex(archive), "automation"))
	writeServerPluginManifest(t, filepath.Join(pluginDir, "consumer"), serverDependencyManifest("tokenhub.consumer", "1.0.0", "tokenhub.core", "^1.0.0"))
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.core/update", map[string]any{}, "dev_admin_token")
	assertResponseBodyJSONError(t, response, http.StatusConflict, "plugin_dependency_unsatisfied")
	core, ok := server.pluginRegistry.Describe("tokenhub.core")
	if !ok || core.Version != "1.4.0" {
		t.Fatalf("core descriptor = %+v, %t; want version 1.4.0", core, ok)
	}
}

func TestAdminPluginRollbackRejectsDependencyBreakingVersion(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": serverDependencyManifest("tokenhub.core", "2.0.0", "", ""),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "core"), adminPluginManifestWithTestDistribution("tokenhub.core", "Core Plugin", "1.0.0", upstream.URL+"/core.zip", adminSHA256Hex(archive), "automation"))
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	update := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.core/update", map[string]any{}, "dev_admin_token")
	if update.Code != http.StatusOK {
		t.Fatalf("update plugin: expected 200, got %d: %s", update.Code, update.Body)
	}
	writeServerPluginManifest(t, filepath.Join(pluginDir, "consumer"), serverDependencyManifest("tokenhub.consumer", "1.0.0", "tokenhub.core", "^2.0.0"))
	if err := server.reloadPluginRuntime(context.Background()); err != nil {
		t.Fatalf("reload consumer: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.core/rollback", map[string]any{}, "dev_admin_token")
	assertResponseBodyJSONError(t, response, http.StatusConflict, "plugin_dependency_unsatisfied")
	current, found, err := pluginmeta.NewRuntime(pluginDir).DescribeInstalledPackage("tokenhub.core")
	if err != nil || !found {
		t.Fatalf("inspect current package found=%t err=%v", found, err)
	}
	if current.Manifest.Version != "2.0.0" {
		t.Fatalf("current version = %q, want unchanged 2.0.0", current.Manifest.Version)
	}
	rollback, found, err := pluginmeta.NewRuntime(pluginDir).DescribeRollbackPackage("tokenhub.core")
	if err != nil || !found {
		t.Fatalf("inspect rollback package found=%t err=%v", found, err)
	}
	if rollback.Manifest.Version != "1.0.0" {
		t.Fatalf("rollback version = %q, want preserved 1.0.0", rollback.Manifest.Version)
	}
}

func TestReloadPluginRuntimeWaitsForRequestSnapshot(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{PluginDir: t.TempDir()})
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	requestDone := make(chan struct{})
	originalRegistry := server.pluginRegistry
	server.mux.HandleFunc("GET /test/plugin-runtime-snapshot", func(w http.ResponseWriter, _ *http.Request) {
		close(requestEntered)
		<-releaseRequest
		if server.pluginRegistry != originalRegistry {
			t.Error("request observed a different plugin registry within one runtime snapshot")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	go func() {
		defer close(requestDone)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test/plugin-runtime-snapshot", nil))
		if response.Code != http.StatusNoContent {
			t.Errorf("snapshot request status = %d", response.Code)
		}
	}()
	<-requestEntered

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.reloadPluginRuntime(context.Background()) }()
	select {
	case err := <-reloadDone:
		close(releaseRequest)
		<-requestDone
		t.Fatalf("reload completed while request held a runtime snapshot: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseRequest)
	<-requestDone
	select {
	case err := <-reloadDone:
		if err != nil {
			t.Fatalf("reload plugin runtime: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not complete after request released its snapshot")
	}
	if server.pluginRegistry == originalRegistry {
		t.Fatal("reload did not publish a new plugin registry")
	}
}

func TestReloadPluginRuntimeWaitsForScheduledJobSnapshot(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{PluginDir: t.TempDir()})
	jobEntered := make(chan struct{})
	releaseJob := make(chan struct{})
	jobDone := make(chan struct{})
	originalRegistry := server.pluginRegistry
	broker := pluginmeta.NewBackgroundJobBroker()
	if err := broker.Register(pluginmeta.BackgroundJobDescriptor{
		PluginID:       "tokenhub.snapshot",
		JobID:          "snapshot.check",
		Schedule:       "1m",
		MaxConcurrency: 1,
	}, pluginmeta.BackgroundJobHandlerFunc(func(context.Context, pluginmeta.BackgroundJobInvocation) (pluginmeta.BackgroundJobResult, error) {
		close(jobEntered)
		<-releaseJob
		if server.pluginRegistry != originalRegistry {
			t.Error("scheduled job observed a different plugin registry within one runtime snapshot")
		}
		return pluginmeta.BackgroundJobResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	server.pluginBackgroundRunner.SetBroker(broker)
	go func() {
		defer close(jobDone)
		server.pluginBackgroundRunner.RunDue(context.Background(), time.Now().UTC(), "schedule")
	}()
	<-jobEntered

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.reloadPluginRuntime(context.Background()) }()
	select {
	case err := <-reloadDone:
		close(releaseJob)
		<-jobDone
		t.Fatalf("reload completed while scheduled job held a runtime snapshot: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseJob)
	<-jobDone
	select {
	case err := <-reloadDone:
		if err != nil {
			t.Fatalf("reload plugin runtime: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not complete after scheduled job released its snapshot")
	}
}

type runtimeSnapshotResponseAdapter struct {
	MockAdapter
	started chan struct{}
	release chan struct{}
}

func (a *runtimeSnapshotResponseAdapter) Responses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest) (any, Usage, error) {
	close(a.started)
	select {
	case <-a.release:
		return a.MockAdapter.Responses(ctx, provider, providerModel, request)
	case <-ctx.Done():
		return nil, Usage{}, ctx.Err()
	}
}

func TestReloadPluginRuntimeWaitsForResponseWorkerSnapshot(t *testing.T) {
	server, _, secret := newBackgroundResponseTestServer(t)
	server.config.PluginDir = t.TempDir()
	adapter := &runtimeSnapshotResponseAdapter{started: make(chan struct{}), release: make(chan struct{})}
	server.adapterRegistry.Register(ProviderMock, adapter, AdapterCapabilityResponses)
	submitBackgroundResponse(t, server.Handler(), secret, "runtime snapshot")
	select {
	case <-adapter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("response worker did not enter the provider adapter")
	}

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.reloadPluginRuntime(context.Background()) }()
	assertPluginReloadBlocked(t, reloadDone, "response worker")
	close(adapter.release)
	assertPluginReloadCompletes(t, reloadDone, "response worker")
}

func TestReloadPluginRuntimeWaitsForImageWorkerSnapshot(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Snapshot", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-snapshot", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_snapshot")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_snapshot", Name: "Image Snapshot", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	store.AddModel(Model{ID: openAIImageModelName, Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_snapshot", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderModel: openAIImageModelName, Priority: 1, Weight: 100, Status: StatusActive})
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "image-snapshot-secret", PluginDir: t.TempDir(), ImageStorageDir: t.TempDir(), ImageWorkerConcurrency: 1})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	started := make(chan struct{})
	release := make(chan struct{})
	imageBytes := realPNGFixture(t)
	server.imageRunner = func(ctx context.Context, _ RouteSelection, _ ImageJob) ([]byte, string, Usage, error) {
		close(started)
		select {
		case <-release:
			return imageBytes, "", Usage{TotalTokens: 1}, nil
		case <-ctx.Done():
			return nil, "", Usage{}, ctx.Err()
		}
	}
	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "runtime snapshot",
	}, secret, map[string]string{"Prefer": "respond-async"})
	if response.Code != http.StatusAccepted {
		t.Fatalf("submit image job: expected 202, got %d: %s", response.Code, response.Body)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("image worker did not enter the provider adapter")
	}

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- server.reloadPluginRuntime(context.Background()) }()
	assertPluginReloadBlocked(t, reloadDone, "image worker")
	close(release)
	assertPluginReloadCompletes(t, reloadDone, "image worker")
}

func assertPluginReloadBlocked(t *testing.T, reloadDone <-chan error, consumer string) {
	t.Helper()
	select {
	case err := <-reloadDone:
		t.Fatalf("reload completed while %s held a runtime snapshot: %v", consumer, err)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertPluginReloadCompletes(t *testing.T, reloadDone <-chan error, consumer string) {
	t.Helper()
	select {
	case err := <-reloadDone:
		if err != nil {
			t.Fatalf("reload after %s snapshot: %v", consumer, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("reload did not complete after %s released its snapshot", consumer)
	}
}

func TestAdminPluginActionFailurePersistsSafeMessage(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
	if err := server.pluginRegistry.Register(pluginmeta.Descriptor{ID: "tokenhub.failure", Name: "Failure", Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{PluginID: "tokenhub.failure", ActionID: "fail", Kind: pluginmeta.ActionKindRead}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, errors.New("plugin stderr leaked-secret")
	})); err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.failure/actions/fail", map[string]any{"access_token": "request-secret"}, "dev_admin_token")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("plugin action failure status = %d: %s", response.Code, response.Body)
	}
	for _, secret := range []string{"leaked-secret", "request-secret"} {
		if strings.Contains(response.Body, secret) {
			t.Fatalf("plugin action response leaked %q: %s", secret, response.Body)
		}
	}
	events := store.ListAuditEvents()
	if len(events) == 0 || events[0].Message != "Plugin action failed" {
		t.Fatalf("plugin action audit event = %+v, want safe failure message", events)
	}
	if strings.Contains(events[0].Message, "leaked-secret") || strings.Contains(events[0].Message, "request-secret") {
		t.Fatalf("plugin action audit message leaked secrets: %+v", events[0])
	}
}

func TestPluginDescriptorHasSettingsRequiresRenderedCapability(t *testing.T) {
	descriptor := pluginmeta.Descriptor{Settings: pluginmeta.ManifestSettings{Scopes: []string{"administrator", "project"}}}
	if pluginDescriptorHasSettings(descriptor) {
		t.Fatal("settings scopes without a rendered capability exposed an empty Settings page")
	}
	descriptor.Capabilities = []pluginmeta.CapabilityDescriptor{{Kind: pluginmeta.CapabilityKindAdminUI, Name: pluginmeta.AdminUICapabilityLegacySettingsPanel}}
	if pluginDescriptorHasSettings(descriptor) {
		t.Fatal("legacy settings panel without a detail renderer exposed an empty Settings page")
	}
	descriptor.Capabilities = []pluginmeta.CapabilityDescriptor{{Kind: pluginmeta.CapabilityKindSIM, Name: pluginmeta.SIMCapabilityThemeTokens}}
	if !pluginDescriptorHasSettings(descriptor) {
		t.Fatal("SIM theme settings capability did not expose Settings")
	}
}

func serverDependencyManifest(id string, version string, dependencyID string, constraint string) string {
	dependencies := ""
	if dependencyID != "" {
		dependencies = "dependencies:\n  - id: " + dependencyID + "\n    version: '" + constraint + "'\n"
	}
	return `
schema_version: 2
id: ` + id + `
name: Dependency Test Plugin
version: ` + version + `
summary: Exercises plugin dependencies.
category: automation
tokenhub:
  plugin_api: v2
kinds: [extension]
placement: []
` + dependencies + `permissions:
  data:
    read: []
    write: []
`
}

func adminPluginManifestWithTestDistribution(id string, name string, version string, downloadURL string, checksum string, category string) string {
	schemaVersion := "1"
	pluginAPI := "v1"
	summary := ""
	categoryLine := ""
	if category != "" {
		schemaVersion = "2"
		pluginAPI = "v2"
		summary = "summary: Exercises plugin updates.\n"
		categoryLine = "category: " + category + "\n"
	}
	return "schema_version: " + schemaVersion + "\n" +
		"id: " + id + "\n" +
		"name: " + name + "\n" +
		"version: " + version + "\n" +
		summary + categoryLine +
		"distribution:\n  download_url: " + downloadURL + "\n  checksum_sha256: " + checksum + "\n" +
		"tokenhub:\n  plugin_api: " + pluginAPI + "\n" +
		"kinds: [extension]\nplacement: []\npermissions:\n  data:\n    read: []\n    write: []\n"
}

func adminPluginCommandManifest(id string, name string, version string, downloadURL string, checksum string) string {
	return `
schema_version: 2
id: ` + id + `
name: ` + name + `
version: ` + version + `
summary: Exercises command package update recovery.
category: automation
distribution:
  download_url: ` + downloadURL + `
  checksum_sha256: ` + checksum + `
tokenhub:
  plugin_api: v2
kinds: [extension]
placement: [management_action]
entry:
  backend:
    protocol: stdio-json-v1
    command: run.sh
capabilities:
  actions:
    - id: recovery.run
      kind: read
      title: Recover
permissions:
  data:
    read: []
    write: []
`
}

func adminPluginActionManifest(id string, version string, actionID string, downloadURL string, checksum string) string {
	distribution := ""
	if downloadURL != "" {
		distribution = "distribution:\n  download_url: " + downloadURL + "\n  checksum_sha256: " + checksum + "\n"
	}
	return `
schema_version: 1
id: ` + id + `
name: Hot Reload Plugin
version: ` + version + `
` + distribution + `tokenhub:
  plugin_api: v1
kinds: [extension]
placement: [management_action]
capabilities:
  actions:
    - id: ` + actionID + `
      kind: read
      title: Synchronize
`
}
