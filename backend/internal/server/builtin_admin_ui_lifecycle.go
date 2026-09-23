package server

import (
	"fmt"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinAdminUIContributions(registry *pluginmeta.Registry, adminUI *pluginmeta.AdminUIRegistry, runtime pluginmeta.Runtime) error {
	definitions := pluginmeta.NewRegistry()
	contributions := pluginmeta.NewAdminUIRegistry()
	registerBuiltinAdminUIDefinitions(definitions, contributions)
	for _, descriptor := range definitions.List() {
		// Shared Provider/UI definitions must retain the Provider's activation
		// state instead of re-enabling it when its presentation is registered.
		if existing, ok := registry.Describe(descriptor.ID); ok {
			descriptor.Status = existing.Status
		}
		state, found, err := runtime.ReadBuiltInPackageState(descriptor.ID)
		if err != nil {
			return fmt.Errorf("read plugin %s lifecycle: %w", descriptor.ID, err)
		}
		if found {
			descriptor.Status = state.Status
		}
		if err := registry.Register(descriptor); err != nil {
			return err
		}
	}
	for _, contribution := range contributions.List() {
		descriptor, ok := registry.Describe(contribution.PluginID)
		if !ok || !(pluginmeta.PackageState{Status: descriptor.Status}).Loadable() {
			continue
		}
		if err := adminUI.Register(contribution); err != nil {
			return err
		}
	}
	return nil
}
