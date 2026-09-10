package plugin

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeInstallZipArchiveInstallsValidatedPackage(t *testing.T) {
	root := t.TempDir()
	archive := pluginZip(t, map[string]zipFixtureFile{
		"bundle/plugin.yaml": {
			Body: minimalPluginManifest("tokenhub.provider.kimi", "Kimi Provider", "1.2.3"),
			Mode: 0o644,
		},
		"bundle/bin/provider.sh": {
			Body: "#!/bin/sh\nprintf '{}'\n",
			Mode: 0o755,
		},
	})
	checksum := sha256Hex(archive)

	pkg, err := NewRuntime(root).InstallZipArchive(archive, InstallOptions{
		ChecksumSHA256: checksum,
		InitialState: PackageState{
			Status: StatusDisabled,
			Reason: "review before enabling",
		},
	})
	if err != nil {
		t.Fatalf("install plugin archive: %v", err)
	}
	if pkg.Manifest.ID != "tokenhub.provider.kimi" || pkg.Manifest.Version != "1.2.3" {
		t.Fatalf("installed package manifest = %+v", pkg.Manifest)
	}
	if pkg.State.Status != StatusDisabled || pkg.State.Reason != "review before enabling" {
		t.Fatalf("installed package state = %+v", pkg.State)
	}
	scriptInfo, err := os.Stat(filepath.Join(root, "tokenhub.provider.kimi", "bin", "provider.sh"))
	if err != nil {
		t.Fatalf("installed script missing: %v", err)
	}
	if scriptInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed script mode = %v, want executable bit", scriptInfo.Mode().Perm())
	}
	packages, err := NewRuntime(root).Discover()
	if err != nil {
		t.Fatalf("discover installed package: %v", err)
	}
	if len(packages) != 1 || packages[0].State.Status != StatusDisabled {
		t.Fatalf("discovered packages = %+v, want disabled installed package", packages)
	}
}

func TestRuntimeInstallZipArchiveRejectsChecksumMismatch(t *testing.T) {
	archive := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.checksum", "Checksum", "1.0.0"), Mode: 0o644},
	})

	_, err := NewRuntime(t.TempDir()).InstallZipArchive(archive, InstallOptions{
		ChecksumSHA256: strings.Repeat("0", 64),
	})
	if !errors.Is(err, ErrInstallChecksumMismatch) {
		t.Fatalf("install checksum error = %v, want ErrInstallChecksumMismatch", err)
	}
}

func TestRuntimeInstallZipArchiveRejectsEscapingEntry(t *testing.T) {
	archive := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.escape", "Escape", "1.0.0"), Mode: 0o644},
		"../escape":   {Body: "bad", Mode: 0o644},
	})

	_, err := NewRuntime(t.TempDir()).InstallZipArchive(archive, InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "escapes the plugin directory") {
		t.Fatalf("install escaping archive error = %v", err)
	}
}

func TestRuntimeInstallZipArchiveReplaceControlsUpdates(t *testing.T) {
	root := t.TempDir()
	runtime := NewRuntime(root)
	first := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.replace", "Replace", "1.0.0"), Mode: 0o644},
	})
	if _, err := runtime.InstallZipArchive(first, InstallOptions{}); err != nil {
		t.Fatalf("install first package: %v", err)
	}
	next := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.replace", "Replace", "2.0.0"), Mode: 0o644},
	})
	if _, err := runtime.InstallZipArchive(next, InstallOptions{}); !errors.Is(err, ErrInstallPackageExists) {
		t.Fatalf("install existing package error = %v, want ErrInstallPackageExists", err)
	}
	pkg, err := runtime.InstallZipArchive(next, InstallOptions{Replace: true})
	if err != nil {
		t.Fatalf("replace package: %v", err)
	}
	if pkg.Manifest.Version != "2.0.0" {
		t.Fatalf("replaced package version = %q, want 2.0.0", pkg.Manifest.Version)
	}
}

