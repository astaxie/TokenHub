package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayResponsePostHooks(ctx context.Context, call CallContext, route RouteSelection, response any, protocol string) (any, error) {
	hooks := s.gatewayRouteHooksForRoute(pluginmeta.StageResponsePost, route, protocol, true)
	if len(hooks) == 0 {
		return response, nil
	}
	body, ok := marshalGatewayHookData(response)
	if !ok {
		return nil, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:       "v1",
			Protocol:      "gateway",
			RouteProtocol: protocol,
			Operation:     "response_post",
			Model:         call.Model.Name,
			RequestBody:   body,
		},
		Data: pluginmeta.GatewayHookData{
			pluginmeta.DataProviderResponse: body,
		},
	}
	if len(route.Route.ID) > 0 || len(route.Provider.ID) > 0 {
		if routeData, ok := marshalGatewayHookData(gatewayRouteCandidateViews([]RouteSelection{route})); ok {
			input.Envelope.Metadata = map[string]json.RawMessage{"route": routeData}
		}
	}
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageResponsePost, input, hooks)
	if err != nil {
		return nil, gatewayHookHTTPError(pluginmeta.StageResponsePost, err)
	}
	output := response
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataProviderResponse]
		if !ok {
			continue
		}
		var patched any
		if err := decodeGatewayPostHookResponsePayload(patch.Value, &patched, protocol); err != nil {
			return nil, err
		}
		output = patched
	}
	return output, nil
}

func (s *Server) runGatewayGuardrailPostHooks(ctx context.Context, call CallContext, route RouteSelection, response any, usage Usage, protocol string) (any, error) {
	hooks := s.gatewayRouteHooksForRoute(pluginmeta.StageGuardrailPost, route, protocol, true)
	if len(hooks) == 0 {
		return response, nil
	}
	body, ok := marshalGatewayHookData(response)
	if !ok {
		return nil, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:       "v1",
			Protocol:      "gateway",
			RouteProtocol: protocol,
			Operation:     "guardrail_post",
			Model:         call.Model.Name,
			RequestBody:   body,
		},
		Data: pluginmeta.GatewayHookData{
			pluginmeta.DataProviderResponse: body,
		},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataUsage:           usage,
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	if len(route.Route.ID) > 0 || len(route.Provider.ID) > 0 {
		if routeData, ok := marshalGatewayHookData(gatewayRouteCandidateViews([]RouteSelection{route})); ok {
			input.Envelope.Metadata = map[string]json.RawMessage{"route": routeData}
		}
	}
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageGuardrailPost, input, hooks)
	if err != nil {
		return nil, gatewayHookHTTPError(pluginmeta.StageGuardrailPost, err)
	}
	output := response
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataProviderResponse]
		if !ok {
			continue
		}
		var patched any
		if err := decodeGatewayPostHookResponsePayload(patch.Value, &patched, protocol); err != nil {
			return nil, err
		}
		output = patched
	}
	return output, nil
}

func (s *Server) runGatewayUsageAttributionHooks(ctx context.Context, call CallContext, route RouteSelection, response any, usage Usage, protocol string) (Usage, error) {
	hooks := s.gatewayRouteHooksForRoute(pluginmeta.StageUsageAttribution, route, protocol, true)
	if len(hooks) == 0 {
		return usage, nil
	}
	usageBody, ok := marshalGatewayHookData(usage)
	if !ok {
		return Usage{}, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:       "v1",
			Protocol:      "gateway",
			RouteProtocol: protocol,
			Operation:     "usage_attribution",
			Model:         call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{
			pluginmeta.DataUsage: usageBody,
		},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:      gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata:  call.Project,
		pluginmeta.DataAPIKeyMetadata:   gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataProviderResponse: response,
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	if len(route.Route.ID) > 0 || len(route.Provider.ID) > 0 {
		if routeData, ok := marshalGatewayHookData(gatewayRouteCandidateViews([]RouteSelection{route})); ok {
			input.Envelope.Metadata = map[string]json.RawMessage{"route": routeData}
		}
	}
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageUsageAttribution, input, hooks)
	if err != nil {
		return Usage{}, gatewayHookHTTPError(pluginmeta.StageUsageAttribution, err)
	}
	output := usage
	if usage.RetrievalEvidence != nil {
		evidence := *usage.RetrievalEvidence
		if evidence.Quantity != nil {
			quantity := *evidence.Quantity
			evidence.Quantity = &quantity
		}
		output.RetrievalEvidence = &evidence
	}
	if usage.AudioDurationSeconds != nil {
		seconds := *usage.AudioDurationSeconds
		output.AudioDurationSeconds = &seconds
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataUsage]
		if !ok {
			continue
		}
		if err := decodeGatewayHookPayload(patch.Value, &output, "gateway_hook_usage_invalid", "Gateway plugin returned invalid usage"); err != nil {
			return Usage{}, err
		}
	}
	output.DocumentCount = usage.DocumentCount
	if call.Model.Modality != "audio" {
		output.AudioDurationSeconds, output.AudioDurationSource = usage.AudioDurationSeconds, usage.AudioDurationSource
		output.AudioDurationRaw, output.AudioDurationUnit = usage.AudioDurationRaw, usage.AudioDurationUnit
	}
	if usage.AudioDurationSource != "" {
		output.AudioDurationRaw, output.AudioDurationUnit = usage.AudioDurationRaw, usage.AudioDurationUnit
	}
	if usage.AudioDurationSeconds != nil && output.AudioDurationSeconds == nil {
		return usage, NewHTTPError(502, "invalid_provider_usage", "Gateway plugin cannot erase reported audio duration")
	}
	if err := validateAudioDuration(output); err != nil {
		return usage, err
	}
	return output, nil
}
