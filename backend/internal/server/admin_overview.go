package server

import (
	"context"
	"net/http"
)

type OverviewModelCounts struct {
	Total      int64
	Active     int64
	Playground int64
}

func (s *GormStore) QueryOverviewModelCounts(ctx context.Context, global bool) (OverviewModelCounts, error) {
	db := visibleAdminModels(s.criticalReadDatabase().WithContext(ctx), global)
	var counts OverviewModelCounts
	err := db.Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN models.status = ? THEN 1 ELSE 0 END), 0) AS active, COALESCE(SUM(CASE WHEN models.status = ? AND COALESCE(modality, '') IN ('', 'chat') THEN 1 ELSE 0 END), 0) AS playground", StatusActive, StatusActive).Scan(&counts).Error
	return counts, err
}

func (s *Server) serveAdminOverview(w http.ResponseWriter, r *http.Request, user AdminUser) {
	ctx, cancel := context.WithTimeout(r.Context(), maximumUsageQueryDuration)
	defer cancel()
	scoped := s.usageQueryServer(ctx)
	store := scoped.store
	providers := []Provider{}
	resources := []ProviderResource{}
	alerts := []AlertEvent{}
	global := s.canViewGlobalOperations(user)
	if global {
		providers = store.ListProviders()
		resources = store.ListProviderResources()
		recent, err := store.ListRecentAlerts(ctx)
		if err != nil {
			writeError(w, r, NewHTTPError(503, "alerts_unavailable", "Alerts are temporarily unavailable"))
			return
		}
		alerts = recent
	}
	models, err := store.QueryOverviewModelCounts(ctx, global)
	if err != nil {
		writeError(w, r, NewHTTPError(503, "overview_unavailable", "Overview is temporarily unavailable"))
		return
	}
	apiKeys := len(scoped.filterAPIKeysForUser(user, store.ListAPIKeys()))
	users := len(scoped.filterAdminUsersForUser(user, store.ListAdminUsers()))
	projects := scoped.filterProjectsForUser(user, store.ListProjects())
	routes, activeRoutes := 0, 0
	if global {
		for _, route := range store.ListRoutes() {
			routes++
			if route.Status == StatusActive {
				activeRoutes++
			}
		}
	}
	summary, status := s.overviewSummaryForUser(ctx, user)
	if summary == nil {
		summary = map[string]any{}
	}
	visiblePlayground := models.Playground
	if visiblePlayground == 0 {
		visiblePlayground = models.Active
	}
	summary["model_count"], summary["active_model_count"] = models.Total, models.Active
	summary["playground_model_count"] = visiblePlayground
	summary["api_key_count"], summary["user_count"] = apiKeys, users
	summary["route_count"], summary["active_route_count"] = routes, activeRoutes
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary, "summary_status": status,
		"projects":  projects,
		"providers": providers, "provider_resources": resources,
		"alerts": alerts,
	})
}
