package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type providerImageCapabilityRouteProfile struct {
	ProviderType               string
	ResourceType               string
	PublicModel                string
	UpstreamModel              string
	CapabilityOption           string
	CapabilityCheckedAtOption  string
	CapabilitySupportedValue   string
	CapabilityUnsupportedValue string
	RouteBackfillOption        string
	RouteBackfillValue         string
}

func (s *Server) applyImageCapabilityActionSideEffects(ctx context.Context, descriptor pluginmeta.ActionDescriptor, payload json.RawMessage, result pluginmeta.ActionResult) (pluginmeta.ActionResult, error) {
	profile, ok := providerImageCapabilityRouteProfileFromAction(descriptor)
	if !ok {
		return result, nil
	}
	capability, ok := providerImageCapabilityResultFromActionData(result.Data)
	if !ok {
		return result, nil
	}
	resourceID, err := imageCapabilityActionResourceID(payload, capability.ResourceID)
	if err != nil {
		return result, err
	}
	if resourceID == "" {
		return result, NewHTTPError(http.StatusBadGateway, "provider_image_capability_missing_resource", "Provider image capability action did not identify a resource")
	}
	resource, ok := s.store.GetProviderResource(resourceID)
	if !ok {
		return result, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return result, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	if profile.ProviderType != "" && profile.ProviderType != provider.Type {
		return result, NewHTTPError(http.StatusForbidden, "plugin_action_subject_mismatch", "Plugin action subject does not match the Provider type")
	}
	if profile.ResourceType != "" && profile.ResourceType != resource.ResourceType {
		return result, NewHTTPError(http.StatusForbidden, "plugin_action_resource_type_mismatch", "Plugin action resource type does not match the Provider resource")
	}

	if !capability.Enabled {
		if err := s.setProviderImageCapabilityRoutesStatus(provider.ID, profile, StatusDisabled); err != nil {
			return result, err
		}
		return result, nil
	}

	if capability.Capability != "" {
		if _, err := s.updateProviderImageCapability(resource.ID, capability.Capability, profile); err != nil {
			return result, err
		}
	}
	if !profile.capabilityIsSupported(capability.Capability) {
		return result, nil
	}
	route, err := s.ensureProviderImageCapabilityRoute(provider.ID, profile)
	if err != nil {
		return result, err
	}
	capability.RouteID = route.ID
	result.Data = imageCapabilityActionResultWithRoute(result.Data, route.ID)
	return result, nil
}

func providerImageCapabilityRouteProfileFromAction(descriptor pluginmeta.ActionDescriptor) (providerImageCapabilityRouteProfile, bool) {
	profile := providerImageCapabilityRouteProfile{
		ProviderType:  strings.TrimSpace(descriptor.Subject),
		ResourceType:  strings.TrimSpace(firstNonEmpty(descriptor.Metadata["provider_resource_type"], descriptor.Metadata["resource_type"])),
		PublicModel:   strings.TrimSpace(descriptor.Metadata["public_model"]),
		UpstreamModel: strings.TrimSpace(descriptor.Metadata["upstream_model"]),
		CapabilityOption: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_option"],
			descriptor.Metadata["provider_capability_option"],
		)),
		CapabilityCheckedAtOption: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_checked_at_option"],
			descriptor.Metadata["provider_capability_checked_at_option"],
		)),
		CapabilitySupportedValue: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_supported_value"],
			descriptor.Metadata["supported_value"],
		)),
		CapabilityUnsupportedValue: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["capability_unsupported_value"],
			descriptor.Metadata["unsupported_value"],
		)),
		RouteBackfillOption: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_backfill_option"],
			descriptor.Metadata["provider_route_backfill_option"],
		)),
		RouteBackfillValue: strings.TrimSpace(firstNonEmpty(
			descriptor.Metadata["route_backfill_value"],
			descriptor.Metadata["provider_route_backfill_value"],
		)),
	}
	profile.withDefaults()
	return profile, profile.PublicModel != "" && profile.UpstreamModel != ""
}

func providerImageCapabilityRouteProfilesFromActions(actions []pluginmeta.ActionDescriptor) []providerImageCapabilityRouteProfile {
	profiles := make([]providerImageCapabilityRouteProfile, 0, len(actions))
	for _, action := range actions {
		if action.Capability != "image.capability.configure" {
			continue
		}
		profile, ok := providerImageCapabilityRouteProfileFromAction(action)
		if !ok {
			continue
		}
		profiles = append(profiles, profile)
	}
	return dedupeProviderImageCapabilityRouteProfiles(profiles)
}

type providerImageCapabilityProfileStore interface {
	setProviderImageCapabilityRouteProfiles([]providerImageCapabilityRouteProfile)
}

func (s *Server) syncProviderImageCapabilityRouteProfiles() {
	if s == nil || s.pluginActions == nil {
		return
	}
	store, ok := s.store.(providerImageCapabilityProfileStore)
	if !ok {
		return
	}
	store.setProviderImageCapabilityRouteProfiles(providerImageCapabilityRouteProfilesFromActions(s.pluginActions.List()))
}

