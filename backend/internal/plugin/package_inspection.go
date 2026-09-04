package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const MaxPackageFilePreviewBytes int64 = 256 << 10

var ErrPackageFilePreviewUnavailable = errors.New("plugin package file preview unavailable")

type PackageInspection struct {
	FileCount int                     `json:"file_count"`
	TotalSize int64                   `json:"total_size"`
	Files     []PackageFileInspection `json:"files"`
}

type PackageFileInspection struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Kind     string `json:"kind"`
	Viewable bool   `json:"viewable"`
}

type PackageFileContent struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

func (r Runtime) InspectPackage(pluginID string) (PackageInspection, error) {
	dir, manifest, err := r.packageDirForInspection(pluginID)
	if err != nil {
		return PackageInspection{}, err
	}
	inspection := PackageInspection{}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		file := inspectPackageFile(relative, info.Size(), manifest)
		inspection.Files = append(inspection.Files, file)
		inspection.TotalSize += info.Size()
		if len(inspection.Files) > maxInstallArchiveFiles {
			return fmt.Errorf("plugin package contains more than %d files", maxInstallArchiveFiles)
		}
		return nil
	})
	if err != nil {
		return PackageInspection{}, err
	}
	sort.Slice(inspection.Files, func(i, j int) bool { return inspection.Files[i].Path < inspection.Files[j].Path })
	inspection.FileCount = len(inspection.Files)
	return inspection, nil
}

func (r Runtime) ReadPackageFile(pluginID string, relativePath string) (PackageFileContent, error) {
	inspection, err := r.InspectPackage(pluginID)
	if err != nil {
		return PackageFileContent{}, err
	}
	relativePath = filepath.ToSlash(strings.TrimSpace(relativePath))
	var selected PackageFileInspection
	found := false
	for _, file := range inspection.Files {
		if file.Path == relativePath {
			selected = file
			found = true
			break
		}
	}
	if !found {
		return PackageFileContent{}, os.ErrNotExist
	}
	if !selected.Viewable {
		return PackageFileContent{}, ErrPackageFilePreviewUnavailable
	}
	dir, _, err := r.packageDirForInspection(pluginID)
	if err != nil {
		return PackageFileContent{}, err
	}
	target, err := packageRelativePath(dir, filepath.FromSlash(relativePath))
	if err != nil {
		return PackageFileContent{}, ErrPackageFilePreviewUnavailable
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return PackageFileContent{}, err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return PackageFileContent{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return PackageFileContent{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return PackageFileContent{}, err
	}
	if resolved == root || !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return PackageFileContent{}, ErrPackageFilePreviewUnavailable
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return PackageFileContent{}, err
	}
	if int64(len(data)) > MaxPackageFilePreviewBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return PackageFileContent{}, ErrPackageFilePreviewUnavailable
	}
	return PackageFileContent{Path: selected.Path, Size: selected.Size, Kind: selected.Kind, Content: string(data)}, nil
}

func (r Runtime) packageDirForInspection(pluginID string) (string, Manifest, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return "", Manifest{}, ErrPackageNotFound
	}
	dirs, err := r.packageDirs()
	if err != nil {
		return "", Manifest{}, err
	}
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
		if err != nil {
			return "", Manifest{}, err
		}
		manifest, err := parseManifestDocument(data)
		if err != nil {
			continue
		}
		if strings.TrimSpace(manifest.ID) == pluginID {
			return dir, manifest, nil
		}
	}
	return "", Manifest{}, ErrPackageNotFound
}

func inspectPackageFile(path string, size int64, manifest Manifest) PackageFileInspection {
	kind := packageFileKind(path, manifest)
	return PackageFileInspection{
		Path:     path,
		Size:     size,
		Kind:     kind,
		Viewable: packageFileViewable(path, kind, size),
	}
}

func packageFileKind(path string, manifest Manifest) string {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	switch normalized {
	case "plugin.yaml":
		return "manifest"
	case packageStateFileName:
		return "runtime_state"
	}
	if manifest.Entry.Frontend != nil && normalized == filepath.ToSlash(strings.TrimSpace(manifest.Entry.Frontend.Schema)) {
		return "schema"
	}
	if manifest.Entry.Backend != nil && normalized == filepath.ToSlash(strings.TrimSpace(manifest.Entry.Backend.Command)) {
		return "executable"
	}
	switch strings.ToLower(filepath.Ext(normalized)) {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".rs", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".sh":
		return "source"
	case ".md", ".txt", ".rst":
		return "documentation"
	case ".json", ".yaml", ".yml", ".toml":
		return "configuration"
	case ".css", ".scss", ".html":
		return "presentation"
	default:
		return "asset"
	}
}

func packageFileViewable(path string, kind string, size int64) bool {
	if size < 0 || size > MaxPackageFilePreviewBytes || kind == "runtime_state" || kind == "asset" {
		return false
	}
	for _, part := range strings.Split(strings.ToLower(filepath.ToSlash(path)), "/") {
		if strings.HasPrefix(part, ".") || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.Contains(part, "private") {
			return false
		}
	}
	return true
}
