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
	return r.loadInto(plugins, chain, adminUI, nil)
}

func (r Runtime) LoadIntoWithActions(plugins *Registry, chain *GatewayChainRegistry, adminUI *AdminUIRegistry, actions *ActionBroker) ([]Package, error) {
	return r.loadInto(plugins, chain, adminUI, actions)
}

func (r Runtime) loadInto(plugins *Registry, chain *GatewayChainRegistry, adminUI *AdminUIRegistry, actions *ActionBroker) ([]Package, error) {
	packages, err := r.Discover()
	if err != nil {
		return nil, err
	}
	packageDirsByID := map[string]string{}
	for _, pkg := range packages {
		if previousDir, ok := packageDirsByID[pkg.Manifest.ID]; ok {
			return nil, fmt.Errorf("duplicate plugin id %s in %s and %s", pkg.Manifest.ID, previousDir, pkg.Dir)
		}
		packageDirsByID[pkg.Manifest.ID] = pkg.Dir
		if err := plugins.Register(pkg.Manifest.Descriptor()); err != nil {
			return nil, fmt.Errorf("register plugin %s: %w", pkg.Manifest.ID, err)
		}
		for _, hook := range pkg.Manifest.GatewayHooks() {
			if err := chain.RegisterHook(hook); err != nil {
				return nil, fmt.Errorf("register gateway hook %s from plugin %s: %w", hook.HookID, pkg.Manifest.ID, err)
			}
		}
		if adminUI != nil {
			if err := adminUI.RegisterManifest(pkg.AdminUI); err != nil {
				return nil, fmt.Errorf("register admin UI contributions from plugin %s: %w", pkg.Manifest.ID, err)
			}
		}
		if actions != nil {
			for _, action := range pkg.Manifest.Actions() {
				handler := actionHandlerForPackage(pkg)
				var err error
				if handler == nil {
					err = actions.RegisterDescriptor(action)
				} else {
					err = actions.Register(action, handler)
				}
				if err != nil {
					return nil, fmt.Errorf("register action %s from plugin %s: %w", action.ActionID, pkg.Manifest.ID, err)
				}
			}
		}
	}
	return packages, nil
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
	return NewActionCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command)
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
	data, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		return Package{}, err
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return Package{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, "plugin.yaml"), err)
	}
	adminUI, err := readAdminUIManifest(dir, manifest)
	if err != nil {
		return Package{}, err
	}
	return Package{Dir: dir, Manifest: manifest, AdminUI: adminUI}, nil
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
