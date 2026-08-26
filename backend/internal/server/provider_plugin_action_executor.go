package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type providerPluginActionOptions struct {
	ApplySideEffects bool
	ResourceType     string
}

func pluginActionActor(user AdminUser) pluginmeta.ActionActor {
	return pluginmeta.ActionActor{
		ID:   user.ID,
		Name: user.Name,
		Role: user.Role,
	}
}

func (s *Server) executeRawPluginAction(ctx context.Context, user AdminUser, pluginID string, actionID string, payload json.RawMessage) (pluginmeta.ActionResult, error) {
	return s.pluginActions.Execute(ctx, pluginmeta.ActionInvocation{
		PluginID: pluginID,
		ActionID: actionID,
		Actor:    pluginActionActor(user),
		Payload:  payload,
	})
}

func (s *Server) executeEncodedPluginAction(ctx context.Context, user AdminUser, pluginID string, actionID string, payload any, opts providerPluginActionOptions) (pluginmeta.ActionResult, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusInternalServerError, "plugin_action_payload_failed", "Plugin action payload could not be encoded")
	}
	result, err := s.executeRawPluginAction(ctx, user, pluginID, actionID, rawPayload)
	if err != nil {
		return pluginmeta.ActionResult{}, pluginActionHTTPError(err)
	}
	if opts.ApplySideEffects {
		if descriptor, ok := s.pluginActions.Describe(pluginID, actionID); ok {
			result, err = s.applyPluginActionSideEffects(ctx, descriptor, rawPayload, result)
			if err != nil {
				return pluginmeta.ActionResult{}, err
			}
		}
	}
	return result, nil
}

func (s *Server) executeProviderCapabilityAction(ctx context.Context, user AdminUser, providerType string, capability AdapterCapability, actionCapability string, payload any, opts providerPluginActionOptions) (pluginmeta.ActionResult, bool, error) {
	action, ok := s.providerPluginCapabilityActionDescriptor(providerType, capability, actionCapability, opts.ResourceType)
	if !ok {
		return pluginmeta.ActionResult{}, false, nil
	}
	result, err := s.executeEncodedPluginAction(ctx, user, action.PluginID, action.ActionID, payload, opts)
	return result, true, err
}

func (s *Server) executeProviderPanelAction(ctx context.Context, user AdminUser, providerType string, panelID string, capability AdapterCapability, payload any, opts providerPluginActionOptions) (pluginmeta.ActionResult, bool, error) {
	pluginID, actionID, ok := s.providerResourcePanelAction(providerType, panelID, capability, opts.ResourceType)
	if !ok {
		return pluginmeta.ActionResult{}, false, nil
	}
	result, err := s.executeEncodedPluginAction(ctx, user, pluginID, actionID, payload, opts)
	return result, true, err
}

func providerPluginActionMatchesResourceType(action pluginmeta.ActionDescriptor, resourceType string) bool {
	expected := firstNonEmpty(action.Metadata["provider_resource_type"], action.Metadata["resource_type"])
	return strings.TrimSpace(expected) == "" || strings.TrimSpace(resourceType) == "" || strings.TrimSpace(expected) == strings.TrimSpace(resourceType)
}
