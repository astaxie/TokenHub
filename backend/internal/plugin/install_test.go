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
