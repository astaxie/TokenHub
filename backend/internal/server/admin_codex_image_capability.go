package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const codexImageCapabilityTestTimeout = 120 * time.Second

const (
	codexImageRouteBackfillOption    = "image_generation_route_backfill_v1"
	codexImageRouteBackfillCompleted = "completed"
)

type codexImageCapabilityRequest struct {
	Enabled *bool `json:"enabled"`
}

type codexImageCapabilityResult struct {
	Enabled    bool   `json:"enabled"`
	Tested     bool   `json:"tested"`
	Capability string `json:"capability,omitempty"`
	ResourceID string `json:"resource_id"`
	RouteID    string `json:"route_id,omitempty"`
}

func (s *Server) handleAdminCodexImageCapability(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	var req codexImageCapabilityRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Enabled == nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "codex_image_enabled_required", "The enabled field is required"))
		return
	}
	enabled := *req.Enabled
	result, err := s.configureCodexImageCapability(r.Context(), resourceID, enabled)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, "configure_codex_image", "provider_resource", resourceID, "failed", httpErr.Code, "", map[string]any{
			"enabled":    enabled,
			"error_code": httpErr.Code,
		})
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "configure_codex_image", "provider_resource", resourceID, "", map[string]any{
		"enabled":    result.Enabled,
		"tested":     result.Tested,
		"capability": result.Capability,
		"route_id":   result.RouteID,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) configureCodexImageCapability(ctx context.Context, resourceID string, enabled bool) (codexImageCapabilityResult, error) {
	resource, provider, err := s.codexImageResource(resourceID, enabled)
	if err != nil {
		return codexImageCapabilityResult{}, err
	}
	result := codexImageCapabilityResult{Enabled: enabled, ResourceID: resource.ID}
	err = s.store.RunClusterOperation(ctx, "codex-image-capability:"+provider.ID, func(leaseCtx context.Context) error {
		current, currentProvider, currentErr := s.codexImageResource(resourceID, enabled)
		if currentErr != nil {
			return currentErr
		}
		if !enabled {
			if err := s.setCodexImageRoutesStatus(currentProvider.ID, StatusDisabled); err != nil {
				return err
			}
			result.Capability = strings.TrimSpace(current.Options[codexImageCapabilityOption])
			return nil
		}

		if route := activeCodexImageRoute(s.store.ListRoutes(), currentProvider.ID); route != nil &&
			strings.TrimSpace(current.Options[codexImageCapabilityOption]) == codexImageCapabilitySupported {
			result.RouteID = route.ID
			result.Capability = codexImageCapabilitySupported
			return nil
		}

		testCtx, cancel := context.WithTimeout(leaseCtx, codexImageCapabilityTestTimeout)
		defer cancel()
		effectiveProvider := effectiveProviderResourceConfig(currentProvider, &current)
		response, _, imageErr := s.codexSubscription.Image(testCtx, effectiveProvider, current.ID, codexSubscriptionImageRequest{
			Model:      codexImageUpstreamModel,
			Prompt:     "A small solid blue square centered on a white background.",
			Background: "auto",
			Quality:    "low",
			Size:       "1024x1024",
		})
		result.Tested = true
		if imageErr != nil && errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			imageErr = &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusGatewayTimeout, "codex_upstream_timeout", "Codex image capability test timed out"),
				Disposition: ProviderErrorTransientSame,
			}
		}
		if imageErr != nil {
			if AsHTTPError(imageErr).Code == "codex_image_forbidden" {
				_, updateErr := s.updateCodexImageCapability(current.ID, codexImageCapabilityUnsupported)
				if updateErr != nil {
					return updateErr
				}
				result.Capability = codexImageCapabilityUnsupported
			}
			return publicCodexImageCapabilityProbeError(imageErr)
		}
		if len(response.Data) == 0 || strings.TrimSpace(response.Data[0].B64JSON) == "" {
			return NewHTTPError(http.StatusBadGateway, "image_result_missing", "Codex image capability test completed without an image result")
		}
		if _, err := decodeGeneratedImage(response.Data[0].B64JSON); err != nil {
			return NewHTTPError(http.StatusBadGateway, "image_result_invalid", err.Error())
		}
		route, err := s.ensureCodexImageRoute(currentProvider.ID)
		if err != nil {
			return err
		}
		_, err = s.updateCodexImageCapability(current.ID, codexImageCapabilitySupported)
		if err != nil {
			return err
		}
		result.RouteID = route.ID
		result.Capability = codexImageCapabilitySupported
		return nil
	})
	return result, err
}

