package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) providerCatalogEntriesWithPlugins(entries []ProviderCatalogEntry) ([]ProviderCatalogEntry, bool) {
	pluginEntries := s.pluginProviderCatalogEntries()
	if len(pluginEntries) == 0 {
		return entries, false
	}
	merged := append([]ProviderCatalogEntry(nil), entries...)
	indexByID := map[string]int{}
	for index, entry := range merged {
		if entry.ID != "" {
			indexByID[entry.ID] = index
		}
	}
	changed := false
	for _, entry := range pluginEntries {
		if index, ok := indexByID[entry.ID]; ok {
			enriched, updated := mergeProviderCatalogEntryWithPluginMetadata(merged[index], entry)
			if updated {
				merged[index] = enriched
				changed = true
			}
			continue
		}
		indexByID[entry.ID] = len(merged)
		merged = append(merged, entry)
		changed = true
	}
	sortCatalogEntries(merged)
	return merged, changed
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

func (s *Server) providerCatalogEntryWithPlugins(ctx context.Context, id string, refresh bool) (ProviderCatalogEntry, string, bool, error) {
	entry, source, ok, err := s.providerCatalog.Get(ctx, id, refresh)
	if err != nil {
		return ProviderCatalogEntry{}, source, false, err
	}
	pluginEntry, pluginOK := s.pluginProviderCatalogEntry(id)
	if !pluginOK {
		return entry, source, ok, nil
	}
	if !ok {
		return pluginEntry, pluginEntry.Source, true, nil
	}
	merged, changed := mergeProviderCatalogEntryWithPluginMetadata(entry, pluginEntry)
	if changed {
		source = firstNonEmpty(source, "catalog") + "+plugins"
	}
	return merged, source, true, nil
}

func (s *Server) pluginProviderCatalogCapabilityEntryForType(providerType string) (ProviderCatalogEntry, bool) {
	providerType = strings.TrimSpace(providerType)
	if s == nil || s.pluginRegistry == nil || s.adapterRegistry == nil || providerType == "" {
		return ProviderCatalogEntry{}, false
	}
	adapter, ok := s.adapterRegistry.Describe(providerType)
	if !ok || adapter.PluginID == "" {
		return ProviderCatalogEntry{}, false
	}
	plugin, ok := s.pluginRegistry.Describe(adapter.PluginID)
	if !ok {
		return ProviderCatalogEntry{}, false
	}
	return providerCatalogEntryFromPluginCapability(plugin, adapter)
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
		if !ok {
			continue
		}
		if plugin.Source == pluginmeta.SourceBuiltIn {
			if entry, ok := providerCatalogEntryFromPluginCapability(plugin, adapter); ok {
				entries = append(entries, entry)
			}
			continue
		}
		entries = append(entries, providerCatalogEntryFromPlugin(plugin, adapter))
	}
	sortCatalogEntries(entries)
	return entries
}

func (s *Server) providerCatalogMultiMethodRouteIDs() []string {
	ids := map[string]bool{"custom": true}
	for _, entry := range s.pluginProviderCatalogEntries() {
		if !validStaticProviderCatalogRouteID(entry.ID) {
			continue
		}
		if _, ok := s.providerPluginCapabilityActionDescriptor(entry.Type, AdapterCapabilityModels, "models.preview", ""); ok {
			ids[entry.ID] = true
		}
	}
	values := make([]string, 0, len(ids))
	for id := range ids {
		values = append(values, id)
	}
	sort.Strings(values)
	return values
}

func validStaticProviderCatalogRouteID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && !strings.Contains(id, "/") && !strings.Contains(id, "{") && !strings.Contains(id, "}")
}

func providerCatalogTypesFromRegistry(registry *AdapterRegistry) map[string]string {
	if registry == nil {
		return nil
	}
	types := map[string]string{}
	for _, adapter := range registry.List() {
		if adapter.PluginID == "" {
			continue
		}
		plugin, ok := adapterPluginDescriptor(registry, adapter.Type)
		if !ok {
			continue
		}
		entry, ok := providerCatalogEntryFromPluginCapability(plugin, adapter)
		if !ok || entry.ID == "" || entry.Type == "" {
			continue
		}
		types[entry.ID] = entry.Type
	}
	if len(types) == 0 {
		return nil
	}
	return types
}

func providerCatalogEntryFromPlugin(plugin pluginmeta.Descriptor, adapter AdapterDescriptor) ProviderCatalogEntry {
	if entry, ok := providerCatalogEntryFromPluginCapability(plugin, adapter); ok {
		return entry
	}
	name := firstNonEmpty(strings.TrimSpace(plugin.Name), adapter.Type)
	return ProviderCatalogEntry{
		ID:          adapter.Type,
		Name:        name,
		DisplayName: name,
		Type:        adapter.Type,
		Source:      "plugin:" + string(plugin.Source),
	}
}

