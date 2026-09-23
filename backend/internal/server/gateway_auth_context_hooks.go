package server

import (
	"context"
	"log"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayAuthContextHooks(ctx context.Context, call *CallContext, headers http.Header) error {
	if call == nil || !s.hasGatewayHookStage(pluginmeta.StageAuthContext) {
		return nil
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: "auth_context",
			Model:     call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(*call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRequestHeaders:  sanitizedGatewayHookHeaders(headers),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.runAuditedGatewayHookStage(ctx, *call, pluginmeta.StageAuthContext, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StageAuthContext, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataAuthContext]
		if !ok {
			continue
		}
		if err := applyGatewayAuthContextPatch(call, patch.Value); err != nil {
			if result.FailurePolicy == pluginmeta.FailurePolicyFailOpen {
				log.Printf("[tokenhub] gateway auth_context hook %s/%s returned invalid auth context for request %s: %v", result.PluginID, result.HookID, call.RequestID, err)
				continue
			}
			return err
		}
	}
	return nil
}
