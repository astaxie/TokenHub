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
var ErrPackageRollbackUnavailable = errors.New("plugin package rollback is unavailable")

type PackageHealthStatus string

const (
	PackageHealthUnknown   PackageHealthStatus = "unknown"
	PackageHealthHealthy   PackageHealthStatus = "healthy"
	PackageHealthUnhealthy PackageHealthStatus = "unhealthy"
)

type PackageLifecycleEvent string

const (
	PackageLifecycleInstalled         PackageLifecycleEvent = "installed"
	PackageLifecycleEnabled           PackageLifecycleEvent = "enabled"
	PackageLifecycleDisabled          PackageLifecycleEvent = "disabled"
	PackageLifecyclePendingRestart    PackageLifecycleEvent = "pending_restart"
	PackageLifecycleValidationFailed  PackageLifecycleEvent = "validation_failed"
	PackageLifecycleRollbackAvailable PackageLifecycleEvent = "rollback_available"
	PackageLifecycleRollbackStarted   PackageLifecycleEvent = "rollback_started"
	PackageLifecycleStartupFailed     PackageLifecycleEvent = "startup_failed"
)

type PackageRollbackTarget string

const (
	PackageRollbackTargetPreviousPackage PackageRollbackTarget = "previous_package"
	PackageRollbackTargetBuiltIn         PackageRollbackTarget = "built_in"
)

