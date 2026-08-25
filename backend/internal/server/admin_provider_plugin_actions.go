package server

import (
	"context"
	"encoding/json"
	"net/http"

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
