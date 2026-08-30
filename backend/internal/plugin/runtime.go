package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Package struct {
	Dir      string
	Manifest Manifest
	State    PackageState
	AdminUI  AdminUIManifest
}

type Runtime struct {
	Dir string
}

func NewRuntime(dir string) Runtime {
	return Runtime{Dir: strings.TrimSpace(dir)}
}

func (r Runtime) LoadInto(plugins *Registry, chain *GatewayChainRegistry, adminUIRegistries ...*AdminUIRegistry) ([]Package, error) {
	var adminUI *AdminUIRegistry
	if len(adminUIRegistries) > 0 {
		adminUI = adminUIRegistries[0]
	}
	return r.loadInto(plugins, chain, adminUI, nil, nil, nil)
}

func (r Runtime) LoadIntoWithActions(plugins *Registry, chain *GatewayChainRegistry, adminUI *AdminUIRegistry, actions *ActionBroker, hookRunners ...*GatewayHookRunner) ([]Package, error) {
	var hookRunner *GatewayHookRunner
	if len(hookRunners) > 0 {
		hookRunner = hookRunners[0]
	}
	return r.loadInto(plugins, chain, adminUI, actions, nil, hookRunner)
}

func (r Runtime) LoadIntoWithActionsAndBackground(plugins *Registry, chain *GatewayChainRegistry, adminUI *AdminUIRegistry, actions *ActionBroker, backgroundJobs *BackgroundJobBroker, hookRunners ...*GatewayHookRunner) ([]Package, error) {
	var hookRunner *GatewayHookRunner
	if len(hookRunners) > 0 {
		hookRunner = hookRunners[0]
	}
	return r.loadInto(plugins, chain, adminUI, actions, backgroundJobs, hookRunner)
}

func (r Runtime) loadInto(plugins *Registry, chain *GatewayChainRegistry, adminUI *AdminUIRegistry, actions *ActionBroker, backgroundJobs *BackgroundJobBroker, hookRunner *GatewayHookRunner) ([]Package, error) {
	dirs, err := r.packageDirs()
	if err != nil {
		return nil, err
	}
	targets := runtimeLoadTargets{
		plugins:        plugins,
		chain:          chain,
		adminUI:        adminUI,
		actions:        actions,
		backgroundJobs: backgroundJobs,
		hookRunner:     hookRunner,
	}
	packageDirsByID := map[string]string{}
	packages := make([]Package, 0, len(dirs))
	for _, dir := range dirs {
		candidate, err := readPackageForLoad(dir)
		if err != nil {
			failed, ok, failErr := r.markPackageLoadFailure(dir, targets, StatusFailedValidation, PackageLifecycleValidationFailed, err)
			if failErr != nil {
				return nil, failErr
			}
			if ok {
				packages = append(packages, failed)
			}
			continue
		}
		pkg := candidate.Package
		if previousDir, ok := packageDirsByID[pkg.Manifest.ID]; ok {
			failed, ok, failErr := r.markPackageLoadFailure(pkg.Dir, targets, StatusFailedValidation, PackageLifecycleValidationFailed, fmt.Errorf("duplicate plugin id %s in %s and %s", pkg.Manifest.ID, previousDir, pkg.Dir))
			if failErr != nil {
				return nil, failErr
			}
			if ok {
				packages = append(packages, failed)
			}
			continue
		}
		packageDirsByID[pkg.Manifest.ID] = pkg.Dir
		if !candidate.ManifestValidated && !pkg.State.Enabled() {
			packages = append(packages, pkg)
			continue
		}
		staged := targets.clone()
		if err := staged.activatePackage(pkg); err != nil {
			failed, ok, failErr := r.markPackageLoadFailure(pkg.Dir, targets, StatusFailedStartup, PackageLifecycleStartupFailed, err)
			if failErr != nil {
				return nil, failErr
			}
			if ok {
				packages = append(packages, failed)
			}
			continue
		}
		targets.commitFrom(staged)
		packages = append(packages, pkg)
	}
	return packages, nil
}

type packageLoadCandidate struct {
	Package
	ManifestValidated bool
}

type runtimeLoadTargets struct {
	plugins        *Registry
	chain          *GatewayChainRegistry
	adminUI        *AdminUIRegistry
	actions        *ActionBroker
	backgroundJobs *BackgroundJobBroker
	hookRunner     *GatewayHookRunner
}