type PackageState struct {
	Status          Status                `json:"status,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	RestartRequired bool                  `json:"restart_required,omitempty"`
	Health          PackageHealthStatus   `json:"health,omitempty"`
	Mandatory       bool                  `json:"mandatory,omitempty"`
	RollbackVersion string                `json:"rollback_version,omitempty"`
	RollbackTarget  PackageRollbackTarget `json:"rollback_target,omitempty"`
	LastErrorCode   string                `json:"last_error_code,omitempty"`
	AuditEvent      PackageLifecycleEvent `json:"audit_event,omitempty"`
}

func readPackageState(dir string) (PackageState, error) {
	data, err := os.ReadFile(filepath.Join(dir, packageStateFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return NormalizePackageState(PackageState{Status: StatusEnabled})
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
	state.Health = PackageHealthStatus(strings.TrimSpace(string(state.Health)))
	state.RollbackVersion = strings.TrimSpace(state.RollbackVersion)
	state.RollbackTarget = PackageRollbackTarget(strings.TrimSpace(string(state.RollbackTarget)))
	state.LastErrorCode = strings.TrimSpace(state.LastErrorCode)
	state.AuditEvent = PackageLifecycleEvent(strings.TrimSpace(string(state.AuditEvent)))
	if state.Status == "" {
		state.Status = StatusEnabled
	}
	if state.Health == "" {
		state.Health = PackageHealthUnknown
	}
	if !validPackageStatus(state.Status) {
		return PackageState{}, fmt.Errorf("unsupported plugin package status %q", state.Status)
	}
	if !validPackageHealthStatus(state.Health) {
		return PackageState{}, fmt.Errorf("unsupported plugin package health %q", state.Health)
	}
	if state.AuditEvent != "" && !validPackageLifecycleEvent(state.AuditEvent) {
		return PackageState{}, fmt.Errorf("unsupported plugin package audit event %q", state.AuditEvent)
	}
	if state.RollbackTarget != "" && !validPackageRollbackTarget(state.RollbackTarget) {
		return PackageState{}, fmt.Errorf("unsupported plugin package rollback target %q", state.RollbackTarget)
	}
	if state.Status == StatusPendingRestart {
		state.RestartRequired = true
	}
	if state.Status == StatusMandatory {
		state.Mandatory = true
	}
	if state.Mandatory && state.Status == StatusDisabled {
		return PackageState{}, fmt.Errorf("mandatory plugin package cannot be disabled")
	}
	if state.Status == StatusRollbackAvailable && state.RollbackVersion == "" {
		return PackageState{}, fmt.Errorf("plugin package rollback_version is required when rollback is available")
	}
	if state.RollbackVersion != "" && state.RollbackTarget == "" {
		state.RollbackTarget = PackageRollbackTargetPreviousPackage
	}
	if state.RollbackTarget == PackageRollbackTargetPreviousPackage && state.RollbackVersion == "" {
		return PackageState{}, fmt.Errorf("plugin package rollback_version is required for previous package rollback")
	}
	return state, nil
}

func (s PackageState) Enabled() bool {
	return s.Loadable()
}

func (s PackageState) Loadable() bool {
	switch s.Status {
	case StatusEnabled, StatusRollbackAvailable, StatusMandatory:
		return true
	default:
		return false
	}
}

func (s PackageState) PendingRestart() bool {
	return s.Status == StatusPendingRestart || s.RestartRequired
}

func (s PackageState) RollbackAvailable() bool {
	return strings.TrimSpace(s.RollbackVersion) != ""
}

func (s PackageState) FailedValidation() bool {
	return s.Status == StatusFailedValidation
}

func (s PackageState) FailedStartup() bool {
	return s.Status == StatusFailedStartup
}

func (s PackageState) BuiltInFallbackAvailable() bool {
	return s.RollbackTarget == PackageRollbackTargetBuiltIn
}

func validPackageStatus(status Status) bool {
	switch status {
	case StatusEnabled, StatusDisabled, StatusPendingRestart, StatusFailedValidation, StatusFailedStartup, StatusRollbackAvailable, StatusMandatory:
		return true
	default:
		return false
	}
}

func validPackageRollbackTarget(target PackageRollbackTarget) bool {
	switch target {
	case PackageRollbackTargetPreviousPackage, PackageRollbackTargetBuiltIn:
		return true
	default:
		return false
	}
}

func validPackageHealthStatus(status PackageHealthStatus) bool {
	switch status {
	case PackageHealthUnknown, PackageHealthHealthy, PackageHealthUnhealthy:
		return true
	default:
		return false
	}
}

func validPackageLifecycleEvent(event PackageLifecycleEvent) bool {
	switch event {
	case PackageLifecycleInstalled,
		PackageLifecycleEnabled,
		PackageLifecycleDisabled,
		PackageLifecyclePendingRestart,
		PackageLifecycleValidationFailed,
		PackageLifecycleRollbackAvailable,
		PackageLifecycleRollbackStarted,
		PackageLifecycleStartupFailed:
		return true
	default:
		return false
	}
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

func (r Runtime) UninstallPackage(pluginID string) (Package, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return Package{}, ErrPackageNotFound
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
		state, err := readPackageState(dir)
		if err != nil {
			return Package{}, err
		}
		if err := os.RemoveAll(dir); err != nil {
			return Package{}, err
		}
		return Package{Dir: dir, Manifest: manifest, State: state}, nil
	}
	return Package{}, ErrPackageNotFound
}

func (r Runtime) RollbackPackage(pluginID string, reason string) (Package, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return Package{}, ErrPackageNotFound
	}
	current, found, err := r.DescribeInstalledPackage(pluginID)
	if err != nil {
		return Package{}, err
	}
	if !found {
		return Package{}, ErrPackageNotFound
	}
	if !current.State.RollbackAvailable() {
		return Package{}, ErrPackageRollbackUnavailable
	}
	root, err := r.prepareInstallRoot()
	if err != nil {
		return Package{}, err
	}
	rollbackDir := rollbackPackageDir(root, pluginID)
	if _, err := os.Stat(filepath.Join(rollbackDir, "plugin.yaml")); err != nil {
		if os.IsNotExist(err) {
			return Package{}, ErrPackageRollbackUnavailable
		}
		return Package{}, err
	}
	target := filepath.Join(root, packageDirName(pluginID))
	replacedDir := filepath.Join(root, ".rollback", packageDirName(pluginID)+".replaced")
	_ = os.RemoveAll(replacedDir)
	if err := os.MkdirAll(filepath.Dir(replacedDir), 0o755); err != nil {
		return Package{}, err
	}
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, replacedDir); err != nil {
			return Package{}, err
		}
	} else if !os.IsNotExist(err) {
		return Package{}, err
	}
	if err := os.Rename(rollbackDir, target); err != nil {
		if _, restoreErr := os.Stat(replacedDir); restoreErr == nil {
			_ = os.Rename(replacedDir, target)
		}
		return Package{}, err
	}
	pkg, err := readPackage(target)
	if err != nil {
		return Package{}, err
	}
	state := pkg.State
	state.Status = current.State.Status
	if state.Status == StatusPendingRestart || state.Status == StatusFailedValidation || state.Status == StatusFailedStartup {
		state.Status = StatusDisabled
	}
	state.Reason = strings.TrimSpace(reason)
	state.RestartRequired = true
	state.Health = PackageHealthUnknown
	state.RollbackVersion = ""
	state.RollbackTarget = ""
	state.LastErrorCode = ""
	state.AuditEvent = PackageLifecycleRollbackStarted
	state, err = NormalizePackageState(state)
	if err != nil {
		return Package{}, err
	}
	if err := writePackageState(target, state); err != nil {
		return Package{}, err
	}
	pkg.State = state
	return pkg, nil
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

func rollbackPackageDir(root string, pluginID string) string {
	return filepath.Join(root, ".rollback", packageDirName(pluginID))
}
