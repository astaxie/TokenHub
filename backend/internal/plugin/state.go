package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const packageStateFileName = "plugin.state.json"

var ErrPackageNotFound = errors.New("plugin package not found")

type PackageState struct {
	Status Status `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func readPackageState(dir string) (PackageState, error) {
	data, err := os.ReadFile(filepath.Join(dir, packageStateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return PackageState{Status: StatusEnabled}, nil
		}
		return PackageState{}, err
	}
	var state PackageState
	if err := json.Unmarshal(data, &state); err != nil {
		return PackageState{}, err
	}
	return NormalizePackageState(state)
}

func NormalizePackageState(state PackageState) (PackageState, error) {
	state.Status = Status(strings.TrimSpace(string(state.Status)))
	state.Reason = strings.TrimSpace(state.Reason)
	if state.Status == "" {
		state.Status = StatusEnabled
	}
	if state.Status != StatusEnabled && state.Status != StatusDisabled {
		return PackageState{}, fmt.Errorf("unsupported plugin package status %q", state.Status)
	}
	return state, nil
}

func (s PackageState) Enabled() bool {
	return s.Status != StatusDisabled
}

func (r Runtime) UpdatePackageState(pluginID string, state PackageState) (Package, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return Package{}, ErrPackageNotFound
	}
	state, err := NormalizePackageState(state)
	if err != nil {
		return Package{}, err
	}
	dirs, err := r.manifestPackageDirs()
	if err != nil {
		return Package{}, err
	}
	for _, dir := range dirs {
		manifest, err := readManifestOnly(dir)
		if err != nil {
			return Package{}, err
		}
		if manifest.ID != pluginID {
			continue
		}
		if err := writePackageState(dir, state); err != nil {
			return Package{}, err
		}
		return Package{Dir: dir, Manifest: manifest, State: state}, nil
	}
	return Package{}, ErrPackageNotFound
}

func (r Runtime) manifestPackageDirs() ([]string, error) {
	if r.Dir == "" {
		return nil, nil
	}
	root, err := filepath.Abs(r.Dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("plugin directory %s is not a directory", root)
	}
	manifestPath := filepath.Join(root, "plugin.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		return []string{root}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "plugin.yaml")); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

func readManifestOnly(dir string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, "plugin.yaml"), err)
	}
	return manifest, nil
}

func writePackageState(dir string, state PackageState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, packageStateFileName), data, 0o644)
}