func publicCodexImageCapabilityProbeError(err error) error {
	httpErr := AsHTTPError(err)
	message := "Codex image capability test failed"
	switch httpErr.Code {
	case "codex_image_forbidden":
		message = "This Codex subscription account is not allowed to use image generation"
	case "provider_resource_reauthorization_required":
		message = "OpenAI/Codex account session has ended. Reauthorize the account."
	case "codex_rate_limited", "codex_quota_exhausted", "codex_upstream_unavailable":
		message = "Codex image capability test is temporarily unavailable"
	case "codex_upstream_timeout":
		message = "Codex image capability test timed out"
	}
	publicErr := NewHTTPError(httpErr.Status, httpErr.Code, message)
	var invocationErr *ProviderInvocationError
	if errors.As(err, &invocationErr) {
		return &ProviderInvocationError{
			Err:         publicErr,
			Disposition: invocationErr.Disposition,
			RetryAfter:  invocationErr.RetryAfter,
		}
	}
	return publicErr
}

func (s *Server) codexImageResource(resourceID string, requireActive bool) (ProviderResource, Provider, error) {
	resource, ok := s.store.GetProviderResource(strings.TrimSpace(resourceID))
	if !ok {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.store.GetProvider(resource.ProviderID)
	if !ok {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if provider.Type != ProviderOpenAICodex || resource.ResourceType != ProviderResourceOpenAISubscription {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusBadRequest, "codex_subscription_resource_required", "Codex image capability testing requires an OpenAI Codex subscription account")
	}
	if requireActive && (provider.Status != StatusActive || !provider.Healthy || resource.Status != StatusActive || !resource.Healthy) {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusConflict, "provider_resource_unavailable", "Enable the Codex subscription account before testing image generation")
	}
	return resource, provider, nil
}