func (t runtimeLoadTargets) clone() runtimeLoadTargets {
	chain := cloneGatewayChainRegistry(t.chain)
	return runtimeLoadTargets{
		plugins:        clonePluginRegistry(t.plugins),
		chain:          chain,
		adminUI:        cloneAdminUIRegistry(t.adminUI),
		actions:        cloneActionBroker(t.actions),
		backgroundJobs: cloneBackgroundJobBroker(t.backgroundJobs),
		hookRunner:     cloneGatewayHookRunner(t.hookRunner, chain),
	}
}

func (t runtimeLoadTargets) commitFrom(staged runtimeLoadTargets) {
	if t.plugins != nil && staged.plugins != nil {
		t.plugins.plugins = staged.plugins.plugins
	}
	if t.chain != nil && staged.chain != nil {
		t.chain.hooks = staged.chain.hooks
	}
	if t.adminUI != nil && staged.adminUI != nil {
		t.adminUI.contributions = staged.adminUI.contributions
	}
	if t.actions != nil && staged.actions != nil {
		t.actions.actions = staged.actions.actions
	}
	if t.backgroundJobs != nil && staged.backgroundJobs != nil {
		t.backgroundJobs.jobs = staged.backgroundJobs.jobs
	}
	if t.hookRunner != nil && staged.hookRunner != nil {
		t.hookRunner.handlers = staged.hookRunner.handlers
	}
}

func (t runtimeLoadTargets) activatePackage(pkg Package) error {
	descriptor := descriptorWithAdminUIContributions(pkg.Manifest.Descriptor(), pkg.AdminUI)
	descriptor.Status = pkg.State.Status
	if err := t.plugins.Register(descriptor); err != nil {
		return fmt.Errorf("register plugin %s: %w", pkg.Manifest.ID, err)
	}
	if !pkg.State.Enabled() {
		return nil
	}
	hookHandler := gatewayHookHandlerForPackage(pkg)
	for _, hook := range pkg.Manifest.GatewayHooks() {
		if err := t.chain.RegisterHook(hook); err != nil {
			return fmt.Errorf("register gateway hook %s from plugin %s: %w", hook.HookID, pkg.Manifest.ID, err)
		}
		if t.hookRunner != nil && hookHandler != nil {
			if err := t.hookRunner.RegisterHandler(hook, hookHandler); err != nil {
				return fmt.Errorf("register gateway hook handler %s from plugin %s: %w", hook.HookID, pkg.Manifest.ID, err)
			}
		}
	}
	if t.actions != nil {
		for _, action := range pkg.Manifest.Actions() {
			handler := actionHandlerForPackage(pkg)
			var err error
			if handler == nil {
				err = t.actions.RegisterDescriptor(action)
			} else {
				err = t.actions.Register(action, handler)
			}
			if err != nil {
				return fmt.Errorf("register action %s from plugin %s: %w", action.ActionID, pkg.Manifest.ID, err)
			}
		}
	}
	if t.adminUI != nil {
		if t.actions != nil {
			if err := t.adminUI.RegisterManifestWithActionResolver(pkg.AdminUI, t.actions); err != nil {
				return fmt.Errorf("register admin UI contributions from plugin %s: %w", pkg.Manifest.ID, err)
			}
		} else if err := t.adminUI.RegisterManifest(pkg.AdminUI); err != nil {
			return fmt.Errorf("register admin UI contributions from plugin %s: %w", pkg.Manifest.ID, err)
		}
	}
	if t.backgroundJobs != nil {
		for _, job := range pkg.Manifest.BackgroundJobs() {
			handler := backgroundJobHandlerForPackage(pkg)
			var err error
			if handler == nil {
				err = t.backgroundJobs.RegisterDescriptor(job)
			} else {
				err = t.backgroundJobs.Register(job, handler)
			}
			if err != nil {
				return fmt.Errorf("register background job %s from plugin %s: %w", job.JobID, pkg.Manifest.ID, err)
			}
		}
	}
	return nil
}

func (t runtimeLoadTargets) rollbackTargetForPackage(manifest Manifest, state PackageState) PackageRollbackTarget {
	if strings.TrimSpace(state.RollbackVersion) != "" {
		return PackageRollbackTargetPreviousPackage
	}
	if strings.TrimSpace(manifest.ID) == "" || t.plugins == nil {
		return ""
	}
	descriptor, ok := t.plugins.Describe(manifest.ID)
	if ok && descriptor.Source == SourceBuiltIn {
		return PackageRollbackTargetBuiltIn
	}
	return ""
}

func clonePluginRegistry(source *Registry) *Registry {
	if source == nil {
		return nil
	}
	clone := NewRegistry()
	for key, descriptor := range source.plugins {
		clone.plugins[key] = descriptor
	}
	return clone
}