func mergeProviderCatalogEntryWithPluginMetadata(base ProviderCatalogEntry, pluginEntry ProviderCatalogEntry) (ProviderCatalogEntry, bool) {
	merged := base
	if pluginEntry.ID != "" {
		merged.ID = pluginEntry.ID
	}
	if pluginEntry.Name != "" {
		merged.Name = pluginEntry.Name
	}
	if pluginEntry.DisplayName != "" {
		merged.DisplayName = pluginEntry.DisplayName
	}
	if pluginEntry.Type != "" {
		merged.Type = pluginEntry.Type
	}
	if pluginEntry.BaseURL != "" {
		merged.BaseURL = pluginEntry.BaseURL
	}
	if pluginEntry.DocURL != "" {
		merged.DocURL = pluginEntry.DocURL
	}
	if pluginEntry.Source != "" {
		merged.Source = pluginEntry.Source
	}
	if pluginEntry.ETag != "" {
		merged.ETag = pluginEntry.ETag
	}
	if len(pluginEntry.Models) > 0 {
		merged.Models = append([]ProviderCatalogModel(nil), pluginEntry.Models...)
		merged.ModelsCount = len(pluginEntry.Models)
		merged.Categories, merged.CategoryCounts = catalogCategorySummary(merged.Models)
	} else {
		if pluginEntry.ModelsCount > 0 && len(merged.Models) == 0 {
			merged.ModelsCount = pluginEntry.ModelsCount
		}
		if len(pluginEntry.Categories) > 0 && len(merged.Categories) == 0 {
			merged.Categories = append([]string(nil), pluginEntry.Categories...)
			merged.CategoryCounts = map[string]int{}
			for _, category := range merged.Categories {
				merged.CategoryCounts[category] = 0
			}
		}
	}
	return merged, !reflect.DeepEqual(base, merged)
}

type pluginProviderCatalogEntry struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	DisplayName string                       `json:"display_name"`
	Type        string                       `json:"type"`
	BaseURL     string                       `json:"base_url"`
	DocURL      string                       `json:"doc_url"`
	Categories  []string                     `json:"categories"`
	ModelsCount int                          `json:"models_count"`
	Source      string                       `json:"source"`
	ETag        string                       `json:"etag"`
	Models      []pluginProviderCatalogModel `json:"models"`
}

type pluginProviderCatalogModel struct {
	ID                        string            `json:"id"`
	Name                      string            `json:"name"`
	DisplayName               string            `json:"display_name"`
	CanonicalName             string            `json:"canonical_name"`
	Category                  string            `json:"category"`
	Family                    string            `json:"family"`
	Type                      string            `json:"type"`
	ContextWindow             int64             `json:"context_window"`
	MaxOutputTokens           int64             `json:"max_output_tokens"`
	InputPriceUSDPer1M        float64           `json:"input_price_usd_per_1m"`
	CacheReadPriceUSDPer1M    float64           `json:"cache_read_price_usd_per_1m"`
	CacheWritePriceUSDPer1M   float64           `json:"cache_write_price_usd_per_1m"`
	CacheWrite5mPriceUSDPer1M float64           `json:"cache_write_5m_price_usd_per_1m"`
	CacheWrite1hPriceUSDPer1M float64           `json:"cache_write_1h_price_usd_per_1m"`
	OutputPriceUSDPer1M       float64           `json:"output_price_usd_per_1m"`
	InputModalities           []string          `json:"input_modalities"`
	OutputModalities          []string          `json:"output_modalities"`
	Capabilities              []string          `json:"capabilities"`
	SupportedParameters       []string          `json:"supported_parameters"`
	LastUpdated               string            `json:"last_updated"`
	Metadata                  map[string]string `json:"metadata"`
}

func providerCatalogEntryFromPluginCapability(plugin pluginmeta.Descriptor, adapter AdapterDescriptor) (ProviderCatalogEntry, bool) {
	for _, capability := range plugin.Capabilities {
		if capability.Kind != "provider_catalog" || capability.Name != "entry" || strings.TrimSpace(capability.Value) == "" {
			continue
		}
		if capability.Subject != "" && capability.Subject != adapter.Type {
			continue
		}
		var manifestEntry pluginProviderCatalogEntry
		if err := json.Unmarshal([]byte(capability.Value), &manifestEntry); err != nil {
			continue
		}
		entry := manifestEntry.providerCatalogEntry(plugin, adapter)
		if entry.ID != "" && entry.Type != "" {
			return entry, true
		}
	}
	return ProviderCatalogEntry{}, false
}