func TestRuntimeInstallZipArchiveKeepsDistinctPluginIDsIsolated(t *testing.T) {
	root := t.TempDir()
	runtime := NewRuntime(root)
	colonID := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml":  {Body: minimalPluginManifest("vendor:plugin", "Colon Plugin", "1.0.0"), Mode: 0o644},
		"identity.txt": {Body: "colon", Mode: 0o644},
	})
	hyphenID := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml":  {Body: minimalPluginManifest("vendor-plugin", "Hyphen Plugin", "1.0.0"), Mode: 0o644},
		"identity.txt": {Body: "hyphen", Mode: 0o644},
	})

	colonPackage, err := runtime.InstallZipArchive(colonID, InstallOptions{})
	if err != nil {
		t.Fatalf("install colon plugin: %v", err)
	}
	hyphenPackage, err := runtime.InstallZipArchive(hyphenID, InstallOptions{Replace: true})
	if err != nil {
		t.Fatalf("install hyphen plugin: %v", err)
	}
	if colonPackage.Dir == hyphenPackage.Dir {
		t.Fatalf("distinct plugin ids share package directory %q", colonPackage.Dir)
	}
	for _, testCase := range []struct {
		id       string
		identity string
	}{
		{id: "vendor:plugin", identity: "colon"},
		{id: "vendor-plugin", identity: "hyphen"},
	} {
		pkg, found, err := runtime.DescribeInstalledPackage(testCase.id)
		if err != nil || !found {
			t.Fatalf("describe %s found=%t err=%v", testCase.id, found, err)
		}
		identity, err := os.ReadFile(filepath.Join(pkg.Dir, "identity.txt"))
		if err != nil || string(identity) != testCase.identity {
			t.Fatalf("%s identity = %q err=%v, want %q", testCase.id, identity, err, testCase.identity)
		}
	}
}

func TestRuntimeInstallZipArchiveSupportsLongLossyPluginID(t *testing.T) {
	pluginID := "vendor:" + strings.Repeat("long-plugin-id", 20)
	archive := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest(pluginID, "Long Plugin ID", "1.0.0"), Mode: 0o644},
	})

	pkg, err := NewRuntime(t.TempDir()).InstallZipArchive(archive, InstallOptions{})
	if err != nil {
		t.Fatalf("install long lossy plugin id: %v", err)
	}
	if pkg.Manifest.ID != pluginID {
		t.Fatalf("installed plugin id = %q, want %q", pkg.Manifest.ID, pluginID)
	}
}

func TestRuntimeInstallZipArchiveRejectsReplacementAcrossPluginIDs(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "vendor-plugin")
	writeManifest(t, legacyDir, minimalPluginManifest("vendor:plugin", "Legacy Colon Plugin", "1.0.0"))
	if err := os.WriteFile(filepath.Join(legacyDir, "identity.txt"), []byte("colon"), 0o644); err != nil {
		t.Fatalf("write legacy package identity: %v", err)
	}
	hyphenID := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml":  {Body: minimalPluginManifest("vendor-plugin", "Hyphen Plugin", "1.0.0"), Mode: 0o644},
		"identity.txt": {Body: "hyphen", Mode: 0o644},
	})

	_, err := NewRuntime(root).InstallZipArchive(hyphenID, InstallOptions{Replace: true})
	if !errors.Is(err, ErrInstallPackageExists) {
		t.Fatalf("cross-plugin replacement error = %v, want ErrInstallPackageExists", err)
	}
	manifest, readErr := readManifestOnly(legacyDir)
	if readErr != nil || manifest.ID != "vendor:plugin" {
		t.Fatalf("preserved legacy package id = %q err=%v, want vendor:plugin", manifest.ID, readErr)
	}
	identity, readErr := os.ReadFile(filepath.Join(legacyDir, "identity.txt"))
	if readErr != nil || string(identity) != "colon" {
		t.Fatalf("preserved legacy package identity = %q err=%v, want colon", identity, readErr)
	}
}

func TestRuntimeInstallZipArchiveReplacesMatchingLegacyPluginDirectory(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "vendor-plugin")
	writeManifest(t, legacyDir, minimalPluginManifest("vendor:plugin", "Legacy Colon Plugin", "1.0.0"))
	if err := os.WriteFile(filepath.Join(legacyDir, "identity.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write legacy package identity: %v", err)
	}
	archive := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml":  {Body: minimalPluginManifest("vendor:plugin", "Colon Plugin", "2.0.0"), Mode: 0o644},
		"identity.txt": {Body: "new", Mode: 0o644},
	})

	pkg, err := NewRuntime(root).InstallZipArchive(archive, InstallOptions{Replace: true})
	if err != nil {
		t.Fatalf("replace legacy plugin package: %v", err)
	}
	if pkg.Dir != legacyDir || pkg.Manifest.Version != "2.0.0" {
		t.Fatalf("updated package = %+v, want version 2.0.0 in %s", pkg, legacyDir)
	}
	identity, err := os.ReadFile(filepath.Join(legacyDir, "identity.txt"))
	if err != nil || string(identity) != "new" {
		t.Fatalf("updated legacy package identity = %q err=%v, want new", identity, err)
	}
	packages, err := NewRuntime(root).Discover()
	if err != nil {
		t.Fatalf("discover updated legacy package: %v", err)
	}
	if len(packages) != 1 || packages[0].Manifest.ID != "vendor:plugin" || packages[0].Manifest.Version != "2.0.0" {
		t.Fatalf("packages after legacy update = %+v, want one updated package", packages)
	}
}

