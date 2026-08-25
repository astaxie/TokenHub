package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func registerBuiltinPluginActions(server *Server) {
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.oauth.start",
		Kind:       pluginmeta.ActionKindExternalRedirect,
		Title:      "Start OpenAI Codex account OAuth",
		Capability: "oauth.start",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"redirect_uri": map[string]any{"type": "string"},
				"return_url":   map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"auth_url", "session_id", "state", "redirect_uri", "expires_at"}, map[string]string{
			"auth_url":     "string",
			"session_id":   "string",
			"state":        "string",
			"redirect_uri": "string",
			"expires_at":   "string",
		}),
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload providerAccountOAuthGenerateRequest
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		response, err := server.generateOpenAIAccountOAuth(payload, nil)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: response, RedirectURL: response.AuthURL}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.oauth.exchange",
		Kind:       pluginmeta.ActionKindMutate,
		Title:      "Exchange OpenAI Codex account OAuth code",
		Capability: "oauth.exchange",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"session_id", "state", "code"},
			"properties": map[string]any{
				"session_id":   map[string]any{"type": "string"},
				"state":        map[string]any{"type": "string"},
				"code":         map[string]any{"type": "string"},
				"redirect_uri": map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema(nil, map[string]string{
			"access_token":    "string",
			"refresh_token":   "string",
			"id_token":        "string",
			"client_id":       "string",
			"scopes":          "string",
			"token_type":      "string",
			"expires_in":      "integer",
			"expires_at":      "string",
			"account_email":   "string",
			"account_id":      "string",
			"user_id":         "string",
			"organization_id": "string",
			"plan_type":       "string",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload providerAccountOAuthExchangeRequest
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		info, err := server.exchangeOpenAIAccountOAuth(ctx, payload)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: info}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.quota.read",
		Kind:       pluginmeta.ActionKindRead,
		Title:      "Read OpenAI Codex account quota",
		Capability: "quota.read",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"refresh":     map[string]any{"type": "boolean"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"fetched_at"}, map[string]string{
			"user_id":    "string",
			"account_id": "string",
			"email":      "string",
			"plan_type":  "string",
			"fetched_at": "integer",
		}),
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
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.quota.reset_credits.read",
		Kind:       pluginmeta.ActionKindRead,
		Title:      "Read OpenAI Codex quota reset credits",
		Capability: "quota.reset_credits.read",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"available_count", "credits", "fetched_at"}, map[string]string{
			"available_count":   "integer",
			"credits":           "array",
			"fetched_at":        "integer",
			"pending_operation": "object",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID string `json:"resource_id"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		credits, err := server.queryOpenAIAccountQuotaResetCredits(ctx, payload.ResourceID)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: credits}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.quota.reset",
		Kind:       pluginmeta.ActionKindMutate,
		Title:      "Reset OpenAI Codex quota",
		Capability: "quota.reset",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id", "confirm", "idempotency_key", "expected_available_count", "credit_id", "danger_confirmation"},
			"properties": map[string]any{
				"resource_id":              map[string]any{"type": "string"},
				"confirm":                  map[string]any{"type": "boolean"},
				"idempotency_key":          map[string]any{"type": "string"},
				"expected_available_count": map[string]any{"type": "integer"},
				"credit_id":                map[string]any{"type": "string"},
				"danger_confirmation":      map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"code", "windows_reset"}, map[string]string{
			"code":          "string",
			"windows_reset": "integer",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID             string `json:"resource_id"`
			Confirm                bool   `json:"confirm"`
			IdempotencyKey         string `json:"idempotency_key"`
			ExpectedAvailableCount int    `json:"expected_available_count"`
			CreditID               string `json:"credit_id"`
			DangerConfirmation     string `json:"danger_confirmation"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		if strings.TrimSpace(payload.DangerConfirmation) != openAIAccountQuotaResetDangerValue {
			return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "quota_reset_danger_confirmation_required", "The dangerous operation confirmation header is required")
		}
		creditID := payload.CreditID
		req := openAIAccountQuotaResetRequest{
			Confirm:                payload.Confirm,
			IdempotencyKey:         payload.IdempotencyKey,
			ExpectedAvailableCount: &payload.ExpectedAvailableCount,
			CreditID:               &creditID,
		}
		if err := validateOpenAIAccountQuotaResetRequest(req, payload.IdempotencyKey); err != nil {
			return pluginmeta.ActionResult{}, err
		}
		result, err := server.resetOpenAIAccountQuota(ctx, payload.ResourceID, req)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: result}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.probe.run",
		Kind:       pluginmeta.ActionKindTest,
		Title:      "Test OpenAI Codex account resource",
		Capability: "probe.run",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id":      map[string]any{"type": "string"},
				"model":            map[string]any{"type": "string"},
				"reasoning_effort": map[string]any{"type": "string"},
				"speed":            map[string]any{"type": "string"},
				"prompt":           map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"resource_id", "model", "output_text", "latency_ms"}, map[string]string{
			"resource_id":           "string",
			"model":                 "string",
			"reasoning_effort":      "string",
			"speed":                 "string",
			"upstream_service_tier": "string",
			"output_text":           "string",
			"usage":                 "object",
			"latency_ms":            "integer",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID      string `json:"resource_id"`
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			Speed           string `json:"speed"`
			Prompt          string `json:"prompt"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		result, err := server.integrations.TestProviderResource(ctx, payload.ResourceID, &ProviderProbeRequest{
			Model:           payload.Model,
			ReasoningEffort: payload.ReasoningEffort,
			Speed:           payload.Speed,
			Prompt:          payload.Prompt,
		})
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: result}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.provider.probe.run",
		Kind:       pluginmeta.ActionKindTest,
		Title:      "Test OpenAI Codex provider",
		Capability: "provider.probe.run",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"provider_id"},
			"properties": map[string]any{
				"provider_id": map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"provider_id", "healthy", "succeeded", "failed"}, map[string]string{
			"provider_id": "string",
			"healthy":     "boolean",
			"succeeded":   "integer",
			"failed":      "integer",
			"results":     "array",
			"errors":      "array",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ProviderID string `json:"provider_id"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		result, err := server.integrations.TestProvider(ctx, payload.ProviderID)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: result}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.models.read",
		Kind:       pluginmeta.ActionKindRead,
		Title:      "Read OpenAI Codex resource models",
		Capability: "models.read",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"id", "models_count", "models"}, map[string]string{
			"id":              "string",
			"name":            "string",
			"display_name":    "string",
			"type":            "string",
			"base_url":        "string",
			"doc_url":         "string",
			"categories":      "array",
			"category_counts": "object",
			"models_count":    "integer",
			"source":          "string",
			"etag":            "string",
			"models":          "array",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID string `json:"resource_id"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		catalog, err := server.queryOpenAICodexModels(ctx, payload.ResourceID)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: catalog}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.models.preview",
		Kind:       pluginmeta.ActionKindRead,
		Title:      "Preview OpenAI Codex models with credentials",
		Capability: "models.preview",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"access_token":    map[string]any{"type": "string"},
				"account_id":      map[string]any{"type": "string"},
				"organization_id": map[string]any{"type": "string"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"id", "models_count", "models"}, map[string]string{
			"id":              "string",
			"name":            "string",
			"display_name":    "string",
			"type":            "string",
			"base_url":        "string",
			"doc_url":         "string",
			"categories":      "array",
			"category_counts": "object",
			"models_count":    "integer",
			"source":          "string",
			"etag":            "string",
			"models":          "array",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var credentials ProviderResourceCredentials
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &credentials); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		catalog, err := server.codexSubscription.ModelsWithCredentials(ctx, credentials)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: catalog}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Title:      "Configure OpenAI Codex image capability",
		Capability: "image.capability.configure",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id", "enabled"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"enabled":     map[string]any{"type": "boolean"},
			},
		},
		OutputSchema: actionObjectSchema([]string{"enabled", "tested", "resource_id"}, map[string]string{
			"enabled":     "boolean",
			"tested":      "boolean",
			"capability":  "string",
			"resource_id": "string",
			"route_id":    "string",
		}),
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID string `json:"resource_id"`
			Enabled    bool   `json:"enabled"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		result, err := server.configureCodexImageCapability(ctx, payload.ResourceID, payload.Enabled)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: result}, nil
	}))
	mustRegisterPluginAction(server.pluginActions, pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.openai-codex",
		ActionID:   "openai_codex.credentials.refresh",
		Kind:       pluginmeta.ActionKindMutate,
		Title:      "Refresh OpenAI Codex account credentials",
		Capability: "credentials.refresh",
		Subject:    ProviderOpenAICodex,
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"resource_id"},
			"properties": map[string]any{
				"resource_id": map[string]any{"type": "string"},
				"force":       map[string]any{"type": "boolean"},
			},
		},
		OutputSchema: map[string]any{
			"type":     "object",
			"required": []string{"credential_summary"},
			"properties": map[string]any{
				"credential_summary": map[string]any{"type": "object"},
			},
		},
	}, pluginmeta.ActionHandlerFunc(func(ctx context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID string `json:"resource_id"`
			Force      bool   `json:"force"`
		}
		if len(invocation.Payload) > 0 {
			if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
				return pluginmeta.ActionResult{}, NewHTTPError(http.StatusBadRequest, "invalid_plugin_action_payload", "Plugin action payload is invalid")
			}
		}
		credentials, err := server.store.RefreshProviderResourceCredentials(ctx, payload.ResourceID, payload.Force)
		if err != nil {
			return pluginmeta.ActionResult{}, err
		}
		return pluginmeta.ActionResult{Data: map[string]any{"credential_summary": providerAccountCredentialSummary(credentials)}}, nil
	}))
}

func mustRegisterPluginAction(actions *pluginmeta.ActionBroker, descriptor pluginmeta.ActionDescriptor, handler pluginmeta.ActionHandler) {
	if err := actions.Register(descriptor, handler); err != nil {
		panic(err)
	}
}

func actionObjectSchema(required []string, properties map[string]string) map[string]any {
	schemaProperties := map[string]any{}
	for name, valueType := range properties {
		schemaProperties[name] = map[string]any{"type": valueType}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": schemaProperties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
