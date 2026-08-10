package server

import (
	"net/http"
	"strings"
	"time"
)

type gatewayIntegrationReadStore interface {
	listGatewayModelsForIntegration() ([]Model, error)
	listGatewayProvidersForIntegration() ([]Provider, error)
	listGatewayProviderResourcesForIntegration() ([]ProviderResource, error)
	listGatewayRoutesForIntegration() ([]ModelRoute, error)
}

func listGatewayModelsForIntegration(store Store) ([]Model, error) {
	if reader, ok := store.(gatewayIntegrationReadStore); ok {
		return reader.listGatewayModelsForIntegration()
	}
	return store.ListModels(), nil
}

func listGatewayProvidersForIntegration(store Store) ([]Provider, error) {
	if reader, ok := store.(gatewayIntegrationReadStore); ok {
		return reader.listGatewayProvidersForIntegration()
	}
	return store.ListProviders(), nil
}

func listGatewayProviderResourcesForIntegration(store Store) ([]ProviderResource, error) {
	if reader, ok := store.(gatewayIntegrationReadStore); ok {
		return reader.listGatewayProviderResourcesForIntegration()
	}
	return store.ListProviderResources(), nil
}

func listGatewayRoutesForIntegration(store Store) ([]ModelRoute, error) {
	if reader, ok := store.(gatewayIntegrationReadStore); ok {
		return reader.listGatewayRoutesForIntegration()
	}
	return store.ListRoutes(), nil
}

func (s *GormStore) listGatewayModelsForIntegration() ([]Model, error) {
	var items []Model
	if err := s.db.Order("name asc").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *GormStore) listGatewayProvidersForIntegration() ([]Provider, error) {
	var items []Provider
	if err := s.db.Order("priority asc").Find(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		items[i].APIKey = ""
	}
	return items, nil
}

func (s *GormStore) listGatewayProviderResourcesForIntegration() ([]ProviderResource, error) {
	var items []ProviderResource
	if err := s.db.Order("provider_id asc, priority asc, weight desc, created_at asc").Find(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		redactProviderResourceSecrets(&items[i])
	}
	return items, nil
}

func (s *GormStore) listGatewayRoutesForIntegration() ([]ModelRoute, error) {
	var items []ModelRoute
	if err := s.db.Order("model_name asc, priority asc").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

var _ gatewayIntegrationReadStore = (*GormStore)(nil)

type gatewayProviderItem struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Type                 string    `json:"type"`
	BaseURL              string    `json:"base_url"`
	Status               string    `json:"status"`
	Healthy              bool      `json:"healthy"`
	Priority             int       `json:"priority"`
	ResourceCount        int       `json:"resource_count"`
	HealthyResourceCount int       `json:"healthy_resource_count"`
	CreatedAt            time.Time `json:"created_at"`
}