func TestRuntimeInstallZipArchiveReplacesSemanticallyInvalidMatchingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "tokenhub.invalid")
	writeManifest(t, target, `
schema_version: 1
id: tokenhub.invalid
name: Invalid Plugin
version: 1.0.0
tokenhub:
  plugin_api: unsupported
kinds:
  - extension
`)
	archive := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml": {Body: minimalPluginManifest("tokenhub.invalid", "Recovered Plugin", "2.0.0"), Mode: 0o644},
	})

	pkg, err := NewRuntime(root).InstallZipArchive(archive, InstallOptions{Replace: true})
	if err != nil {
		t.Fatalf("replace semantically invalid package: %v", err)
	}
	if pkg.Manifest.ID != "tokenhub.invalid" || pkg.Manifest.Version != "2.0.0" {
		t.Fatalf("recovered package = %+v, want valid version 2.0.0", pkg)
	}
}

func TestRuntimeInstallZipArchivePreservesRollbackPackage(t *testing.T) {
	root := t.TempDir()
	runtime := NewRuntime(root)
	first := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml":       {Body: minimalPluginManifest("tokenhub.rollback", "Rollback", "1.0.0"), Mode: 0o644},
		"bin/provider.sh":   {Body: "#!/bin/sh\nprintf old\n", Mode: 0o755},
		"private/state.txt": {Body: "old-state", Mode: 0o600},
	})
	if _, err := runtime.InstallZipArchive(first, InstallOptions{}); err != nil {
		t.Fatalf("install first package: %v", err)
	}
	next := pluginZip(t, map[string]zipFixtureFile{
		"plugin.yaml":     {Body: minimalPluginManifest("tokenhub.rollback", "Rollback", "2.0.0"), Mode: 0o644},
		"bin/provider.sh": {Body: "#!/bin/sh\nprintf new\n", Mode: 0o755},
	})
	pkg, err := runtime.InstallZipArchive(next, InstallOptions{
		Replace:          true,
		PreserveRollback: true,
		InitialState: PackageState{
			Status:          StatusEnabled,
			RollbackVersion: "1.0.0",
			AuditEvent:      PackageLifecyclePendingRestart,
		},
	})
	if err != nil {
		t.Fatalf("replace package with rollback backup: %v", err)
	}
	if pkg.Manifest.Version != "2.0.0" || !pkg.State.RollbackAvailable() {
		t.Fatalf("updated package = %+v, want version 2.0.0 with rollback", pkg)
	}
	rollbackManifest, err := readManifestOnly(filepath.Join(root, ".rollback", "tokenhub.rollback"))
	if err != nil {
		t.Fatalf("read rollback manifest: %v", err)
	}
	if rollbackManifest.Version != "1.0.0" {
		t.Fatalf("rollback manifest version = %q, want 1.0.0", rollbackManifest.Version)
	}
	data, err := os.ReadFile(filepath.Join(root, ".rollback", "tokenhub.rollback", "private", "state.txt"))
	if err != nil {
		t.Fatalf("read rollback private data: %v", err)
	}
	if string(data) != "old-state" {
		t.Fatalf("rollback private data = %q, want old-state", data)
	}
}

type zipFixtureFile struct {
	Body string
	Mode os.FileMode
}

func pluginZip(t *testing.T, files map[string]zipFixtureFile) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, file := range files {
		header := &zip.FileHeader{Name: name}
		header.SetMode(file.Mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(file.Body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func minimalPluginManifest(id string, name string, version string) string {
	return `
schema_version: 1
id: ` + id + `
name: ` + name + `
version: ` + version + `
tokenhub:
  plugin_api: v1
kinds:
  - extension
`
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
