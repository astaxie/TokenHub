package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const (
	builtinProviderCatalogPluginPrefix   = "tokenhub.provider-catalog."
	providerCatalogGeneratedPluginSource = "plugin:provider_catalog"
)

var providerCatalogPluginEntriesCache sync.Map

type providerCatalogPluginFileEntry struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Type        string            `json:"type"`
	API         string            `json:"api"`
	BaseURL     string            `json:"base_url"`
	Doc         string            `json:"doc"`
	DocURL      string            `json:"doc_url"`
	Models      []json.RawMessage `json:"models"`
}

func registerBuiltinProviderCatalogFilePlugins(registry *pluginmeta.Registry, adapters *AdapterRegistry, catalogFile string, runtime pluginmeta.Runtime) error {
	if registry == nil {
		return nil
	}
	entries, err := loadProviderCatalogPluginEntries(catalogFile, providerCatalogTypesFromRegistry(adapters), providerCatalogDefaultTypeFromRegistry(adapters))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	existingCatalogIDs := providerCatalogPluginIDs(registry)
	for _, entry := range entries {
		if entry.ID == "custom" {
			continue
		}
		if _, exists := existingCatalogIDs[entry.ID]; exists {
			continue
		}
		if err := registerBuiltinProviderCatalogEntryPlugin(registry, entry, runtime); err != nil {
			return err
		}
	}
	return nil
}

func loadProviderCatalogPluginEntries(catalogFile string, catalogTypes map[string]string, defaultType string) ([]ProviderCatalogEntry, error) {
	catalogFile = strings.TrimSpace(catalogFile)
	if catalogFile == "" {
		catalogFile = defaultProviderCatalogFile()
	}
	if strings.TrimSpace(defaultType) == "" {
		defaultType = defaultProviderCatalogProviderType()
	}
	absolutePath, err := filepath.Abs(catalogFile)
	if err != nil {
		return nil, fmt.Errorf("resolve provider catalog plugin metadata %s: %w", catalogFile, err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("inspect provider catalog plugin metadata %s: %w", absolutePath, err)
	}
	cacheKey := providerCatalogPluginCacheKey(absolutePath, info.Size(), info.ModTime().UnixNano(), catalogTypes, defaultType)
	if cached, ok := providerCatalogPluginEntriesCache.Load(cacheKey); ok {
		return append([]ProviderCatalogEntry(nil), cached.([]ProviderCatalogEntry)...), nil
	}
	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Errorf("read provider catalog plugin metadata %s: %w", absolutePath, err)
	}
	var payload struct {
		Providers map[string]providerCatalogPluginFileEntry `json:"providers"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, fmt.Errorf("parse provider catalog plugin metadata %s: %w", absolutePath, err)
	}
	if len(payload.Providers) == 0 {
		return nil, fmt.Errorf("provider catalog plugin metadata has no providers")
	}
	entries := make([]ProviderCatalogEntry, 0, len(payload.Providers))
	for catalogID, raw := range payload.Providers {
		id := firstNonEmpty(raw.ID, catalogID)
		name := firstNonEmpty(raw.Name, raw.DisplayName, id)
		if id == "" || name == "" {
			continue
		}
		entries = append(entries, ProviderCatalogEntry{
			ID:          id,
			Name:        name,
			DisplayName: firstNonEmpty(raw.DisplayName, name),
			Type:        firstNonEmpty(raw.Type, catalogTypes[id], defaultType),
			BaseURL:     normalizeProviderBaseURL(id, firstNonEmpty(raw.BaseURL, raw.API)),
			DocURL:      firstNonEmpty(raw.DocURL, raw.Doc),
			ModelsCount: len(raw.Models),
			Source:      "plugin:built_in",
		})
	}
	sortCatalogEntries(entries)
	providerCatalogPluginEntriesCache.Store(cacheKey, entries)
	return append([]ProviderCatalogEntry(nil), entries...), nil
}

func providerCatalogPluginCacheKey(path string, size int64, modified int64, catalogTypes map[string]string, defaultType string) string {
	keys := make([]string, 0, len(catalogTypes))
	for key := range catalogTypes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var types strings.Builder
	for _, key := range keys {
		types.WriteString(key)
		types.WriteByte('=')
		types.WriteString(catalogTypes[key])
		types.WriteByte(';')
	}
	return fmt.Sprintf("%s:%d:%d:%s:%s", path, size, modified, defaultType, types.String())
}

func providerCatalogPluginIDs(registry *pluginmeta.Registry) map[string]string {
	ids := map[string]string{}
	for _, descriptor := range registry.List() {
		for _, entry := range providerCatalogEntriesFromPluginCapabilities(descriptor) {
			if entry.ID != "" {
				ids[entry.ID] = descriptor.ID
			}
		}
	}
	return ids
}

func registerBuiltinProviderCatalogEntryPlugin(registry *pluginmeta.Registry, entry ProviderCatalogEntry, runtime pluginmeta.Runtime) error {
	pluginID := builtinProviderCatalogPluginPrefix + strings.ToLower(strings.TrimSpace(entry.ID))
	state, err := builtInProviderPluginState(&runtime, pluginID)
	if err != nil {
		return fmt.Errorf("read provider catalog plugin %s state: %w", pluginID, err)
	}
	metadata := builtinProviderPluginCatalogEntryFromProviderCatalog(entry)
	metadata.Models = nil
	metadata.Source = providerCatalogGeneratedPluginSource
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode provider catalog plugin %s: %w", pluginID, err)
	}
	return registry.Register(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    firstNonEmpty(entry.DisplayName, entry.Name, entry.ID),
		Version: "built-in",
		Source:  pluginmeta.SourceBuiltIn,
		Status:  state.Status,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{{
			Kind:    pluginmeta.CapabilityKindProviderCatalog,
			Name:    pluginmeta.ProviderCatalogEntry,
			Subject: entry.Type,
			Value:   string(encoded),
		}},
	})
}
