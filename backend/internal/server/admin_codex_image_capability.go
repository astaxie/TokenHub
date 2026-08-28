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

type providerImageCapabilityRequest struct {
	Enabled *bool `json:"enabled"`
}

type providerImageCapabilityResult struct {
	Enabled    bool   `json:"enabled"`
	Tested     bool   `json:"tested"`
	Capability string `json:"capability,omitempty"`
	ResourceID string `json:"resource_id"`
	RouteID    string `json:"route_id,omitempty"`
}

func (a CodexSubscriptionAdapter) ProviderOperationKey(provider Provider, operation ProviderAdminOperation) (string, bool) {
	if operation != ProviderAdminOperationDeleteProvider {
		return "", false
	}
	profiles := a.imageCapabilityProfilesForProvider(provider.Type)
	if len(profiles) == 0 {
		return "", false
	}
	return providerImageCapabilityClusterKey(profiles[0], provider.ID), true
}

func (a CodexSubscriptionAdapter) ProviderResourceOperationKey(provider Provider, resource ProviderResource, operation ProviderAdminOperation) (string, bool) {
	if operation != ProviderAdminOperationUpdateResource && operation != ProviderAdminOperationDeleteResource {
		return "", false
	}
	for _, profile := range a.imageCapabilityProfilesForProvider(provider.Type) {
		if profile.ResourceType != "" && profile.ResourceType != resource.ResourceType {
			continue
		}
		return providerImageCapabilityClusterKey(profile, firstNonEmpty(resource.ProviderID, provider.ID)), true
	}
	return "", false
}

