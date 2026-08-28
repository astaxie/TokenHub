package server

import (
	"context"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayProviderCallHooks(ctx context.Context, call CallContext, route RouteSelection, payload any, protocol string) (any, Usage, bool, error) {
	hooks := s.gatewayProviderCallHooksForRoute(route, protocol)
	if len(hooks) == 0 {
		return nil, Usage{}, false, nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return nil, Usage{}, false, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "provider_call",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:         gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata:     call.Project,
		pluginmeta.DataProviderCredentials: gatewayProviderCredentialsView(route),
		pluginmeta.DataProviderRequest:     body,
		pluginmeta.DataRouteCandidates:     gatewayRouteCandidateViews([]RouteSelection{route}),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageProviderCall, input, hooks)
	if err != nil {
		if pluginmeta.IsGatewayHookRouteSkipped(err) {
			return nil, Usage{}, false, &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusBadGateway, "gateway_hook_route_skipped", "Gateway plugin skipped this route"),
				Disposition: ProviderErrorRouteSkipped,
			}
		}
		return nil, Usage{}, false, &ProviderInvocationError{
			Err:         gatewayHookHTTPError(pluginmeta.StageProviderCall, err),
			Disposition: ProviderErrorPolicy,
		}
	}
	var response any
	var usage Usage
	handled := false
	for _, result := range report.Results {
		if patch, ok := result.Writes[pluginmeta.DataProviderResponse]; ok {
			if err := decodeGatewayHookPayload(patch.Value, &response, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response"); err != nil {
				return nil, Usage{}, false, &ProviderInvocationError{Err: err, Disposition: ProviderErrorPolicy}
			}
			handled = true
		}
		if patch, ok := result.Writes[pluginmeta.DataUsage]; ok {
			if err := decodeGatewayHookPayload(patch.Value, &usage, "gateway_hook_usage_invalid", "Gateway plugin returned invalid usage"); err != nil {
				return nil, Usage{}, false, &ProviderInvocationError{Err: err, Disposition: ProviderErrorPolicy}
			}
		}
	}
	if report.TerminalDecision == pluginmeta.HookDecisionShortCircuit && !handled {
		return nil, Usage{}, false, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway provider plugin did not return a response"),
			Disposition: ProviderErrorPolicy,
		}
	}
	return response, usage, handled, nil
}
