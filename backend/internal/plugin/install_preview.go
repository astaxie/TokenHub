package plugin

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
)

func InspectInstallZipArchive(archive []byte) (Manifest, error) {
	if len(archive) == 0 {
		return Manifest{}, fmt.Errorf("plugin package archive is empty")
	}
	if len(archive) > maxInstallArchiveBytes {
		return Manifest{}, fmt.Errorf("plugin package archive exceeds %d bytes", maxInstallArchiveBytes)
	}
	staging, err := os.MkdirTemp("", "tokenhub-plugin-preview-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(staging)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return Manifest{}, err
	}
	if err := extractZipArchive(reader, staging); err != nil {
		return Manifest{}, err
	}
	_, manifest, err := stagedPackageDir(staging)
	return manifest, err
}
