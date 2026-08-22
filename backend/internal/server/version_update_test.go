package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeUpdateDownloadsVerifiesAndActivatesRelease(t *testing.T) {
	root := prepareNativeInstallRoot(t, "0.3.1", nil)
	// The target binary must satisfy database preparation and verification
	// before the release can be activated.
	targetBinary := "#!/bin/sh\ncase \"$2\" in prepare|verify) exit 0;; *) exit 1;; esac\n"
	archive := nativeTestArchive(t, "0.3.2", map[string]nativeTestArchiveEntry{
		"bin/tokenhub": {content: targetBinary, mode: 0o755},
	})
	checksum := sha256.Sum256(archive)
	assetName := "tokenhub_0.3.2_linux_amd64.tar.gz"

	var releases *httptest.Server
	releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/TokenHub/releases/latest":
			writeTestJSON(w, nativeTestRelease("v0.3.2", assetName, releases.URL))
		case "/assets/" + assetName:
			_, _ = w.Write(archive)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(hex.EncodeToString(checksum[:]) + "  " + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer releases.Close()

	service := nativeTestVersionService(root, "0.3.1", releases)
	service.databaseURL = "sqlite://" + filepath.Join(t.TempDir(), "preflight.db")
	version, err := service.performNativeUpdate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.3.2" {
		t.Fatalf("updated version = %q, want 0.3.2", version)
	}
	assertNativeCurrentVersion(t, root, "0.3.2")
	if _, err := os.Stat(filepath.Join(root, "releases", "0.3.1", "VERSION")); err != nil {
		t.Fatalf("previous release was not preserved: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "current", "bin", "tokenhub"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != targetBinary {
		t.Fatalf("activated backend content = %q", content)
	}
	version, err = service.performNativeUpdate(t.Context())
	if err != nil || version != "0.3.2" {
		t.Fatalf("repeated update = %q, %v; want idempotent 0.3.2", version, err)
	}
}

func TestNativeUpdateChecksumFailureKeepsCurrentRelease(t *testing.T) {
	root := prepareNativeInstallRoot(t, "0.3.1", nil)
	archive := nativeTestArchive(t, "0.3.2", nil)
	assetName := "tokenhub_0.3.2_linux_amd64.tar.gz"

	var releases *httptest.Server
	releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/TokenHub/releases/latest":
			writeTestJSON(w, nativeTestRelease("v0.3.2", assetName, releases.URL))
		case "/assets/" + assetName:
			_, _ = w.Write(archive)
		case "/assets/checksums.txt":
			_, _ = w.Write([]byte(strings.Repeat("0", 64) + "  " + assetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer releases.Close()

	service := nativeTestVersionService(root, "0.3.1", releases)
	if _, err := service.performNativeUpdate(t.Context()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("performNativeUpdate error = %v, want checksum failure", err)
	}
	assertNativeCurrentVersion(t, root, "0.3.1")
	if _, err := os.Stat(filepath.Join(root, "releases", "0.3.2")); !os.IsNotExist(err) {
		t.Fatalf("failed update left an installed target: %v", err)
	}
}

func TestExtractNativeArchiveRejectsPathTraversal(t *testing.T) {
	archive := nativeTestArchive(t, "0.3.2", map[string]nativeTestArchiveEntry{
		"../escaped": {content: "unsafe", mode: 0644},
	})
	archivePath := filepath.Join(t.TempDir(), "release.tar.gz")
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "bundle")
	err := extractNativeArchive(archivePath, destination)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("extractNativeArchive error = %v, want unsafe path rejection", err)
	}
}

func TestNativeRollbackActivatesInstalledAllowedRelease(t *testing.T) {
	root := prepareNativeInstallRoot(t, "0.3.2", map[string]string{"0.3.1": "old"})
	var calls int
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer releases.Close()

	service := nativeTestVersionService(root, "0.3.2", releases)
	version, err := service.rollbackNativeRelease(context.Background(), "0.3.1")
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.3.1" {
		t.Fatalf("rollback version = %q, want 0.3.1", version)
	}
	if calls != 0 {
		t.Fatalf("installed rollback made %d GitHub requests, want none", calls)
	}
	assertNativeCurrentVersion(t, root, "0.3.1")
	version, err = service.rollbackNativeRelease(context.Background(), "0.3.1")
	if err != nil || version != "0.3.1" {
		t.Fatalf("repeated rollback = %q, %v; want idempotent 0.3.1", version, err)
	}
}