func (s *Server) updateCodexImageCapability(resourceID string, capability string) (ProviderResource, error) {
	options := map[string]string{
		codexImageCapabilityOption:          capability,
		codexImageCapabilityCheckedAtOption: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if capability == codexImageCapabilitySupported {
		options[codexImageRouteBackfillOption] = codexImageRouteBackfillCompleted
	}
	return s.store.UpdateProviderResourceOptions(resourceID, options)
}

func codexImageRouteMatches(route ModelRoute, providerID string) bool {
	return strings.TrimSpace(route.ModelName) == codexImageModelName &&
		strings.TrimSpace(route.ProviderID) == strings.TrimSpace(providerID) &&
		strings.TrimSpace(route.ProviderModel) == codexImageUpstreamModel
}

func codexImageRouteHasSupportedResource(route ModelRoute, resources []ProviderResource) bool {
	providerID := strings.TrimSpace(route.ProviderID)
	resourceID := strings.TrimSpace(route.ProviderResourceID)
	resourceGroup := strings.TrimSpace(route.ResourceGroup)
	for _, resource := range resources {
		if strings.TrimSpace(resource.ProviderID) != providerID ||
			resource.ResourceType != ProviderResourceOpenAISubscription ||
			resource.Status != StatusActive || !resource.Healthy ||
			strings.TrimSpace(resource.Options[codexImageCapabilityOption]) != codexImageCapabilitySupported {
			continue
		}
		if resourceID != "" && strings.TrimSpace(resource.ID) != resourceID {
			continue
		}
		if resourceGroup != "" && strings.TrimSpace(resource.Group) != resourceGroup {
			continue
		}
		return true
	}
	return false
}

func activeCodexImageRoute(routes []ModelRoute, providerID string) *ModelRoute {
	for index := range routes {
		if codexImageRouteMatches(routes[index], providerID) && routes[index].Status == StatusActive {
			route := routes[index]
			return &route
		}
	}
	return nil
}

func (s *Server) ensureCodexImageRoute(providerID string) (ModelRoute, error) {
	var disabled *ModelRoute
	for _, route := range s.store.ListRoutes() {
		if !codexImageRouteMatches(route, providerID) {
			continue
		}
		if route.Status == StatusActive {
			return route, nil
		}
		if disabled == nil {
			copy := route
			disabled = &copy
		}
	}
	if disabled != nil {
		disabled.Status = StatusActive
		return s.store.UpdateRoute(disabled.ID, *disabled)
	}
	return s.store.CreateRoute(defaultCodexImageRoute(providerID))
}

func (s *Server) setCodexImageRoutesStatus(providerID string, status string) error {
	for _, route := range s.store.ListRoutes() {
		if !codexImageRouteMatches(route, providerID) || route.Status == status {
			continue
		}
		route.Status = status
		if _, err := s.store.UpdateRoute(route.ID, route); err != nil {
			return err
		}
	}
	return nil
}

func defaultCodexImageRoute(providerID string) ModelRoute {
	return ModelRoute{
		ModelName:     codexImageModelName,
		ProviderID:    strings.TrimSpace(providerID),
		ProviderModel: codexImageUpstreamModel,
		Priority:      1,
		Weight:        100,
		QualityScore:  50,
		CostScore:     50,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityWeighted,
		ProjectScope:  RouteProjectScopeAll,
	}
}

func backfillCodexImageRoutes(store Store) {
	resources := store.ListProviderResources()
	for _, provider := range store.ListProviders() {
		if provider.Type != ProviderOpenAICodex || provider.Status != StatusActive || !provider.Healthy ||
			!providerHasSupportedCodexImageResource(resources, provider.ID) || codexImageRouteBackfillDone(resources, provider.ID) {
			continue
		}
		err := store.RunClusterOperation(context.Background(), "codex-image-capability:"+provider.ID, func(context.Context) error {
			currentResources := store.ListProviderResources()
			if !providerHasSupportedCodexImageResource(currentResources, provider.ID) || codexImageRouteBackfillDone(currentResources, provider.ID) {
				return nil
			}
			routeExists := false
			for _, route := range store.ListRoutes() {
				if codexImageRouteMatches(route, provider.ID) {
					routeExists = true
					break
				}
			}
			if !routeExists {
				if _, err := store.CreateRoute(defaultCodexImageRoute(provider.ID)); err != nil {
					return err
				}
			}
			for _, resource := range currentResources {
				if resource.ProviderID != provider.ID || strings.TrimSpace(resource.Options[codexImageCapabilityOption]) != codexImageCapabilitySupported {
					continue
				}
				if _, err := store.UpdateProviderResourceOptions(resource.ID, map[string]string{
					codexImageRouteBackfillOption: codexImageRouteBackfillCompleted,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			log.Printf("[tokenhub] failed to backfill Codex image route provider=%s: %v", provider.ID, err)
		}
	}
}

func codexImageRouteBackfillDone(resources []ProviderResource, providerID string) bool {
	for _, resource := range resources {
		if resource.ProviderID == providerID && resource.Options[codexImageRouteBackfillOption] == codexImageRouteBackfillCompleted {
			return true
		}
	}
	return false
}

func providerHasSupportedCodexImageResource(resources []ProviderResource, providerID string) bool {
	return codexImageRouteHasSupportedResource(ModelRoute{ProviderID: providerID}, resources)
}