func cloneGatewayChainRegistry(source *GatewayChainRegistry) *GatewayChainRegistry {
	if source == nil {
		return nil
	}
	clone := NewGatewayChainRegistry()
	for stage, hooks := range source.hooks {
		clone.hooks[stage] = append([]GatewayHookDescriptor(nil), hooks...)
	}
	return clone
}

func cloneAdminUIRegistry(source *AdminUIRegistry) *AdminUIRegistry {
	if source == nil {
		return nil
	}
	clone := NewAdminUIRegistry()
	for key, contribution := range source.contributions {
		clone.contributions[key] = contribution
	}
	return clone
}

func cloneActionBroker(source *ActionBroker) *ActionBroker {
	if source == nil {
		return nil
	}
	clone := NewActionBroker()
	for key, entry := range source.actions {
		clone.actions[key] = entry
	}
	return clone
}

func cloneBackgroundJobBroker(source *BackgroundJobBroker) *BackgroundJobBroker {
	if source == nil {
		return nil
	}
	clone := NewBackgroundJobBroker()
	for key, entry := range source.jobs {
		clone.jobs[key] = entry
	}
	return clone
}

func cloneGatewayHookRunner(source *GatewayHookRunner, chain *GatewayChainRegistry) *GatewayHookRunner {
	if source == nil {
		return nil
	}
	clone := NewGatewayHookRunner(chain)
	for key, handler := range source.handlers {
		clone.handlers[key] = handler
	}
	return clone
}

func (r Runtime) DescribeInstalledPackage(pluginID string) (Package, bool, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return Package{}, false, nil
	}
	packages, err := r.Discover()
	if err != nil {
		return Package{}, false, err
	}
	for _, pkg := range packages {
		if pkg.Manifest.ID == pluginID {
			return pkg, true, nil
		}
	}
	return Package{}, false, nil
}

func (r Runtime) packageDirs() ([]string, error) {
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

func readPackageForLoad(dir string) (packageLoadCandidate, error) {
	state, err := readPackageState(dir)
	if err != nil {
		return packageLoadCandidate{}, fmt.Errorf("read %s: %w", filepath.Join(dir, packageStateFileName), err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return packageLoadCandidate{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		if state.Loadable() {
			return packageLoadCandidate{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, "plugin.yaml"), err)
		}
		manifest, rawErr := parseManifestDocument(data)
		if rawErr != nil {
			return packageLoadCandidate{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, "plugin.yaml"), err)
		}
		return packageLoadCandidate{
			Package: Package{
				Dir:      dir,
				Manifest: manifest,
				State:    state,
			},
		}, nil
	}
	if !state.Enabled() {
		return packageLoadCandidate{
			Package: Package{
				Dir:      dir,
				Manifest: manifest,
				State:    state,
			},
			ManifestValidated: true,
		}, nil
	}
	adminUI, err := readAdminUIManifest(dir, manifest)
	if err != nil {
		return packageLoadCandidate{}, err
	}
	return packageLoadCandidate{
		Package: Package{
			Dir:      dir,
			Manifest: manifest,
			State:    state,
			AdminUI:  adminUI,
		},
		ManifestValidated: true,
	}, nil
}

func (r Runtime) markPackageLoadFailure(dir string, targets runtimeLoadTargets, status Status, event PackageLifecycleEvent, cause error) (Package, bool, error) {
	data, readErr := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if readErr != nil {
		return Package{}, false, readErr
	}
	manifest, rawErr := parseManifestDocument(data)
	if rawErr != nil {
		return Package{}, false, nil
	}
	state, stateErr := readPackageState(dir)
	if stateErr != nil {
		state = PackageState{}
	}
	rollbackTarget := targets.rollbackTargetForPackage(manifest, state)
	state.Status = status
	state.Reason = strings.TrimSpace(cause.Error())
	state.RestartRequired = false
	state.Health = PackageHealthUnhealthy
	state.LastErrorCode = packageLoadFailureCode(cause, event)
	state.AuditEvent = event
	if strings.TrimSpace(state.RollbackVersion) != "" {
		state.RollbackTarget = PackageRollbackTargetPreviousPackage
	} else {
		state.RollbackTarget = rollbackTarget
	}
	state, err := NormalizePackageState(state)
	if err != nil {
		return Package{}, false, err
	}
	if err := writePackageState(dir, state); err != nil {
		return Package{}, false, err
	}
	return Package{
		Dir:      dir,
		Manifest: manifest,
		State:    state,
	}, strings.TrimSpace(manifest.ID) != "", nil
}

func packageLoadFailureCode(cause error, event PackageLifecycleEvent) string {
	if code, ok := PluginErrorCodeOf(cause); ok {
		return string(code)
	}
	switch event {
	case PackageLifecycleValidationFailed:
		return "plugin_validation_failed"
	case PackageLifecycleStartupFailed:
		return "plugin_startup_failed"
	default:
		return "plugin_load_failed"
	}
}

func descriptorWithAdminUIContributions(descriptor Descriptor, adminUI AdminUIManifest) Descriptor {
	for _, contribution := range adminUI.Contributions {
		if strings.TrimSpace(string(contribution.Slot)) == "" || strings.TrimSpace(contribution.ID) == "" {
			continue
		}
		descriptor.Capabilities = append(descriptor.Capabilities, CapabilityDescriptor{
			Kind:    "admin_ui",
			Name:    string(contribution.Slot),
			Subject: contribution.ID,
			Value:   contribution.Action,
		})
	}
	return NormalizeDescriptor(descriptor)
}

func actionHandlerForPackage(pkg Package) ActionHandler {
	if pkg.Manifest.Entry.Backend == nil {
		return nil
	}
	if strings.TrimSpace(pkg.Manifest.Entry.Backend.Protocol) != BackendProtocolStdioJSONV1 {
		return nil
	}
	if strings.TrimSpace(pkg.Manifest.Entry.Backend.Command) == "" {
		return nil
	}
	return NewActionCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command, PermissionGrantFromManifest(pkg.Manifest.Permissions))
}