func TestNativeRollbackListsInstalledVersionsWhenGitHubIsUnavailable(t *testing.T) {
	root := prepareNativeInstallRoot(t, "0.3.3", map[string]string{
		"0.3.1": "old",
		"0.3.2": "previous",
	})
	var calls int
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer releases.Close()

	service := nativeTestVersionService(root, "0.3.3", releases)
	versions, err := service.listRollbackVersions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != "0.3.2" || versions[1].Version != "0.3.1" {
		t.Fatalf("offline rollback versions = %+v, want 0.3.2 and 0.3.1", versions)
	}
	if calls != 1 {
		t.Fatalf("offline rollback listing made %d GitHub requests, want 1", calls)
	}
	_, err = service.listRollbackVersions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cached offline rollback listing made %d GitHub requests, want 1", calls)
	}
}

func TestNativeVersionInfoReportsPendingRestartAfterActivation(t *testing.T) {
	root := prepareNativeInstallRoot(t, "0.3.1", map[string]string{"0.3.2": "next"})
	var calls int
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/test/TokenHub/releases/latest" {
			http.NotFound(w, r)
			return
		}
		writeTestJSON(w, map[string]any{"tag_name": "v0.3.2"})
	}))
	defer releases.Close()

	service := nativeTestVersionService(root, "0.3.1", releases)
	initial := service.checkUpdate(t.Context(), false)
	if initial.PendingRestart != "" {
		t.Fatalf("initial pending restart version = %q, want empty", initial.PendingRestart)
	}
	if err := service.activateNativeRelease("0.3.2"); err != nil {
		t.Fatal(err)
	}

	cached := service.checkUpdate(t.Context(), false)
	if cached.PendingRestart != "0.3.2" {
		t.Fatalf("cached pending restart version = %q, want 0.3.2", cached.PendingRestart)
	}
	if calls != 1 {
		t.Fatalf("cached pending restart check made %d GitHub calls, want 1", calls)
	}
}

func TestContainerDeploymentRejectsOnlineUpdateEndpoint(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken:     "native-test-admin-token",
		AppVersion:     "0.3.1",
		BuildType:      releaseBuildType,
		DeploymentType: containerDeploymentType,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/system/update", nil)
	request.Header.Set("authorization", "Bearer native-test-admin-token")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("container update status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "managed_update_unavailable" {
		t.Fatalf("container update error code = %q", payload.Error.Code)
	}
	assertSystemVersionAudit(t, store, "update", "failed", "")
}

func TestManagedContainerReportsUpdateSupportAndPendingRestart(t *testing.T) {
	root := prepareNativeInstallRoot(t, "0.3.1", map[string]string{"0.3.2": "next"})
	service := newVersionService(Config{
		AppVersion:        "0.3.1",
		BuildType:         releaseBuildType,
		DeploymentType:    containerDeploymentType,
		ManagedUpdates:    true,
		ReleaseRepository: "test/TokenHub",
		InstallRoot:       root,
	})

	if !service.baseVersionInfo().UpdateSupported {
		t.Fatal("managed container did not report online update support")
	}
	release, err := service.acquireNativeOperation()
	if err != nil {
		t.Fatalf("managed container could not acquire update operation: %v", err)
	}
	release()
	if err := service.activateNativeRelease("0.3.2"); err != nil {
		t.Fatal(err)
	}
	if got := service.pendingNativeRestartVersion(); got != "0.3.2" {
		t.Fatalf("pending restart version = %q, want 0.3.2", got)
	}
}

