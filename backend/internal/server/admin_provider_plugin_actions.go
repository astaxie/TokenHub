package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) executeProviderProbeAction(ctx context.Context, user AdminUser, providerID string) (any, bool, error) {
	provider, ok := s.providerByID(providerID)
	if !ok {
		return nil, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerPluginCapabilityAction(provider.Type, AdapterCapabilityProbe, "provider.probe.run")
	if !ok {
		return nil, false, nil
	}
	payload, err := json.Marshal(map[string]any{
		"provider_id": providerID,
	})
	if err != nil {
		return nil, true, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return nil, true, pluginActionHTTPError(err)
	}
	return result.Data, true, nil
}

func (s *Server) executeProviderResourceModelsAction(ctx context.Context, user AdminUser, resourceID string) (ProviderCatalogEntry, bool, error) {
	resource, ok := s.providerResourceByID(resourceID)
	if !ok {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
	}
	provider, ok := s.providerByID(resource.ProviderID)
	if !ok {
		return ProviderCatalogEntry{}, false, NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
	}
	pluginID, actionID, ok := s.providerPluginCapabilityAction(provider.Type, AdapterCapabilityModels, "models.read")
	if !ok {
		return ProviderCatalogEntry{}, false, nil
	}
	payload, err := json.Marshal(map[string]any{
		"resource_id": resourceID,
	})
	if err != nil {
		return ProviderCatalogEntry{}, true, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return ProviderCatalogEntry{}, true, pluginActionHTTPError(err)
	}
	catalog, ok := providerCatalogEntryFromActionData(result.Data)
	if !ok {
		return ProviderCatalogEntry{}, true, NewHTTPError(http.StatusInternalServerError, "provider_models_invalid_result", "Provider models action returned an invalid result")
	}
	return catalog, true, nil
}

func (s *Server) executeProviderCredentialModelsAction(ctx context.Context, user AdminUser, providerType string, credentials ProviderResourceCredentials) (ProviderCatalogEntry, bool, error) {
	pluginID, actionID, ok := s.providerPluginCapabilityAction(providerType, AdapterCapabilityModels, "models.preview")
	if !ok {
		return ProviderCatalogEntry{}, false, nil
	}
	payload, err := json.Marshal(credentials)
	if err != nil {
		return ProviderCatalogEntry{}, true, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor: pluginmeta.ActionActor{
			ID:   user.ID,
			Name: user.Name,
			Role: user.Role,
		},
		Payload: payload,
	})
	if err != nil {
		return ProviderCatalogEntry{}, true, pluginActionHTTPError(err)
	}
	catalog, ok := providerCatalogEntryFromActionData(result.Data)
	if !ok {
		return ProviderCatalogEntry{}, true, NewHTTPError(http.StatusInternalServerError, "provider_models_invalid_result", "Provider models preview action returned an invalid result")
	}
	return catalog, true, nil
}

func providerCatalogEntryFromActionData(data any) (ProviderCatalogEntry, bool) {
	if result, ok := data.(ProviderCatalogEntry); ok {
		return result, strings.TrimSpace(result.ID) != ""
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ProviderCatalogEntry{}, false
	}
	var result ProviderCatalogEntry
	if err := json.Unmarshal(raw, &result); err != nil {
		return ProviderCatalogEntry{}, false
	}
	return result, strings.TrimSpace(result.ID) != ""
}