func gatewayHookHandlerForPackage(pkg Package) GatewayHookHandler {
	if pkg.Manifest.Entry.Backend == nil {
		return nil
	}
	if strings.TrimSpace(pkg.Manifest.Entry.Backend.Protocol) != BackendProtocolStdioJSONV1 {
		return nil
	}
	if strings.TrimSpace(pkg.Manifest.Entry.Backend.Command) == "" {
		return nil
	}
	return NewGatewayCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command, PermissionGrantFromManifest(pkg.Manifest.Permissions))
}

func backgroundJobHandlerForPackage(pkg Package) BackgroundJobHandler {
	if pkg.Manifest.Entry.Backend == nil {
		return nil
	}
	if strings.TrimSpace(pkg.Manifest.Entry.Backend.Protocol) != BackendProtocolStdioJSONV1 {
		return nil
	}
	if strings.TrimSpace(pkg.Manifest.Entry.Backend.Command) == "" {
		return nil
	}
	return NewBackgroundCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command, PermissionGrantFromManifest(pkg.Manifest.Permissions))
}

func (r Runtime) Discover() ([]Package, error) {
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
		pkg, err := readPackage(root)
		if err != nil {
			return nil, err
		}
		return []Package{pkg}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	packages := make([]Package, 0, len(entries))
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
		pkg, err := readPackage(dir)
		if err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func readPackage(dir string) (Package, error) {
	state, err := readPackageState(dir)
	if err != nil {
		return Package{}, fmt.Errorf("read %s: %w", filepath.Join(dir, packageStateFileName), err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return Package{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return Package{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, "plugin.yaml"), err)
	}
	if !state.Enabled() {
		return Package{Dir: dir, Manifest: manifest, State: state}, nil
	}
	adminUI, err := readAdminUIManifest(dir, manifest)
	if err != nil {
		return Package{}, err
	}
	return Package{Dir: dir, Manifest: manifest, State: state, AdminUI: adminUI}, nil
}

func readAdminUIManifest(dir string, manifest Manifest) (AdminUIManifest, error) {
	if manifest.Entry.Frontend == nil || strings.TrimSpace(manifest.Entry.Frontend.Schema) == "" {
		return AdminUIManifest{}, nil
	}
	path, err := packageRelativePath(dir, manifest.Entry.Frontend.Schema)
	if err != nil {
		return AdminUIManifest{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AdminUIManifest{}, err
	}
	adminUI, err := ParseAdminUIManifest(manifest.ID, data)
	if err != nil {
		return AdminUIManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return adminUI, nil
}

func packageRelativePath(root string, relative string) (string, error) {
	relative = strings.TrimSpace(relative)
	if relative == "" {
		return "", fmt.Errorf("plugin package path is required")
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("plugin package path %s must be relative", relative)
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("plugin package path %s escapes the plugin directory", relative)
	}
	return filepath.Join(root, clean), nil
}
