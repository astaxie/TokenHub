package server

import (
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) providerCatalogEntriesWithPlugins(entries []ProviderCatalogEntry) ([]ProviderCatalogEntry, bool) {
	pluginEntries := s.pluginProviderCatalogEntries()
	if len(pluginEntries) == 0 {
		return entries, false
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.ID != "" {
			seen[entry.ID] = struct{}{}
		}
	}
	merged := append([]ProviderCatalogEntry(nil), entries...)
	for _, entry := range pluginEntries {
		if _, ok := seen[entry.ID]; ok {
			continue
		}
		seen[entry.ID] = struct{}{}
		merged = append(merged, entry)
	}
	sortCatalogEntries(merged)
	return merged, len(merged) != len(entries)
}

func (s *Server) pluginProviderCatalogEntry(id string) (ProviderCatalogEntry, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ProviderCatalogEntry{}, false
	}
	for _, entry := range s.pluginProviderCatalogEntries() {
		if entry.ID == id {
			return entry, true
		}
	}
	return ProviderCatalogEntry{}, false
}

func (s *Server) pluginProviderCatalogEntries() []ProviderCatalogEntry {
	if s == nil || s.pluginRegistry == nil || s.adapterRegistry == nil {
		return nil
	}
	entries := []ProviderCatalogEntry{}
	for _, adapter := range s.adapterRegistry.List() {
		if adapter.PluginID == "" {
			continue
		}
		plugin, ok := s.pluginRegistry.Describe(adapter.PluginID)
		if !ok || plugin.Source == pluginmeta.SourceBuiltIn {
			continue
		}
		entries = append(entries, providerCatalogEntryFromPlugin(plugin, adapter))
	}
	sortCatalogEntries(entries)
	return entries
}

func providerCatalogEntryFromPlugin(plugin pluginmeta.Descriptor, adapter AdapterDescriptor) ProviderCatalogEntry {
	name := firstNonEmpty(strings.TrimSpace(plugin.Name), adapter.Type)
	return ProviderCatalogEntry{
		ID:          adapter.Type,
		Name:        name,
		DisplayName: name,
		Type:        adapter.Type,
		Source:      "plugin:" + string(plugin.Source),
	}
}
