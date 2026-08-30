package plugin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectInstallZipArchiveReadsManifestWithoutInstallingPackageState(t *testing.T) {
	pluginDir := t.TempDir()
	archive := pluginPreviewZip(t, map[string]string{
		"bundle/plugin.yaml": minimalPluginManifest("tokenhub.preview.sample", "Preview Sample", "1.2.0"),
	})
	manifest, err := InspectInstallZipArchive(archive)
	if err != nil {
		t.Fatalf("inspect install archive: %v", err)
	}
	if manifest.ID != "tokenhub.preview.sample" || manifest.Version != "1.2.0" {
		t.Fatalf("manifest = %+v, want preview sample 1.2.0", manifest)
	}
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("plugin dir has %d entries after preview, want no package mutation", len(entries))
	}
}

func TestInspectInstallZipArchiveRejectsTraversalAndOversizedArchives(t *testing.T) {
	traversal := pluginPreviewZip(t, map[string]string{
		"../plugin.yaml": minimalPluginManifest("tokenhub.preview.traversal", "Traversal", "1.0.0"),
	})
	if _, err := InspectInstallZipArchive(traversal); err == nil {
		t.Fatal("InspectInstallZipArchive accepted traversal archive")
	}
	oversized := make([]byte, maxInstallArchiveBytes+1)
	if _, err := InspectInstallZipArchive(oversized); err == nil {
		t.Fatal("InspectInstallZipArchive accepted oversized archive")
	}
}

func TestInspectInstallZipArchiveRejectsZipSlipBackslashPath(t *testing.T) {
	archive := pluginPreviewZip(t, map[string]string{
		"..\\plugin.yaml": minimalPluginManifest("tokenhub.preview.backslash", "Backslash", "1.0.0"),
	})
	if _, err := InspectInstallZipArchive(archive); err == nil {
		t.Fatal("InspectInstallZipArchive accepted backslash traversal archive")
	}
}

func pluginPreviewZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