func TestNativeRestartEndpointSignalsProcessAfterResponse(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken:     "native-test-admin-token",
		AppVersion:     "0.3.2",
		BuildType:      releaseBuildType,
		DeploymentType: nativeDeploymentType,
		InstallRoot:    t.TempDir(),
	})
	restarted := make(chan struct{})
	app.versions.restartProcess = func() error {
		close(restarted)
		return nil
	}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/system/restart", nil)
	request.Header.Set("authorization", "Bearer native-test-admin-token")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("native restart status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("native restart callback was not invoked")
	}
	waitForSystemVersionAudit(t, store, "restart", "success", "0.3.2")
}

func TestNativeRestartEndpointRecordsCallbackFailure(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken:     "native-test-admin-token",
		AppVersion:     "0.3.2",
		BuildType:      releaseBuildType,
		DeploymentType: nativeDeploymentType,
		InstallRoot:    t.TempDir(),
	})
	restartErr := errors.New("restart callback failed")
	app.versions.restartProcess = func() error {
		return restartErr
	}

	request := httptest.NewRequest(http.MethodPost, "/api/admin/system/restart", nil)
	request.Header.Set("authorization", "Bearer native-test-admin-token")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("native restart status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	waitForSystemVersionAudit(t, store, "restart", "failed", "0.3.2")
}

func assertSystemVersionAudit(t *testing.T, store *MemoryStore, action, status, resourceID string) {
	t.Helper()
	for _, event := range store.ListAuditEvents() {
		if event.Action != action || event.ResourceType != "system_version" {
			continue
		}
		if event.Status != status || event.ResourceID != resourceID {
			t.Fatalf("system version audit = %+v, want action=%s status=%s resource=%s", event, action, status, resourceID)
		}
		return
	}
	t.Fatalf("missing %s system version audit event: %+v", action, store.ListAuditEvents())
}