func imageCapabilityActionResourceID(payload json.RawMessage, fallback string) (string, error) {
	resourceID := strings.TrimSpace(fallback)
	if resourceID != "" {
		return resourceID, nil
	}
	var request struct {
		ResourceID string `json:"resource_id"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &request); err != nil {
			return "", NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
		}
	}
	return strings.TrimSpace(request.ResourceID), nil
}

func imageCapabilityActionResultWithRoute(data any, routeID string) any {
	raw, err := json.Marshal(data)
	if err != nil {
		return data
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return data
	}
	result["route_id"] = routeID
	return result
}

func (s *Server) updateProviderImageCapability(resourceID string, capability string, profile providerImageCapabilityRouteProfile) (ProviderResource, error) {
	profile.withDefaults()
	options := map[string]string{
		profile.CapabilityOption:          capability,
		profile.CapabilityCheckedAtOption: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if profile.capabilityIsSupported(capability) {
		options[profile.RouteBackfillOption] = profile.RouteBackfillValue
	}
	return s.store.UpdateProviderResourceOptions(resourceID, options)
}

func (s *Server) ensureProviderImageCapabilityRoute(providerID string, profile providerImageCapabilityRouteProfile) (ModelRoute, error) {
	var disabled *ModelRoute
	for _, route := range s.store.ListRoutes() {
		if !providerImageCapabilityRouteMatches(route, providerID, profile) {
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
	return s.store.CreateRoute(defaultProviderImageCapabilityRoute(providerID, profile))
}

func (s *Server) setProviderImageCapabilityRoutesStatus(providerID string, profile providerImageCapabilityRouteProfile, status string) error {
	for _, route := range s.store.ListRoutes() {
		if !providerImageCapabilityRouteMatches(route, providerID, profile) || route.Status == status {
			continue
		}
		route.Status = status
		if _, err := s.store.UpdateRoute(route.ID, route); err != nil {
			return err
		}
	}
	return nil
}

func providerImageCapabilityRouteMatches(route ModelRoute, providerID string, profile providerImageCapabilityRouteProfile) bool {
	profile.withDefaults()
	return strings.TrimSpace(route.ModelName) == profile.PublicModel &&
		strings.TrimSpace(route.ProviderID) == strings.TrimSpace(providerID) &&
		strings.TrimSpace(route.ProviderModel) == profile.UpstreamModel
}

func defaultProviderImageCapabilityRoute(providerID string, profile providerImageCapabilityRouteProfile) ModelRoute {
	return ModelRoute{
		ModelName:     profile.PublicModel,
		ProviderID:    strings.TrimSpace(providerID),
		ProviderModel: profile.UpstreamModel,
		Priority:      1,
		Weight:        100,
		QualityScore:  50,
		CostScore:     50,
		Status:        StatusActive,
		Strategy:      RouteStrategyPriorityWeighted,
		ProjectScope:  RouteProjectScopeAll,
	}
}

func (p *providerImageCapabilityRouteProfile) withDefaults() {
	if p.CapabilityOption == "" {
		p.CapabilityOption = "image_capability"
	}
	if p.CapabilityCheckedAtOption == "" {
		p.CapabilityCheckedAtOption = "image_capability_checked_at"
	}
	if p.CapabilitySupportedValue == "" {
		p.CapabilitySupportedValue = "supported"
	}
	if p.CapabilityUnsupportedValue == "" {
		p.CapabilityUnsupportedValue = "unsupported"
	}
	if p.RouteBackfillOption == "" {
		p.RouteBackfillOption = "image_capability_route_backfill_v1"
	}
	if p.RouteBackfillValue == "" {
		p.RouteBackfillValue = "completed"
	}
}

func (p providerImageCapabilityRouteProfile) capabilityIsSupported(capability string) bool {
	p.withDefaults()
	return strings.TrimSpace(capability) == p.CapabilitySupportedValue
}

func (p providerImageCapabilityRouteProfile) capabilityIsUnsupported(capability string) bool {
	p.withDefaults()
	return strings.TrimSpace(capability) == p.CapabilityUnsupportedValue
}

func (p providerImageCapabilityRouteProfile) key() string {
	p.withDefaults()
	return strings.Join([]string{
		p.ProviderType,
		p.ResourceType,
		p.PublicModel,
		p.UpstreamModel,
		p.CapabilityOption,
		p.CapabilityCheckedAtOption,
		p.CapabilitySupportedValue,
		p.CapabilityUnsupportedValue,
		p.RouteBackfillOption,
		p.RouteBackfillValue,
	}, "\x00")
}

func dedupeProviderImageCapabilityRouteProfiles(profiles []providerImageCapabilityRouteProfile) []providerImageCapabilityRouteProfile {
	if len(profiles) == 0 {
		return nil
	}
	seen := map[string]bool{}
	deduped := make([]providerImageCapabilityRouteProfile, 0, len(profiles))
	for _, profile := range profiles {
		profile.withDefaults()
		key := profile.key()
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, profile)
	}
	return deduped
}

func providerImageCapabilityProfileMatchesResource(profile providerImageCapabilityRouteProfile, provider Provider, resource ProviderResource) bool {
	profile.withDefaults()
	if profile.ProviderType != "" && strings.TrimSpace(provider.Type) != profile.ProviderType {
		return false
	}
	if profile.ResourceType != "" && strings.TrimSpace(resource.ResourceType) != profile.ResourceType {
		return false
	}
	return true
}
