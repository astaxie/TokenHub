package plugin

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxInstallArchiveBytes      = 64 << 20
	maxInstallUncompressedBytes = 256 << 20
	maxInstallArchiveFiles      = 4096
)

var (
	ErrInstallChecksumMismatch = errors.New("plugin package checksum mismatch")
	ErrInstallPackageExists    = errors.New("plugin package already exists")
)

type InstallOptions struct {
	ChecksumSHA256    string
	TrustPolicy       PluginTrustPolicy
	SignatureURL      string
	SignatureKeyID    string
	SignatureVerified bool
	Replace           bool
	PreserveRollback  bool
	InitialState      PackageState
}

func (r Runtime) InstallZipArchive(archive []byte, options InstallOptions) (Package, error) {
	if len(archive) == 0 {
		return Package{}, fmt.Errorf("plugin package archive is empty")
	}
	if len(archive) > maxInstallArchiveBytes {
		return Package{}, fmt.Errorf("plugin package archive exceeds %d bytes", maxInstallArchiveBytes)
	}
	if _, err := ValidateInstallTrust(archive, options); err != nil {
		return Package{}, err
	}
	root, err := r.prepareInstallRoot()
	if err != nil {
		return Package{}, err
	}
	staging, err := os.MkdirTemp(root, ".install-*")
	if err != nil {
		return Package{}, err
	}
	defer os.RemoveAll(staging)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return Package{}, err
	}
	if err := extractZipArchive(reader, staging); err != nil {
		return Package{}, err
	}
	packageDir, manifest, err := stagedPackageDir(staging)
	if err != nil {
		return Package{}, err
	}
	state, err := NormalizePackageState(options.InitialState)
	if err != nil {
		return Package{}, err
	}
	if err := writePackageState(packageDir, state); err != nil {
		return Package{}, err
	}
	target := filepath.Join(root, packageDirName(manifest.ID))
	rollbackBackupDir := ""
	if options.PreserveRollback {
		rollbackBackupDir = rollbackPackageDir(root, manifest.ID)
	}
	if err := replacePackageDir(packageDir, target, options.Replace, rollbackBackupDir); err != nil {
		return Package{}, err
	}
	return readPackage(target)
}

func (r Runtime) PreserveRollbackPackage(pluginID string, sourceDir string) error {
	pluginID = strings.TrimSpace(pluginID)
	sourceDir = strings.TrimSpace(sourceDir)
	if pluginID == "" || sourceDir == "" {
		return ErrPackageNotFound
	}
	root, err := r.prepareInstallRoot()
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	sourceAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}
	if sourceAbs == rootAbs || !strings.HasPrefix(sourceAbs, rootAbs+string(os.PathSeparator)) {
		return fmt.Errorf("plugin package rollback source must be inside plugin directory")
	}
	manifest, err := readManifestOnly(sourceAbs)
	if err != nil {
		return err
	}
	if manifest.ID != pluginID {
		return ErrPackageNotFound
	}
	target := rollbackPackageDir(root, pluginID)
	_ = os.RemoveAll(target)
	if err := copyPluginPackageDir(sourceAbs, target); err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	return nil
}

func (r Runtime) prepareInstallRoot() (string, error) {
	if strings.TrimSpace(r.Dir) == "" {
		return "", fmt.Errorf("plugin directory is required")
	}
	root, err := filepath.Abs(r.Dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("plugin directory %s is not a directory", root)
	}
	return root, nil
}

func verifyArchiveChecksum(archive []byte, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
	}
	if !isHexSHA256(expected) {
		return fmt.Errorf("plugin package checksum must be a lowercase SHA-256 hex digest")
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != expected {
		return ErrInstallChecksumMismatch
	}
	return nil
}

func extractZipArchive(reader *zip.Reader, root string) error {
	if len(reader.File) == 0 {
		return fmt.Errorf("plugin package archive is empty")
	}
	if len(reader.File) > maxInstallArchiveFiles {
		return fmt.Errorf("plugin package archive contains too many files")
	}
	var extracted uint64
	for _, entry := range reader.File {
		clean, err := safeZipEntryPath(entry.Name)
		if err != nil {
			return err
		}
		mode := entry.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin package entry %s must not be a symlink", entry.Name)
		}
		target := filepath.Join(root, clean)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			return fmt.Errorf("plugin package entry %s must be a regular file", entry.Name)
		}
		extracted += entry.UncompressedSize64
		if extracted > maxInstallUncompressedBytes {
			return fmt.Errorf("plugin package archive expands beyond %d bytes", maxInstallUncompressedBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(entry, target); err != nil {
			return err
		}
	}
	return nil
}

func safeZipEntryPath(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return "", fmt.Errorf("plugin package entry path is empty")
	}
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("plugin package entry %s escapes the plugin directory", name)
	}
	return clean, nil
}

func extractZipFile(entry *zip.File, target string) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	mode := entry.FileInfo().Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, source)
	return err
}

func stagedPackageDir(staging string) (string, Manifest, error) {
	manifestPath := filepath.Join(staging, "plugin.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		manifest, err := readManifestOnly(staging)
		if err != nil {
			return "", Manifest{}, err
		}
		return staging, manifest, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", Manifest{}, err
	}
	entries, readErr := os.ReadDir(staging)
	if readErr != nil {
		return "", Manifest{}, readErr
	}
	var foundDir string
	var foundManifest Manifest
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(staging, entry.Name())
		manifest, err := readManifestOnly(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", Manifest{}, err
		}
		if foundDir != "" {
			return "", Manifest{}, fmt.Errorf("plugin package archive must contain exactly one plugin manifest")
		}
		foundDir = dir
		foundManifest = manifest
	}
	if foundDir == "" {
		return "", Manifest{}, fmt.Errorf("plugin package archive does not contain plugin.yaml")
	}
	return foundDir, foundManifest, nil
}

func packageDirName(pluginID string) string {
	var builder strings.Builder
	for _, char := range strings.TrimSpace(pluginID) {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_' || char == '.':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	name := strings.Trim(builder.String(), ".-")
	if name == "" {
		return "plugin"
	}
	return name
}

func replacePackageDir(source string, target string, replace bool, rollbackBackupDir string) error {
	if _, err := os.Stat(target); err == nil {
		if !replace {
			return ErrInstallPackageExists
		}
		backup := target + ".previous"
		_ = os.RemoveAll(backup)
		if err := os.Rename(target, backup); err != nil {
			return err
		}
		if err := os.Rename(source, target); err != nil {
			_ = os.Rename(backup, target)
			return err
		}
		if strings.TrimSpace(rollbackBackupDir) == "" {
			return os.RemoveAll(backup)
		}
		if err := replaceRollbackBackupDir(backup, rollbackBackupDir); err != nil {
			return err
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, target)
}

func replaceRollbackBackupDir(source string, target string) error {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(target)
	return os.Rename(source, target)
}

func copyPluginPackageDir(source string, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("plugin package rollback source is not a directory")
	}
	if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		clean, err := safeZipEntryPath(rel)
		if err != nil {
			return err
		}
		dst := filepath.Join(target, clean)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(dst, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin package rollback entry %s must be a regular file", rel)
		}
		return copyPluginPackageFile(path, dst, info.Mode().Perm())
	})
}

func copyPluginPackageFile(source string, target string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}