type gatewayRouteItem struct {
	ID                 string     `json:"id"`
	ModelName          string     `json:"model_name"`
	ProviderID         string     `json:"provider_id"`
	ProviderName       string     `json:"provider_name"`
	ProviderResourceID string     `json:"provider_resource_id"`
	ResourceName       string     `json:"resource_name"`
	ResourceGroup      string     `json:"resource_group"`
	StickySession      bool       `json:"sticky_session"`
	ProviderModel      string     `json:"provider_model"`
	Priority           int        `json:"priority"`
	Weight             int        `json:"weight"`
	QualityScore       int        `json:"quality_score"`
	CostScore          int        `json:"cost_score"`
	Status             string     `json:"status"`
	Strategy           string     `json:"strategy"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

func (s *Server) handleGatewayProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	page, pageSize, ok := gatewayModelPagination(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_provider_query", "Provider query is invalid"))
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	providerType := strings.TrimSpace(r.URL.Query().Get("type"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	health := strings.TrimSpace(r.URL.Query().Get("health"))
	if len(name) > 200 || len(providerType) > 100 || len(status) > 100 || (health != "" && health != "healthy" && health != "unhealthy") {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_provider_query", "Provider query is invalid"))
		return
	}

	resources, err := listGatewayProviderResourcesForIntegration(s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	providers, err := listGatewayProvidersForIntegration(s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resourceCounts := map[string]int{}
	healthyResourceCounts := map[string]int{}
	for _, resource := range resources {
		resourceCounts[resource.ProviderID]++
		if resource.Status == StatusActive && resource.Healthy {
			healthyResourceCounts[resource.ProviderID]++
		}
	}
	filtered := make([]gatewayProviderItem, 0)
	for _, provider := range providers {
		if name != "" && !strings.Contains(strings.ToLower(provider.Name+" "+provider.ID), name) {
			continue
		}
		if providerType != "" && provider.Type != providerType {
			continue
		}
		if status != "" && provider.Status != status {
			continue
		}
		if health == "healthy" && !provider.Healthy || health == "unhealthy" && provider.Healthy {
			continue
		}
		filtered = append(filtered, gatewayProviderItem{
			ID: provider.ID, Name: provider.Name, Type: provider.Type, BaseURL: provider.BaseURL,
			Status: provider.Status, Healthy: provider.Healthy, Priority: provider.Priority,
			ResourceCount: resourceCounts[provider.ID], HealthyResourceCount: healthyResourceCounts[provider.ID],
			CreatedAt: provider.CreatedAt,
		})
	}
	writeGatewayInfrastructurePage(w, page, pageSize, filtered)
}

func (s *Server) handleGatewayRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	if !s.requireGatewayIntegrationToken(w, r) {
		return
	}
	page, pageSize, ok := gatewayModelPagination(r)
	if !ok {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_route_query", "Route query is invalid"))
		return
	}
	modelName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("model_name")))
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	strategy := strings.TrimSpace(r.URL.Query().Get("strategy"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if len(modelName) > 200 || len(providerID) > 200 || len(strategy) > 100 || len(status) > 100 {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_route_query", "Route query is invalid"))
		return
	}

	providers, err := listGatewayProvidersForIntegration(s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resources, err := listGatewayProviderResourcesForIntegration(s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	routes, err := listGatewayRoutesForIntegration(s.store)
	if err != nil {
		writeError(w, r, err)
		return
	}
	providerNames := map[string]string{}
	for _, provider := range providers {
		providerNames[provider.ID] = provider.Name
	}
	resourceNames := map[string]string{}
	for _, resource := range resources {
		resourceNames[resource.ID] = resource.Name
	}
	filtered := make([]gatewayRouteItem, 0)
	for _, route := range routes {
		if modelName != "" && !strings.Contains(strings.ToLower(route.ModelName), modelName) {
			continue
		}
		if providerID != "" && route.ProviderID != providerID {
			continue
		}
		if strategy != "" && route.Strategy != strategy {
			continue
		}
		if status != "" && route.Status != status {
			continue
		}
		filtered = append(filtered, gatewayRouteItem{
			ID: route.ID, ModelName: route.ModelName, ProviderID: route.ProviderID,
			ProviderName: providerNames[route.ProviderID], ProviderResourceID: route.ProviderResourceID,
			ResourceName: resourceNames[route.ProviderResourceID], ResourceGroup: route.ResourceGroup,
			StickySession: route.StickySession, ProviderModel: route.ProviderModel, Priority: route.Priority,
			Weight: route.Weight, QualityScore: route.QualityScore, CostScore: route.CostScore,
			Status: route.Status, Strategy: route.Strategy, LastUsedAt: route.LastUsedAt, CreatedAt: route.CreatedAt,
		})
	}
	writeGatewayInfrastructurePage(w, page, pageSize, filtered)
}

func writeGatewayInfrastructurePage[T any](w http.ResponseWriter, page int, pageSize int, items []T) {
	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": items[start:end], "page": page, "page_size": pageSize, "total": total,
	})
}
