package server

import (
	"encoding/json"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func sensitiveGatewayHookCall(call CallContext) bool {
	return call.Model.Modality == "rerank" || audioTranscriptionEndpoint([]string{call.RouteProtocol})
}

// Hook failures can echo request or response content before the endpoint finisher
// runs, including failures logged by fail-open route-rank and cache-write stages.
func sanitizeSensitiveGatewayHookError(call CallContext, err error) error {
	if err == nil || !sensitiveGatewayHookCall(call) {
		return err
	}
	// Preserve control flow while discarding any plugin message wrapped with it.
	if pluginmeta.IsGatewayHookDenied(err) {
		return pluginmeta.ErrGatewayHookDenied
	}
	if pluginmeta.IsGatewayHookRouteSkipped(err) {
		return pluginmeta.ErrGatewayHookRouteSkipped
	}
	if pluginmeta.IsGatewayHookShortCircuited(err) {
		return pluginmeta.ErrGatewayHookShortCircuited
	}
	if call.Model.Modality == "rerank" {
		return sanitizeRerankProviderError(err)
	}
	return sanitizeTranscriptionProviderError(err)
}

func gatewayHookAuditEventForCall(call CallContext, raw json.RawMessage) any {
	if sensitiveGatewayHookCall(call) {
		return map[string]any{"omitted": true, "reason": "sensitive_endpoint_payload", "bytes": len(raw)}
	}
	return decodeGatewayPluginAuditEvent(raw)
}
