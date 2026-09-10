package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeInspectsPackageFilesWithoutExposingUnsafeContent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "example.plugin")
	writeInspectionFile(t, dir, "plugin.yaml", "schema_version: 1\nid: example.plugin\nname: Example\nversion: 1.0.0\ntokenhub:\n  plugin_api: v1\nkinds: [extension]\n")
	writeInspectionFile(t, dir, "src/main.go", "package main\n")
	writeInspectionFile(t, dir, "ui/schema.json", "{\"schema_version\":1}\n")
	writeInspectionFile(t, dir, "plugin.state.json", "{\"status\":\"enabled\"}\n")
	writeInspectionFile(t, dir, "config/credentials.json", "{\"token\":\"secret\"}\n")
	writeInspectionFile(t, dir, "bin/plugin", string([]byte{0, 1, 2}))

	inspection, err := NewRuntime(root).InspectPackage("example.plugin")
	if err != nil {
		t.Fatalf("inspect plugin package: %v", err)
	}
	if inspection.FileCount != 6 {
		t.Fatalf("file count = %d, want 6", inspection.FileCount)
	}
	files := map[string]PackageFileInspection{}
	for _, file := range inspection.Files {
		files[file.Path] = file
	}
	if files["plugin.yaml"].Kind != "manifest" || !files["plugin.yaml"].Viewable {
		t.Fatalf("manifest inspection = %+v", files["plugin.yaml"])
	}
	if files["src/main.go"].Kind != "source" || !files["src/main.go"].Viewable {
		t.Fatalf("source inspection = %+v", files["src/main.go"])
	}
	for _, path := range []string{"plugin.state.json", "config/credentials.json", "bin/plugin"} {
		if files[path].Viewable {
			t.Fatalf("unsafe file %s is viewable: %+v", path, files[path])
		}
	}
}

func TestRuntimeReadsOnlyViewablePackageFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "example.plugin")
	writeInspectionFile(t, dir, "plugin.yaml", "schema_version: 1\nid: example.plugin\n")
	writeInspectionFile(t, dir, "src/main.go", "package main\n")
	writeInspectionFile(t, dir, "private-key.txt", "do-not-return\n")

	content, err := NewRuntime(root).ReadPackageFile("example.plugin", "src/main.go")
	if err != nil {
		t.Fatalf("read package source: %v", err)
	}
	if content.Content != "package main\n" || content.Kind != "source" {
		t.Fatalf("package source = %+v", content)
	}
	for _, path := range []string{"../outside.txt", "private-key.txt"} {
		if _, err := NewRuntime(root).ReadPackageFile("example.plugin", path); err == nil {
			t.Fatalf("read unsafe package path %q succeeded", path)
		}
	}
	if _, err := NewRuntime(root).ReadPackageFile("missing.plugin", "plugin.yaml"); !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("missing package error = %v, want ErrPackageNotFound", err)
	}
}

func TestRuntimeSkipsPackageSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "example.plugin")
	writeInspectionFile(t, dir, "plugin.yaml", "schema_version: 1\nid: example.plugin\n")
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	inspection, err := NewRuntime(root).InspectPackage("example.plugin")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.FileCount != 1 || inspection.Files[0].Path != "plugin.yaml" {
		t.Fatalf("inspection followed symlink: %+v", inspection)
	}
}

func writeInspectionFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
