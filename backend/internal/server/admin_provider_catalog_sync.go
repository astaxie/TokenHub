package server

import (
	"net/http"
	"time"
)

// handleAdminProviderCatalogSyncPost backs the explicit "sync provider
// catalog" admin action. It bypasses the freshness window, fetches the public
// catalog from the configured upstream (falling back to the bundled catalog),
// and reports the outcome so the console can show when the snapshot landed.
func (s *Server) handleAdminProviderCatalogSyncPost(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "provider", r.Method); !ok {
		return
	}
	startedAt := time.Now()
	entries, source, err := s.providerCatalog.ForceReload(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	modelsCount := 0
	for _, entry := range entries {
		modelsCount += entry.ModelsCount
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":       source,
		"providers":    len(entries),
		"models_count": modelsCount,
		"duration_ms":  time.Since(startedAt).Milliseconds(),
	})
}