func waitForSystemVersionAudit(t *testing.T, store *MemoryStore, action, status, resourceID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range store.ListAuditEvents() {
			if event.Action != action || event.ResourceType != "system_version" {
				continue
			}
			if event.Status != status || event.ResourceID != resourceID {
				t.Fatalf("system version audit = %+v, want action=%s status=%s resource=%s", event, action, status, resourceID)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missing %s system version audit event: %+v", action, store.ListAuditEvents())
}

func TestValidateNativeAssetURL(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"https://github.com/astaxie/TokenHub/releases/download/v0.3.2/archive.tar.gz",
		"https://release-assets.githubusercontent.com/github-production-release-asset/file",
	} {
		if err := validateNativeAssetURL(rawURL); err != nil {
			t.Errorf("validateNativeAssetURL(%q) = %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{
		"http://github.com/astaxie/TokenHub/releases/download/v0.3.2/archive.tar.gz",
		"https://github.com:8443/archive.tar.gz",
		"https://example.com/archive.tar.gz",
	} {
		if err := validateNativeAssetURL(rawURL); err == nil {
			t.Errorf("validateNativeAssetURL(%q) unexpectedly succeeded", rawURL)
		}
	}
}

func TestNativeArchiveNameSupportsLinux(t *testing.T) {
	t.Parallel()
	tests := []struct {
		os   string
		arch string
		want string
	}{
		{os: "linux", arch: "amd64", want: "tokenhub_0.3.5_linux_amd64.tar.gz"},
		{os: "linux", arch: "arm64", want: "tokenhub_0.3.5_linux_arm64.tar.gz"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.os+"/"+test.arch, func(t *testing.T) {
			t.Parallel()
			service := &versionService{runtimeOS: test.os, runtimeArch: test.arch}
			got, err := service.nativeArchiveName("0.3.5")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("nativeArchiveName = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNativeArchiveNameRejectsMacOS(t *testing.T) {
	t.Parallel()
	service := &versionService{runtimeOS: "darwin", runtimeArch: "arm64"}
	if _, err := service.nativeArchiveName("0.3.5"); err == nil {
		t.Fatal("expected macOS native update to be rejected")
	}
}

type nativeTestArchiveEntry struct {
	content string
	mode    int64
}

func nativeTestArchive(t *testing.T, version string, extra map[string]nativeTestArchiveEntry) []byte {
	t.Helper()
	entries := map[string]nativeTestArchiveEntry{
		"bin/tokenhub":               {content: "tokenhub-" + version, mode: 0755},
		"bin/node":                   {content: "node-" + version, mode: 0755},
		"bin/tokenhub-run":           {content: "run-" + version, mode: 0755},
		"frontend/server.js":         {content: "server-" + version, mode: 0644},
		"catalog/model-catalog.yaml": {content: "models: []", mode: 0644},
		"catalog/provider-catalog.json": {
			content: `{"providers":[]}`,
			mode:    0644,
		},
		"deploy/tokenhub.service": {content: "[Service]", mode: 0644},
		"VERSION":                 {content: version + "\n", mode: 0644},
	}
	for name, entry := range extra {
		entries[name] = entry
	}

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, entry := range entries {
		header := &tar.Header{
			Name: name,
			Mode: entry.mode,
			Size: int64(len(entry.content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func prepareNativeInstallRoot(t *testing.T, currentVersion string, additional map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "releases"), 0750); err != nil {
		t.Fatal(err)
	}
	createNativeTestBundle(t, filepath.Join(root, "releases", currentVersion), currentVersion)
	for version := range additional {
		createNativeTestBundle(t, filepath.Join(root, "releases", version), version)
	}
	if err := os.Symlink(filepath.Join("releases", currentVersion), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return root
}

func createNativeTestBundle(t *testing.T, root string, version string) {
	t.Helper()
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"bin/tokenhub":               {content: "tokenhub-" + version, mode: 0755},
		"bin/node":                   {content: "node-" + version, mode: 0755},
		"bin/tokenhub-run":           {content: "run-" + version, mode: 0755},
		"frontend/server.js":         {content: "server-" + version, mode: 0644},
		"catalog/model-catalog.yaml": {content: "models: []", mode: 0644},
		"catalog/provider-catalog.json": {
			content: `{"providers":[]}`,
			mode:    0644,
		},
		"deploy/tokenhub.service": {content: "[Service]", mode: 0644},
		"VERSION":                 {content: version + "\n", mode: 0644},
	}
	for name, file := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file.content), file.mode); err != nil {
			t.Fatal(err)
		}
	}
}

func nativeTestRelease(tag string, assetName string, baseURL string) map[string]any {
	return map[string]any{
		"tag_name": tag,
		"assets": []map[string]any{
			{
				"name":                 assetName,
				"browser_download_url": baseURL + "/assets/" + assetName,
				"size":                 1024,
			},
			{
				"name":                 "checksums.txt",
				"browser_download_url": baseURL + "/assets/checksums.txt",
				"size":                 128,
			},
		},
	}
}

func nativeTestVersionService(root string, currentVersion string, releases *httptest.Server) *versionService {
	service := newVersionService(Config{
		AppVersion:        currentVersion,
		BuildType:         releaseBuildType,
		DeploymentType:    nativeDeploymentType,
		ReleaseRepository: "test/TokenHub",
		InstallRoot:       root,
	})
	service.apiBaseURL = releases.URL
	service.client = releases.Client()
	service.downloadClient = releases.Client()
	service.validateAssetURL = func(string) error { return nil }
	service.runtimeOS = "linux"
	service.runtimeArch = "amd64"
	return service
}

func assertNativeCurrentVersion(t *testing.T, root string, expected string) {
	t.Helper()
	target, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join("releases", expected) {
		t.Fatalf("current link = %q, want %q", target, filepath.Join("releases", expected))
	}
}
