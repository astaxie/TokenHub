package server

import (
	"context"
	"log"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayCacheLookupHooks(ctx context.Context, call CallContext, payload any) (any, Usage, bool, error) {
	if !s.hasGatewayHookStage(pluginmeta.StageCacheLookup) {
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
			Operation:   "cache_lookup",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageCacheLookup, input)
	if err != nil {
		return nil, Usage{}, false, gatewayHookHTTPError(pluginmeta.StageCacheLookup, err)
	}
	if report.TerminalDecision != pluginmeta.HookDecisionShortCircuit {
		return nil, Usage{}, false, nil
	}
	result := report.Results[len(report.Results)-1]
	responsePatch, ok := result.Writes[pluginmeta.DataProviderResponse]
	if !ok {
		return nil, Usage{}, false, NewHTTPError(http.StatusBadGateway, "gateway_hook_cache_hit_invalid", "Gateway cache plugin did not return a response")
	}
	var response any
	if err := decodeGatewayHookPayload(responsePatch.Value, &response, "gateway_hook_cache_hit_invalid", "Gateway cache plugin returned an invalid response"); err != nil {
		return nil, Usage{}, false, err
	}
	var usage Usage
	if usagePatch, ok := result.Writes[pluginmeta.DataUsage]; ok {
		if err := decodeGatewayHookPayload(usagePatch.Value, &usage, "gateway_hook_cache_hit_invalid", "Gateway cache plugin returned invalid usage"); err != nil {
			return nil, Usage{}, false, err
		}
	}
	return response, usage, true, nil
}

func (s *Server) runGatewayCacheWriteHooks(ctx context.Context, call CallContext, route RouteSelection, payload any, response any, usage Usage, protocol string) {
	hooks := s.gatewayRouteHooksForRoute(pluginmeta.StageCacheWrite, route, protocol, true)
	if len(hooks) == 0 {
		return
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		log.Printf("[tokenhub] gateway cache_write hooks skipped for request %s: input could not be encoded", call.RequestID)
		return
	}
	responseBody, ok := marshalGatewayHookData(response)
	if !ok {
		log.Printf("[tokenhub] gateway cache_write hooks skipped for request %s: response could not be encoded", call.RequestID)
		return
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "cache_write",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	input.Data[pluginmeta.DataProviderResponse] = responseBody
	if encodedUsage, ok := marshalGatewayHookData(usage); ok {
		input.Data[pluginmeta.DataUsage] = encodedUsage
	}
	if _, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageCacheWrite, input, hooks); err != nil {
		log.Printf("[tokenhub] gateway cache_write hooks failed for request %s: %v", call.RequestID, err)
	}
}
