package server

import (
	"errors"
	"path/filepath"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func builtInProviderPackageRuntime(catalogFile string) pluginmeta.Runtime {
	catalogFile = strings.TrimSpace(catalogFile)
	if catalogFile == "" {
		catalogFile = defaultProviderCatalogFile()
	}
	absolute, err := filepath.Abs(catalogFile)
	if err != nil {
		return pluginmeta.Runtime{}
	}
	return pluginmeta.NewRuntime(filepath.Join(filepath.Dir(absolute), "builtin-plugins", "providers"))
}

func builtInPackageRuntimes(catalogFile string) []pluginmeta.Runtime {
	providerRuntime := builtInProviderPackageRuntime(catalogFile)
	root := filepath.Dir(providerRuntime.Dir)
	return []pluginmeta.Runtime{
		providerRuntime,
		pluginmeta.NewRuntime(filepath.Join(root, "platform")),
	}
}

func inspectBuiltInPluginPackage(catalogFile string, pluginID string) (pluginmeta.PackageInspection, error) {
	for _, runtime := range builtInPackageRuntimes(catalogFile) {
		inspection, err := runtime.InspectPackage(pluginID)
		if err == nil || !errors.Is(err, pluginmeta.ErrPackageNotFound) {
			return inspection, err
		}
	}
	return pluginmeta.PackageInspection{}, pluginmeta.ErrPackageNotFound
}

func readBuiltInPluginPackageFile(catalogFile string, pluginID string, path string) (pluginmeta.PackageFileContent, error) {
	for _, runtime := range builtInPackageRuntimes(catalogFile) {
		content, err := runtime.ReadPackageFile(pluginID, path)
		if err == nil || !errors.Is(err, pluginmeta.ErrPackageNotFound) {
			return content, err
		}
	}
	return pluginmeta.PackageFileContent{}, pluginmeta.ErrPackageNotFound
}

type pluginUsageFacts struct {
	configured bool
	inUse      bool
}

func (s *Server) providerPluginUsageFacts() map[string]pluginUsageFacts {
	facts := map[string]pluginUsageFacts{}
	if s == nil || s.store == nil || s.pluginRegistry == nil {
		return facts
	}
	pluginIDByCatalogID := map[string]string{}
	for _, descriptor := range s.pluginRegistry.List() {
		for _, entry := range providerCatalogEntriesFromPluginCapabilities(descriptor) {
			if entry.ID != "" {
				pluginIDByCatalogID[entry.ID] = descriptor.ID
			}
		}
	}
	activeRoutes := map[string]bool{}
	for _, route := range s.store.ListRoutes() {
		if route.Status == StatusActive {
			activeRoutes[route.ProviderID] = true
		}
	}
	for _, provider := range s.store.ListProviders() {
		pluginID := pluginIDByCatalogID[strings.TrimSpace(provider.Options["catalog_id"])]
		if pluginID == "" && s.adapterRegistry != nil {
			if adapter, ok := s.adapterRegistry.Describe(provider.Type); ok {
				pluginID = adapter.PluginID
			}
		}
		if pluginID == "" {
			continue
		}
		usage := facts[pluginID]
		usage.configured = true
		usage.inUse = usage.inUse || (provider.Status == StatusActive && activeRoutes[provider.ID])
		facts[pluginID] = usage
	}
	return facts
}

func pluginDescriptorHasSettings(descriptor pluginmeta.Descriptor) bool {
	for _, capability := range descriptor.Capabilities {
		if capability.Kind == pluginmeta.CapabilityKindSIM && capability.Name == pluginmeta.SIMCapabilityThemeTokens {
			return true
		}
	}
	return false
}

func pluginDescriptorRequiresConfiguration(descriptor pluginmeta.Descriptor) bool {
	for _, kind := range descriptor.Kinds {
		if kind == pluginmeta.KindProvider {
			return true
		}
	}
	return false
}

func pluginDescriptorSummary(descriptor pluginmeta.Descriptor) string {
	if summary := strings.TrimSpace(descriptor.Summary); summary != "" {
		return summary
	}
	if descriptor.Marketplace != nil {
		if summary := strings.TrimSpace(descriptor.Marketplace.Summary); summary != "" {
			return summary
		}
	}
	return strings.TrimSpace(descriptor.Description)
}

func (s *Server) reloadedInstalledPluginPackage(pluginID string) (pluginmeta.Package, error) {
	pkg, found, err := pluginmeta.NewRuntime(s.config.PluginDir).DescribeInstalledPackage(pluginID)
	if err != nil {
		return pluginmeta.Package{}, err
	}
	if !found {
		return pluginmeta.Package{}, pluginmeta.ErrPackageNotFound
	}
	return pkg, nil
}
