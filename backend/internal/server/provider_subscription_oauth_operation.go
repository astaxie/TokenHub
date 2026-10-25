package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runSubscriptionOAuthAction(ctx context.Context, definition builtinSubscriptionDefinition, capability string, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
	operation := func(operationCtx context.Context) (pluginmeta.ActionResult, error) {
		if capability == "oauth.cancel" {
			return s.cancelSubscriptionOAuth(definition, invocation)
		}
		return s.exchangeSubscriptionOAuth(operationCtx, definition, invocation)
	}
	if definition.providerType != ProviderGitHubCopilotSubscription {
		return operation(ctx)
	}
	var input struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(invocation.Payload, &input) != nil {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_plugin_action_payload", "Plugin action payload is invalid")
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return pluginmeta.ActionResult{}, NewHTTPError(400, "invalid_oauth_state", "Subscription OAuth session is invalid or expired")
	}
	// Device polling temporarily consumes the session and restores pending polls.
	// Serialize cancellation with that whole transition so it cannot miss the
	// consumed session and leave a later restored authorization active.
	var result pluginmeta.ActionResult
	err := s.store.RunClusterOperation(ctx, "subscription-oauth:"+definition.providerType+":"+strings.TrimSpace(input.SessionID), func(leaseCtx context.Context) error {
		if err := leaseCtx.Err(); err != nil {
			return err
		}
		var err error
		result, err = operation(leaseCtx)
		return err
	})
	// Cancellation may make database/sql roll back lease acquisition before its
	// transaction finishes, reporting ErrTxDone instead of the caller's cause.
	if errors.Is(err, sql.ErrTxDone) && ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, err
}