func (entry pluginProviderCatalogEntry) providerCatalogEntry(plugin pluginmeta.Descriptor, adapter AdapterDescriptor) ProviderCatalogEntry {
	models := make([]ProviderCatalogModel, 0, len(entry.Models))
	for _, model := range entry.Models {
		if converted, ok := model.providerCatalogModel(); ok {
			models = append(models, converted)
		}
	}
	categories := catalogUniqueStrings(entry.Categories)
	categoryCounts := map[string]int(nil)
	if len(models) > 0 {
		modelCategories, counts := catalogCategorySummary(models)
		if len(categories) == 0 {
			categories = modelCategories
		}
		categoryCounts = counts
	} else if len(categories) > 0 {
		categoryCounts = map[string]int{}
		for _, category := range categories {
			categoryCounts[category] = 0
		}
	}
	modelsCount := entry.ModelsCount
	if len(models) > 0 {
		modelsCount = len(models)
	}
	name := firstNonEmpty(strings.TrimSpace(entry.Name), strings.TrimSpace(entry.DisplayName), strings.TrimSpace(plugin.Name), adapter.Type)
	return ProviderCatalogEntry{
		ID:             firstNonEmpty(strings.TrimSpace(entry.ID), adapter.Type),
		Name:           name,
		DisplayName:    firstNonEmpty(strings.TrimSpace(entry.DisplayName), name),
		Type:           firstNonEmpty(strings.TrimSpace(entry.Type), adapter.Type),
		BaseURL:        strings.TrimSpace(entry.BaseURL),
		DocURL:         strings.TrimSpace(entry.DocURL),
		Categories:     categories,
		CategoryCounts: categoryCounts,
		ModelsCount:    modelsCount,
		Source:         firstNonEmpty(strings.TrimSpace(entry.Source), "plugin:"+string(plugin.Source)),
		ETag:           strings.TrimSpace(entry.ETag),
		Models:         models,
	}
}

func (model pluginProviderCatalogModel) providerCatalogModel() (ProviderCatalogModel, bool) {
	id := strings.TrimSpace(model.ID)
	if id == "" {
		return ProviderCatalogModel{}, false
	}
	name := firstNonEmpty(strings.TrimSpace(model.Name), id)
	displayName := firstNonEmpty(strings.TrimSpace(model.DisplayName), name)
	category := standardModelCategory(firstNonEmpty(model.Category, inferModelCategory(id, displayName)))
	family := firstNonEmpty(strings.TrimSpace(model.Family), inferModelFamily(id))
	metadata := model.Metadata
	if metadata == nil {
		metadata = map[string]string{"source": "plugin"}
	}
	return ProviderCatalogModel{
		ID:                        id,
		Name:                      name,
		DisplayName:               displayName,
		CanonicalName:             firstNonEmpty(strings.TrimSpace(model.CanonicalName), canonicalModelName(id, displayName)),
		Category:                  category,
		Family:                    family,
		Type:                      firstNonEmpty(strings.TrimSpace(model.Type), "chat"),
		ContextWindow:             model.ContextWindow,
		MaxOutputTokens:           model.MaxOutputTokens,
		InputPriceUSDPer1M:        model.InputPriceUSDPer1M,
		CacheReadPriceUSDPer1M:    model.CacheReadPriceUSDPer1M,
		CacheWritePriceUSDPer1M:   model.CacheWritePriceUSDPer1M,
		CacheWrite5mPriceUSDPer1M: model.CacheWrite5mPriceUSDPer1M,
		CacheWrite1hPriceUSDPer1M: model.CacheWrite1hPriceUSDPer1M,
		OutputPriceUSDPer1M:       model.OutputPriceUSDPer1M,
		InputModalities:           catalogUniqueStrings(model.InputModalities),
		OutputModalities:          catalogUniqueStrings(model.OutputModalities),
		Capabilities:              catalogUniqueStrings(model.Capabilities),
		SupportedParameters:       catalogUniqueStrings(model.SupportedParameters),
		LastUpdated:               strings.TrimSpace(model.LastUpdated),
		Metadata:                  metadata,
	}, true
}

func providerCatalogEntryWithSubmittedModels(entry ProviderCatalogEntry, models []ProviderCatalogModel, category string) ProviderCatalogEntry {
	catalog := customProviderCatalogFromModels(models, category)
	catalog.ID = firstNonEmpty(entry.ID, catalog.ID)
	catalog.Name = firstNonEmpty(entry.Name, entry.DisplayName, catalog.Name)
	catalog.DisplayName = firstNonEmpty(entry.DisplayName, catalog.DisplayName, catalog.Name)
	catalog.Type = firstNonEmpty(entry.Type, catalog.Type)
	catalog.BaseURL = entry.BaseURL
	catalog.DocURL = entry.DocURL
	catalog.Source = firstNonEmpty(entry.Source, catalog.Source)
	return catalog
}