func (a CodexSubscriptionAdapter) imageCapabilityProfilesForProvider(providerType string) []providerImageCapabilityRouteProfile {
	if a.ImageCapabilityProfiles == nil {
		return nil
	}
	providerType = strings.TrimSpace(providerType)
	profiles := []providerImageCapabilityRouteProfile{}
	for _, profile := range a.ImageCapabilityProfiles(providerType) {
		if profile.OperationKeyPrefix == "" {
			profile.OperationKeyPrefix = "codex-image-capability"
		}
		profile.withDefaults()
		if profile.ProviderType != "" && profile.ProviderType != providerType {
			continue
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

func codexImageCapabilityClusterKey(providerID string) string {
	return providerImageCapabilityClusterKey(codexImageCapabilityRouteProfile(), providerID)
}

func providerImageCapabilityClusterKey(profile providerImageCapabilityRouteProfile, providerID string) string {
	profile.withDefaults()
	return profile.OperationKeyPrefix + ":" + strings.TrimSpace(providerID)
}

func (s *Server) handleAdminProviderImageCapability(w http.ResponseWriter, r *http.Request, user AdminUser, resourceID string) {
	var req providerImageCapabilityRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	profile := s.providerImageCapabilityProfileForResource(resourceID)
	if req.Enabled == nil {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, profile.EnabledRequiredErrorCode, profile.EnabledRequiredMessage))
		return
	}
	enabled := *req.Enabled
	result, supported, err := s.executeProviderResourceImageCapabilityAction(r.Context(), user, resourceID, enabled)
	if !supported {
		err = NewHTTPError(http.StatusBadRequest, "provider_image_capability_unsupported", "Image capability configuration is not available for this provider resource")
	}
	if err != nil {
		httpErr := AsHTTPError(err)
		s.recordAdminAuditWithStatus(r, user, profile.AuditAction, "provider_resource", resourceID, "failed", httpErr.Code, "", map[string]any{
			"enabled":    enabled,
			"error_code": httpErr.Code,
		})
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, profile.AuditAction, "provider_resource", resourceID, "", map[string]any{
		"enabled":    result.Enabled,
		"tested":     result.Tested,
		"capability": result.Capability,
		"route_id":   result.RouteID,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) providerImageCapabilityProfileForResource(resourceID string) providerImageCapabilityRouteProfile {
	profile := providerImageCapabilityRouteProfile{}
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		profile.withDefaults()
		return profile
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		profile.withDefaults()
		return profile
	}
	action, ok := s.providerPluginCapabilityActionDescriptor(provider.Type, AdapterCapabilityImageGenerate, "image.capability.configure", resource.ResourceType)
	if !ok {
		profile.withDefaults()
		return profile
	}
	return providerImageCapabilityProfileFromAction(action)
}

func (s *Server) configureCodexImageCapability(ctx context.Context, resourceID string, enabled bool, profile providerImageCapabilityRouteProfile) (providerImageCapabilityResult, error) {
	profile.withDefaults()
	resource, provider, err := s.codexImageResource(resourceID, enabled, profile)
	if err != nil {
		return providerImageCapabilityResult{}, err
	}
	result := providerImageCapabilityResult{Enabled: enabled, ResourceID: resource.ID}
	err = s.store.RunClusterOperation(ctx, providerImageCapabilityClusterKey(profile, provider.ID), func(leaseCtx context.Context) error {
		current, currentProvider, currentErr := s.codexImageResource(resourceID, enabled, profile)
		if currentErr != nil {
			return currentErr
		}
		if !enabled {
			if err := s.setProviderImageCapabilityRoutesStatus(currentProvider.ID, profile, StatusDisabled); err != nil {
				return err
			}
			result.Capability = strings.TrimSpace(current.Options[profile.CapabilityOption])
			return nil
		}

		if route := activeProviderImageCapabilityRoute(s.store.ListRoutes(), currentProvider.ID, profile); route != nil &&
			profile.capabilityIsSupported(current.Options[profile.CapabilityOption]) {
			result.RouteID = route.ID
			result.Capability = profile.CapabilitySupportedValue
			return nil
		}

		testCtx, cancel := context.WithTimeout(leaseCtx, codexImageCapabilityTestTimeout)
		defer cancel()
		effectiveProvider := effectiveProviderResourceConfig(currentProvider, &current)
		response, _, imageErr := s.codexSubscription.Image(testCtx, effectiveProvider, current.ID, codexSubscriptionImageRequest{
			Model:      profile.UpstreamModel,
			Prompt:     profile.ProbePrompt,
			Background: profile.ProbeBackground,
			Quality:    profile.ProbeQuality,
			Size:       profile.ProbeSize,
		})
		result.Tested = true
		if imageErr != nil && errors.Is(testCtx.Err(), context.DeadlineExceeded) {
			imageErr = &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusGatewayTimeout, profile.ProbeTimeoutErrorCode, profile.ProbeTimeoutErrorMessage),
				Disposition: ProviderErrorTransientSame,
			}
		}
		if imageErr != nil {
			if AsHTTPError(imageErr).Code == "codex_image_forbidden" {
				_, updateErr := s.updateProviderImageCapability(current.ID, profile.CapabilityUnsupportedValue, profile)
				if updateErr != nil {
					return updateErr
				}
				result.Capability = profile.CapabilityUnsupportedValue
			}
			return publicProviderImageCapabilityProbeError(imageErr, profile)
		}
		if len(response.Data) == 0 || strings.TrimSpace(response.Data[0].B64JSON) == "" {
			return NewHTTPError(http.StatusBadGateway, "image_result_missing", "Codex image capability test completed without an image result")
		}
		if _, err := decodeGeneratedImage(response.Data[0].B64JSON); err != nil {
			return NewHTTPError(http.StatusBadGateway, "image_result_invalid", err.Error())
		}
		route, err := s.ensureProviderImageCapabilityRoute(currentProvider.ID, profile)
		if err != nil {
			return err
		}
		_, err = s.updateProviderImageCapability(current.ID, profile.CapabilitySupportedValue, profile)
		if err != nil {
			return err
		}
		result.RouteID = route.ID
		result.Capability = profile.CapabilitySupportedValue
		return nil
	})
	return result, err
}

