package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinPluginActions(server *Server) {
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID: "tokenhub.provider.openai-codex",
		ActionID: "openai_codex.oauth.start",
		Kind:     pluginmeta.ActionKindExternalRedirect,
		Title:    "Start OpenAI Codex account OAuth",
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, NewHTTPError(http.StatusNotImplemented, "plugin_action_not_implemented", "Plugin action is not implemented yet")
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID: "tokenhub.provider.openai-codex",
		ActionID: "openai_codex.quota.read",
		Kind:     pluginmeta.ActionKindRead,
		Title:    "Read OpenAI Codex account quota",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"refresh":     map[string]any{"type": "boolean"},
			},
		},
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID string `json:"resource_id"`
			Refresh    bool   `json:"refresh"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		quota, err := server.queryOpenAIAccountQuotaCached(ctx, payload.ResourceID, payload.Refresh)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: quota}, nil
	}))
}

func mustRegisterPluginAction(actions *pluginmeta.ActionBroker, descriptor pluginmeta.ActionDescriptor, handler pluginmeta.ActionHandler) {
	if err := actions.Register(descriptor, handler); err != nil {
		panic(err)
	}
}
