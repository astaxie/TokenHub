package server

import (
	"context"
	"net/http"
	"sort"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type adminPluginMarketplaceResponse struct {
	SourceURL string                         `json:"source_url,omitempty"`
	Available bool                           `json:"available"`
	Error     string                         `json:"error,omitempty"`
	Plugins   []adminPluginMarketplacePlugin `json:"plugins"`
}

type adminPluginMarketplacePlugin struct {
	Plugin           pluginmeta.Descriptor `json:"plugin"`
	Installed        bool                  `json:"installed"`
	InstalledVersion string                `json:"installed_version,omitempty"`
	UpdateAvailable  bool                  `json:"update_available"`
}

func (s *Server) handleAdminPluginMarketplaceGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	response := adminPluginMarketplaceResponse{SourceURL: strings.TrimSpace(s.config.PluginMarketplaceURL)}
	items, err := s.loadPluginMarketplace(r.Context())
	if err != nil {
		response.Error = err.Error()
		writeJSON(w, http.StatusOK, map[string]any{"data": response})
		return
	}
	response.Available = len(items) > 0 || response.SourceURL != ""
	response.Plugins = s.annotatePluginMarketplace(items)
	writeJSON(w, http.StatusOK, map[string]any{"data": response})
}

func (s *Server) loadPluginMarketplace(ctx context.Context) ([]pluginmeta.Descriptor, error) {
	if s == nil || strings.TrimSpace(s.config.PluginMarketplaceURL) == "" {
		return nil, nil
	}
	client := s.pluginMarketplaceClient
	if client == nil {
		client = http.DefaultClient
	}
	return pluginmeta.NewMarketplace(s.config.PluginMarketplaceURL, client).List(ctx)
}

func (s *Server) annotatePluginMarketplace(items []pluginmeta.Descriptor) []adminPluginMarketplacePlugin {
	annotated := make([]adminPluginMarketplacePlugin, 0, len(items))
	for _, plugin := range items {
		installed, ok := pluginmeta.Descriptor{}, false
		if s != nil && s.pluginRegistry != nil {
			installed, ok = s.pluginRegistry.Describe(plugin.ID)
		}
		entry := adminPluginMarketplacePlugin{
			Plugin:    plugin,
			Installed: ok,
		}
		if ok {
			entry.InstalledVersion = strings.TrimSpace(installed.Version)
			entry.UpdateAvailable = marketplaceUpdateAvailable(installed, plugin)
		}
		annotated = append(annotated, entry)
	}
	sort.Slice(annotated, func(i, j int) bool {
		if annotated[i].Installed != annotated[j].Installed {
			return annotated[i].Installed && !annotated[j].Installed
		}
		return annotated[i].Plugin.ID < annotated[j].Plugin.ID
	})
	return annotated
}

func marketplaceUpdateAvailable(installed pluginmeta.Descriptor, available pluginmeta.Descriptor) bool {
	if strings.TrimSpace(installed.ID) == "" || strings.TrimSpace(available.ID) == "" {
		return false
	}
	if strings.TrimSpace(installed.Version) == "" || strings.TrimSpace(available.Version) == "" {
		return false
	}
	if installed.Version == available.Version {
		return false
	}
	distribution := available.Distribution
	if distribution == nil {
		return false
	}
	return strings.TrimSpace(distribution.DownloadURL) != "" && strings.TrimSpace(distribution.ChecksumSHA256) != ""
}