func publicProviderImageCapabilityProbeError(err error, profile providerImageCapabilityRouteProfile) error {
	httpErr := AsHTTPError(err)
	message := providerImageCapabilityProbeErrorMessage(profile, httpErr.Code)
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

func providerImageCapabilityProbeErrorMessage(profile providerImageCapabilityRouteProfile, code string) string {
	profile.withDefaults()
	code = strings.TrimSpace(code)
	if message := strings.TrimSpace(profile.ProbeErrorMessages[code]); message != "" {
		return message
	}
	return "Provider image capability test failed"
}

func (s *Server) codexImageResource(resourceID string, requireActive bool, profiles ...providerImageCapabilityRouteProfile) (ProviderResource, Provider, error) {
	profile := codexImageCapabilityRouteProfile()
	if len(profiles) > 0 {
		profile = profiles[0]
		profile.withDefaults()
	}
	resource, ok := s.store.GetProviderResource(strings.TrimSpace(resourceID))
	if !ok {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.store.GetProvider(resource.ProviderID)
	if !ok {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if provider.Type != profile.ProviderType || resource.ResourceType != profile.ResourceType {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusBadRequest, "codex_subscription_resource_required", "Codex image capability testing requires an OpenAI Codex subscription account")
	}
	if requireActive && (provider.Status != StatusActive || !provider.Healthy || resource.Status != StatusActive || !resource.Healthy) {
		return ProviderResource{}, Provider{}, NewHTTPError(http.StatusConflict, "provider_resource_unavailable", "Enable the Codex subscription account before testing image generation")
	}
	return resource, provider, nil
}

func (s *Server) updateCodexImageCapability(resourceID string, capability string) (ProviderResource, error) {
	return s.updateProviderImageCapability(resourceID, capability, codexImageCapabilityRouteProfile())
}

func codexImageRouteMatches(route ModelRoute, providerID string) bool {
	return providerImageCapabilityRouteMatches(route, providerID, codexImageCapabilityRouteProfile())
}

func codexImageRouteHasSupportedResource(route ModelRoute, resources []ProviderResource) bool {
	return providerImageCapabilityRouteHasSupportedResource(route, resources, codexImageCapabilityRouteProfile())
}

func providerImageCapabilityRouteHasSupportedResource(route ModelRoute, resources []ProviderResource, profile providerImageCapabilityRouteProfile) bool {
	providerID := strings.TrimSpace(route.ProviderID)
	resourceID := strings.TrimSpace(route.ProviderResourceID)
	resourceGroup := strings.TrimSpace(route.ResourceGroup)
	for _, resource := range resources {
		if strings.TrimSpace(resource.ProviderID) != providerID ||
			(profile.ResourceType != "" && resource.ResourceType != profile.ResourceType) ||
			resource.Status != StatusActive || !resource.Healthy ||
			!profile.capabilityIsSupported(resource.Options[profile.CapabilityOption]) {
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
	return activeProviderImageCapabilityRoute(routes, providerID, codexImageCapabilityRouteProfile())
}

func activeProviderImageCapabilityRoute(routes []ModelRoute, providerID string, profile providerImageCapabilityRouteProfile) *ModelRoute {
	for index := range routes {
		if providerImageCapabilityRouteMatches(routes[index], providerID, profile) && routes[index].Status == StatusActive {
			route := routes[index]
			return &route
		}
	}
	return nil
}

func (s *Server) ensureCodexImageRoute(providerID string) (ModelRoute, error) {
	return s.ensureProviderImageCapabilityRoute(providerID, codexImageCapabilityRouteProfile())
}

func (s *Server) setCodexImageRoutesStatus(providerID string, status string) error {
	return s.setProviderImageCapabilityRoutesStatus(providerID, codexImageCapabilityRouteProfile(), status)
}

func defaultCodexImageRoute(providerID string) ModelRoute {
	return defaultProviderImageCapabilityRoute(providerID, codexImageCapabilityRouteProfile())
}

func codexImageCapabilityRouteProfile() providerImageCapabilityRouteProfile {
	return providerImageCapabilityRouteProfile{
		ProviderType:                ProviderOpenAICodex,
		ResourceType:                ProviderResourceOpenAISubscription,
		PublicModel:                 codexImageModelName,
		UpstreamModel:               codexImageUpstreamModel,
		CapabilityOption:            codexImageCapabilityOption,
		CapabilityCheckedAtOption:   codexImageCapabilityCheckedAtOption,
		CapabilitySupportedValue:    codexImageCapabilitySupported,
		CapabilityUnsupportedValue:  codexImageCapabilityUnsupported,
		RouteBackfillOption:         codexImageRouteBackfillOption,
		RouteBackfillValue:          codexImageRouteBackfillCompleted,
		OperationKeyPrefix:          "codex-image-capability",
		ProbePrompt:                 "A small solid blue square centered on a white background.",
		ProbeBackground:             "auto",
		ProbeQuality:                "low",
		ProbeSize:                   "1024x1024",
		ProbeTimeoutErrorCode:       "codex_upstream_timeout",
		ProbeTimeoutErrorMessage:    "Codex image capability test timed out",
		RuntimeUnsupportedErrorCode: "codex_image_forbidden",
		RequestAliasModel:           openAIImageModelName,
		RequestAliasHeader:          "x-codex-image-turn-id",
		RequestAliasOriginator:      "codex",
		RequestAliasResponseFormat:  "b64_json",
		RequestDefaultModel:         true,
		RequestSupportsMask:         false,
		RequestSupportsMaskSet:      true,
	}
}

func backfillCodexImageRoutes(store Store) {
	backfillProviderImageCapabilityRoutes(store)
}

func backfillProviderImageCapabilityRoutes(store Store) {
	for _, profile := range providerImageCapabilityRouteProfilesForBackfill(store) {
		backfillProviderImageCapabilityRoutesForProfile(store, profile)
	}
}

type providerImageCapabilityRouteProfileReader interface {
	providerImageCapabilityRouteProfiles() []providerImageCapabilityRouteProfile
}

func providerImageCapabilityRouteProfilesForBackfill(store Store) []providerImageCapabilityRouteProfile {
	if reader, ok := store.(providerImageCapabilityRouteProfileReader); ok {
		return reader.providerImageCapabilityRouteProfiles()
	}
	return nil
}

func backfillProviderImageCapabilityRoutesForProfile(store Store, profile providerImageCapabilityRouteProfile) {
	profile.withDefaults()
	if profile.ProviderType == "" || profile.PublicModel == "" || profile.UpstreamModel == "" {
		return
	}
	resources := store.ListProviderResources()
	for _, provider := range store.ListProviders() {
		if provider.Type != profile.ProviderType || provider.Status != StatusActive || !provider.Healthy ||
			!providerHasSupportedImageCapabilityResource(resources, provider.ID, profile) || providerImageCapabilityRouteBackfillDone(resources, provider.ID, profile) {
			continue
		}
		err := store.RunClusterOperation(context.Background(), providerImageCapabilityClusterKey(profile, provider.ID), func(context.Context) error {
			currentResources := store.ListProviderResources()
			if !providerHasSupportedImageCapabilityResource(currentResources, provider.ID, profile) || providerImageCapabilityRouteBackfillDone(currentResources, provider.ID, profile) {
				return nil
			}
			routeExists := false
			for _, route := range store.ListRoutes() {
				if providerImageCapabilityRouteMatches(route, provider.ID, profile) {
					routeExists = true
					break
				}
			}
			if !routeExists {
				if _, err := store.CreateRoute(defaultProviderImageCapabilityRoute(provider.ID, profile)); err != nil {
					return err
				}
			}
			for _, resource := range currentResources {
				if resource.ProviderID != provider.ID || strings.TrimSpace(resource.Options[profile.CapabilityOption]) != profile.CapabilitySupportedValue {
					continue
				}
				if _, err := store.UpdateProviderResourceOptions(resource.ID, map[string]string{
					profile.RouteBackfillOption: profile.RouteBackfillValue,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			log.Printf("[tokenhub] failed to backfill provider image route provider=%s model=%s: %v", provider.ID, profile.PublicModel, err)
		}
	}
}

func codexImageRouteBackfillDone(resources []ProviderResource, providerID string) bool {
	return providerImageCapabilityRouteBackfillDone(resources, providerID, codexImageCapabilityRouteProfile())
}

func providerImageCapabilityRouteBackfillDone(resources []ProviderResource, providerID string, profile providerImageCapabilityRouteProfile) bool {
	profile.withDefaults()
	for _, resource := range resources {
		if resource.ProviderID == providerID && resource.Options[profile.RouteBackfillOption] == profile.RouteBackfillValue {
			return true
		}
	}
	return false
}

func providerHasSupportedCodexImageResource(resources []ProviderResource, providerID string) bool {
	return providerHasSupportedImageCapabilityResource(resources, providerID, codexImageCapabilityRouteProfile())
}

func providerHasSupportedImageCapabilityResource(resources []ProviderResource, providerID string, profile providerImageCapabilityRouteProfile) bool {
	return providerImageCapabilityRouteHasSupportedResource(ModelRoute{ProviderID: providerID}, resources, profile)
}
