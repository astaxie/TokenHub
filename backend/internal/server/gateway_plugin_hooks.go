package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type gatewayRouteCandidateView struct {
	RouteID          string `json:"route_id"`
	ProviderID       string `json:"provider_id,omitempty"`
	ProviderType     string `json:"provider_type,omitempty"`
	ProviderModel    string `json:"provider_model,omitempty"`
	ResourceID       string `json:"resource_id,omitempty"`
	RoutePriority    int    `json:"route_priority"`
	ResourcePriority int    `json:"resource_priority"`
	Weight           int    `json:"weight"`
	Strategy         string `json:"strategy,omitempty"`
}

type gatewayRouteOrderPatch struct {
	RouteIDs []string `json:"route_ids"`
}

func (s *Server) runGatewayAuthContextHooks(ctx context.Context, call *CallContext, headers http.Header) error {
	if s == nil || call == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageAuthContext)) == 0 {
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageAuthContext, input)
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

func (s *Server) runGatewayCacheLookupHooks(ctx context.Context, call CallContext, payload any) (any, Usage, bool, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageCacheLookup)) == 0 {
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageCacheLookup, input)
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

func (s *Server) runGatewayCacheWriteHooks(ctx context.Context, call CallContext, payload any, response any, usage Usage) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageCacheWrite)) == 0 {
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
	if _, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageCacheWrite, input); err != nil {
		log.Printf("[tokenhub] gateway cache_write hooks failed for request %s: %v", call.RequestID, err)
	}
}

func (s *Server) runGatewayResponsePostHooks(ctx context.Context, call CallContext, route RouteSelection, response any) (any, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageResponsePost)) == 0 {
		return response, nil
	}
	body, ok := marshalGatewayHookData(response)
	if !ok {
		return nil, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "response_post",
			Model:       call.Model.Name,
			RequestBody: body,
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageResponsePost, input)
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
		if err := decodeGatewayHookPayload(patch.Value, &patched, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response"); err != nil {
			return nil, err
		}
		output = patched
	}
	return output, nil
}

func (s *Server) runGatewayGuardrailPostHooks(ctx context.Context, call CallContext, route RouteSelection, response any, usage Usage) (any, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageGuardrailPost)) == 0 {
		return response, nil
	}
	body, ok := marshalGatewayHookData(response)
	if !ok {
		return nil, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "guardrail_post",
			Model:       call.Model.Name,
			RequestBody: body,
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageGuardrailPost, input)
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
		if err := decodeGatewayHookPayload(patch.Value, &patched, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response"); err != nil {
			return nil, err
		}
		output = patched
	}
	return output, nil
}

func (s *Server) runGatewayUsageAttributionHooks(ctx context.Context, call CallContext, route RouteSelection, response any, usage Usage) (Usage, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageUsageAttribution)) == 0 {
		return usage, nil
	}
	usageBody, ok := marshalGatewayHookData(usage)
	if !ok {
		return Usage{}, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: "usage_attribution",
			Model:     call.Model.Name,
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageUsageAttribution, input)
	if err != nil {
		return Usage{}, gatewayHookHTTPError(pluginmeta.StageUsageAttribution, err)
	}
	output := usage
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataUsage]
		if !ok {
			continue
		}
		if err := decodeGatewayHookPayload(patch.Value, &output, "gateway_hook_usage_invalid", "Gateway plugin returned invalid usage"); err != nil {
			return Usage{}, err
		}
	}
	return output, nil
}

func (s *Server) runGatewayAdmissionHooks(ctx context.Context, call CallContext, headers http.Header, payload any, tokenReservation int64) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageAdmission)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "admission",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	if requestHeaders, ok := marshalGatewayHookData(sanitizedGatewayHookHeaders(headers)); ok {
		input.Data[pluginmeta.DataRequestHeaders] = requestHeaders
	}
	if reservation, ok := marshalGatewayHookData(Usage{TotalTokens: maxInt64(tokenReservation, 0)}); ok {
		input.Data[pluginmeta.DataUsage] = reservation
	}
	if _, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageAdmission, input); err != nil {
		return gatewayHookHTTPError(pluginmeta.StageAdmission, err)
	}
	return nil
}

func (s *Server) runGatewayDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageDecodeNormalize)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "decode_normalize",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: pluginmeta.GatewayHookData{
			pluginmeta.DataRequestBody: body,
		},
	}
	if requestHeaders, ok := marshalGatewayHookData(sanitizedGatewayHookHeaders(headers)); ok {
		input.Data[pluginmeta.DataRequestHeaders] = requestHeaders
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageDecodeNormalize, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StageDecodeNormalize, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRequestBody]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayChatDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *ChatCompletionRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayResponsesDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *ResponsesRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ResponsesRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayEmbeddingsDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *EmbeddingsRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applyEmbeddingsGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayCompactDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, request map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	body := cloneRawJSON(request, 0)
	err := s.runGatewayDecodeNormalizeHooks(ctx, call, headers, body, func(data json.RawMessage) error {
		originalModel := compactRequestModel(body)
		var patched map[string]json.RawMessage
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if patchedModel := compactRequestModel(patched); patchedModel != originalModel {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
		}
		body = cloneRawJSON(patched, 0)
		return nil
	})
	return body, err
}

func (s *Server) runGatewayAnthropicDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *anthropicMessagesRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, req.Raw, func(data json.RawMessage) error {
		return applyAnthropicGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayGeminiDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, payload *map[string]any, model string, stream bool) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *payload, func(data json.RawMessage) error {
		return applyGeminiGatewayRequestPatch(payload, data, model, stream)
	})
}

func (s *Server) runGatewayAnthropicPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *anthropicMessagesRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, req.Raw, func(data json.RawMessage) error {
		return applyAnthropicGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayGeminiPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, payload *map[string]any, model string, stream bool) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *payload, func(data json.RawMessage) error {
		return applyGeminiGatewayRequestPatch(payload, data, model, stream)
	})
}

func (s *Server) runGatewayEmbeddingsPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *EmbeddingsRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applyEmbeddingsGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StagePrivacyPre)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "privacy_pre",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	if requestHeaders, ok := marshalGatewayHookData(sanitizedGatewayHookHeaders(headers)); ok {
		input.Data[pluginmeta.DataRequestHeaders] = requestHeaders
	}
	input.Data[pluginmeta.DataRequestBody] = body
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StagePrivacyPre, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StagePrivacyPre, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRequestBody]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayGuardrailPreHooks(ctx context.Context, call CallContext, payload any, targets []guardrailTextTarget, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageGuardrailPre)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	segments := gatewayTextSegmentsFromGuardrailTargets(targets)
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:        "v1",
			Protocol:       "gateway",
			Operation:      "guardrail_pre",
			Model:          call.Model.Name,
			RequestBody:    body,
			NormalizedText: segments,
		},
		Data: gatewayHookCallData(call, body),
	}
	if encoded, ok := marshalGatewayHookData(segments); ok {
		input.Data[pluginmeta.DataNormalizedText] = encoded
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageGuardrailPre, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StageGuardrailPre, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRequestBody]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayChatGuardrailPreHooks(ctx context.Context, call CallContext, req *ChatCompletionRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *req, chatGuardrailTargets(req), func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayResponsesGuardrailPreHooks(ctx context.Context, call CallContext, req *ResponsesRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *req, responsesGuardrailTargets(req), func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ResponsesRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayEmbeddingsGuardrailPreHooks(ctx context.Context, call CallContext, req *EmbeddingsRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *req, embeddingsGuardrailTargets(req), func(data json.RawMessage) error {
		return applyEmbeddingsGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayCompactGuardrailPreHooks(ctx context.Context, call CallContext, request map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	body := cloneRawJSON(request, 0)
	err := s.runGatewayGuardrailPreHooks(ctx, call, body, responsesCompactGuardrailTargets(body), func(data json.RawMessage) error {
		originalModel := compactRequestModel(body)
		var patched map[string]json.RawMessage
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if patchedModel := compactRequestModel(patched); patchedModel != originalModel {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
		}
		body = cloneRawJSON(patched, 0)
		return nil
	})
	return body, err
}

func (s *Server) runGatewayAnthropicGuardrailPreHooks(ctx context.Context, call CallContext, req *anthropicMessagesRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, req.Raw, anthropicGuardrailTargets(req), func(data json.RawMessage) error {
		return applyAnthropicGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayGeminiGuardrailPreHooks(ctx context.Context, call CallContext, payload *map[string]any, model string, stream bool) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *payload, geminiGuardrailTargets(*payload), func(data json.RawMessage) error {
		return applyGeminiGatewayRequestPatch(payload, data, model, stream)
	})
}

func (s *Server) runGatewayAnthropicContextOptimizeHooks(ctx context.Context, call CallContext, req *anthropicMessagesRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, req.Raw, func(data json.RawMessage) error {
		return applyAnthropicGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayGeminiContextOptimizeHooks(ctx context.Context, call CallContext, payload *map[string]any, model string, stream bool) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *payload, func(data json.RawMessage) error {
		return applyGeminiGatewayRequestPatch(payload, data, model, stream)
	})
}

func (s *Server) runGatewayContextOptimizeHooks(ctx context.Context, call CallContext, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageContextOptimize)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "context_optimize",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageContextOptimize, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StageContextOptimize, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRequestBody]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayChatContextOptimizeHooks(ctx context.Context, call CallContext, req *ChatCompletionRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayResponsesContextOptimizeHooks(ctx context.Context, call CallContext, req *ResponsesRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ResponsesRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayEmbeddingsContextOptimizeHooks(ctx context.Context, call CallContext, req *EmbeddingsRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		return applyEmbeddingsGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageRequestTransform)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "request_transform",
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageRequestTransform, input)
	if err != nil {
		if pluginmeta.IsGatewayHookRouteSkipped(err) {
			return &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusBadGateway, "gateway_hook_route_skipped", "Gateway plugin skipped this route"),
				Disposition: ProviderErrorRouteSkipped,
			}
		}
		return &ProviderInvocationError{
			Err:         gatewayHookHTTPError(pluginmeta.StageRequestTransform, err),
			Disposition: ProviderErrorPolicy,
		}
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataProviderRequest]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayChatRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *ChatCompletionRequest) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayResponsesRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *ResponsesRequest) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ResponsesRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayEmbeddingsRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *EmbeddingsRequest) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		var patched EmbeddingsRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if patched.Model != originalModel {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayCompactRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, request map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	body := cloneRawJSON(request, 0)
	err := s.runGatewayRequestTransformHooks(ctx, call, route, body, func(data json.RawMessage) error {
		var patched map[string]json.RawMessage
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		body = cloneRawJSON(patched, 0)
		return nil
	})
	return body, err
}

func (s *Server) runGatewayProviderCallHooks(ctx context.Context, call CallContext, route RouteSelection, payload any) (any, Usage, bool, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageProviderCall)) == 0 {
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
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageProviderCall, input)
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

func (s *Server) runGatewayRouteCandidatesHooks(ctx context.Context, call CallContext, routes []RouteSelection) ([]RouteSelection, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(routes) == 0 || len(s.gatewayChain.Hooks(pluginmeta.StageRouteCandidates)) == 0 {
		return routes, nil
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: "route_candidates",
			Model:     call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRouteCandidates: gatewayRouteCandidateViews(routes),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageRouteCandidates, input)
	if err != nil {
		return nil, gatewayHookHTTPError(pluginmeta.StageRouteCandidates, err)
	}
	selected := routes
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRouteCandidates]
		if !ok {
			continue
		}
		next, err := applyGatewayRouteCandidatesPatch(selected, patch.Value)
		if err != nil {
			if result.FailurePolicy == pluginmeta.FailurePolicyFailOpen {
				log.Printf("[tokenhub] gateway route_candidates hook %s/%s returned invalid candidates for request %s: %v", result.PluginID, result.HookID, call.RequestID, err)
				continue
			}
			return nil, NewHTTPError(http.StatusBadGateway, "gateway_hook_route_candidates_invalid", "Gateway routing plugin returned invalid route candidates")
		}
		selected = next
	}
	if len(selected) == 0 {
		return nil, ErrProviderMissing
	}
	return selected, nil
}

func (s *Server) runGatewayRouteRankHooks(ctx context.Context, call CallContext, planned []RouteSelection) []RouteSelection {
	if s == nil || s.gatewayHooks == nil || len(planned) < 2 {
		return planned
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: "route_rank",
			Model:     call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRouteCandidates: gatewayRouteCandidateViews(planned),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageRouteRank, input)
	if err != nil {
		log.Printf("[tokenhub] gateway route_rank hooks failed for request %s: %v", call.RequestID, err)
		return planned
	}
	ordered := planned
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRouteCandidates]
		if !ok {
			continue
		}
		next, err := applyGatewayRouteOrderPatch(ordered, patch.Value)
		if err != nil {
			log.Printf("[tokenhub] gateway route_rank hook %s/%s returned an invalid route order for request %s: %v", result.PluginID, result.HookID, call.RequestID, err)
			continue
		}
		ordered = next
	}
	return ordered
}

func (s *Server) runGatewayTraceExportHooks(ctx context.Context, completion GatewayCallCompletion) {
	if s == nil || s.gatewayHooks == nil {
		return
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: completion.Call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: string(completion.Kind),
			Model:     completion.Call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	if usage, ok := marshalGatewayHookData(completion.Usage); ok {
		input.Data[pluginmeta.DataUsage] = usage
	}
	if audit, ok := marshalGatewayHookData(gatewayTraceExportAuditView(completion)); ok {
		input.Data[pluginmeta.DataAudit] = audit
	}
	if len(input.Data) == 0 {
		input.Data = nil
	}
	if _, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageTraceExport, input); err != nil {
		log.Printf("[tokenhub] gateway trace_export hooks failed for request %s: %v", completion.Call.RequestID, err)
	}
}

func gatewayTraceExportAuditView(completion GatewayCallCompletion) map[string]any {
	return map[string]any{
		"kind":        completion.Kind,
		"request_id":  completion.Call.RequestID,
		"project_id":  completion.Call.Project.ID,
		"api_key_id":  completion.Call.Key.ID,
		"model":       completion.Call.Model.Name,
		"stream":      completion.Call.Stream,
		"route_id":    completion.Route.Route.ID,
		"provider_id": completion.Route.Provider.ID,
		"status_code": completion.StatusCode,
		"error_code":  completion.ErrorCode,
		"finished_at": completion.FinishedAt,
	}
}

func gatewayHookCallData(call CallContext, requestBody json.RawMessage) pluginmeta.GatewayHookData {
	data := pluginmeta.GatewayHookData{}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRequestBody:     requestBody,
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			data[dataClass] = encoded
		}
	}
	return data
}

func gatewayProviderCredentialsView(route RouteSelection) map[string]any {
	view := map[string]any{
		"provider": map[string]any{
			"id":                route.Provider.ID,
			"type":              route.Provider.Type,
			"base_url":          route.Provider.BaseURL,
			"api_key":           route.Provider.APIKey,
			"headers":           cloneStringMap(route.Provider.Headers),
			"sensitive_headers": append([]string(nil), route.Provider.SensitiveHeaders...),
			"options":           cloneStringMap(route.Provider.Options),
		},
		"provider_model": route.ProviderModel,
	}
	if route.Resource != nil {
		view["resource"] = map[string]any{
			"id":                route.Resource.ID,
			"type":              route.Resource.ResourceType,
			"base_url":          route.Resource.BaseURL,
			"api_key":           route.Resource.APIKey,
			"headers":           cloneStringMap(route.Resource.Headers),
			"sensitive_headers": append([]string(nil), route.Resource.SensitiveHeaders...),
			"options":           cloneStringMap(route.Resource.Options),
		}
	}
	return view
}

func marshalGatewayHookData(value any) (json.RawMessage, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return data, true
}

func sanitizedGatewayHookHeaders(headers http.Header) map[string][]string {
	sanitized := map[string][]string{}
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if sensitiveGatewayHookHeader(canonical) {
			sanitized[canonical] = []string{"[redacted]"}
			continue
		}
		sanitized[canonical] = append([]string(nil), values...)
	}
	return sanitized
}

func sensitiveGatewayHookHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization", "X-Api-Key":
		return true
	default:
		return false
	}
}

func gatewayHookHTTPError(stage pluginmeta.GatewayHookStage, err error) error {
	if pluginmeta.IsGatewayHookDenied(err) {
		return NewHTTPError(http.StatusForbidden, "gateway_hook_denied", fmt.Sprintf("Request blocked by the %s plugin stage", stage))
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "internal_error" {
		return httpErr
	}
	return NewHTTPError(http.StatusInternalServerError, "gateway_hook_failed", fmt.Sprintf("Gateway plugin stage %s failed", stage))
}

func decodeGatewayHookRequestPatch(data json.RawMessage, target any) error {
	return decodeGatewayHookPayload(data, target, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
}

func decodeGatewayHookPayload(data json.RawMessage, target any, code string, message string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return NewHTTPError(http.StatusBadGateway, code, message)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return NewHTTPError(http.StatusBadGateway, code, "Gateway plugin payload must contain a single JSON value")
	}
	return nil
}

func validateGatewayHookRequestInvariant(originalModel string, originalStream bool, patchedModel string, patchedStream bool) error {
	if patchedModel != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	if patchedStream != originalStream {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested stream mode")
	}
	return nil
}

func gatewayTextSegmentsFromGuardrailTargets(targets []guardrailTextTarget) []pluginmeta.TextSegment {
	segments := make([]pluginmeta.TextSegment, 0, len(targets))
	for _, target := range targets {
		if target.fragment.ID == "" && target.fragment.Text == "" {
			continue
		}
		segments = append(segments, pluginmeta.TextSegment{
			ID:   target.fragment.ID,
			Text: target.fragment.Text,
		})
	}
	return segments
}

func compactRequestModel(request map[string]json.RawMessage) string {
	var model string
	if request != nil {
		_ = json.Unmarshal(request["model"], &model)
	}
	return strings.TrimSpace(model)
}

func applyAnthropicGatewayRequestPatch(req *anthropicMessagesRequest, data json.RawMessage) error {
	if req == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	originalModel := req.Model
	originalStream := req.Stream
	var patched map[string]any
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	model, _ := patched["model"].(string)
	if strings.TrimSpace(model) != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	stream, _ := patched["stream"].(bool)
	if stream != originalStream {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested stream mode")
	}
	messages, ok := patched["messages"].([]any)
	if !ok || len(messages) == 0 {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	req.Raw = patched
	req.Messages = messages
	req.MaxTokens = int(int64FromAny(patched["max_tokens"]))
	return nil
}

func applyGeminiGatewayRequestPatch(payload *map[string]any, data json.RawMessage, model string, stream bool) error {
	if payload == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	var patched map[string]any
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	if patchedModel, ok := patched["model"].(string); ok && strings.TrimSpace(patchedModel) != "" && strings.TrimSpace(patchedModel) != model {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	if patchedStream, ok := patched["stream"].(bool); ok && patchedStream != stream {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested stream mode")
	}
	if _, ok := patched["contents"].([]any); !ok {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	*payload = patched
	return nil
}

func applyEmbeddingsGatewayRequestPatch(req *EmbeddingsRequest, data json.RawMessage) error {
	if req == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	originalModel := req.Model
	var patched EmbeddingsRequest
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	if strings.TrimSpace(patched.Model) != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	*req = patched
	return nil
}

func gatewayRouteCandidateViews(routes []RouteSelection) []gatewayRouteCandidateView {
	views := make([]gatewayRouteCandidateView, 0, len(routes))
	for _, route := range routes {
		views = append(views, gatewayRouteCandidateView{
			RouteID:          route.Route.ID,
			ProviderID:       route.Provider.ID,
			ProviderType:     route.Provider.Type,
			ProviderModel:    route.ProviderModel,
			ResourceID:       routeResourceID(route),
			RoutePriority:    route.Route.Priority,
			ResourcePriority: routeResourcePriority(route),
			Weight:           routeEffectiveWeight(route),
			Strategy:         routeStrategy(route.Route),
		})
	}
	return views
}

func gatewayAuthContextView(call CallContext) map[string]any {
	view := map[string]any{
		"project_id": call.Project.ID,
		"api_key_id": call.Key.ID,
		"model":      call.Model.Name,
		"stream":     call.Stream,
	}
	if len(call.GatewayAuthMetadata) > 0 {
		view["metadata"] = call.GatewayAuthMetadata
	}
	return view
}

func gatewayAPIKeyMetadataView(key APIKey) map[string]any {
	return map[string]any{
		"id":                key.ID,
		"project_id":        key.ProjectID,
		"owner_user_id":     key.OwnerUserID,
		"group":             key.Group,
		"model_access_mode": key.ModelAccessMode,
		"allowed_models":    key.Allowed,
		"rate_limit_rpm":    key.RateLimitRPM,
		"token_limit_tpm":   key.TokenLimitTPM,
		"metadata":          key.Metadata,
	}
}

func applyGatewayAuthContextPatch(call *CallContext, data json.RawMessage) error {
	if call == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned an invalid auth context")
	}
	var patch map[string]json.RawMessage
	if err := decodeGatewayHookPayload(data, &patch, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned an invalid auth context"); err != nil {
		return err
	}
	if err := validateGatewayAuthStringInvariant(patch, "project_id", call.Project.ID); err != nil {
		return err
	}
	if err := validateGatewayAuthStringInvariant(patch, "api_key_id", call.Key.ID); err != nil {
		return err
	}
	if err := validateGatewayAuthStringInvariant(patch, "model", call.Model.Name); err != nil {
		return err
	}
	if err := validateGatewayAuthBoolInvariant(patch, "stream", call.Stream); err != nil {
		return err
	}
	metadataRaw, ok := patch["metadata"]
	if !ok {
		return nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned invalid metadata")
	}
	if call.GatewayAuthMetadata == nil {
		call.GatewayAuthMetadata = map[string]json.RawMessage{}
	}
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned an empty metadata key")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(call.GatewayAuthMetadata, key)
			continue
		}
		call.GatewayAuthMetadata[key] = append(json.RawMessage(nil), value...)
	}
	return nil
}

func validateGatewayAuthStringInvariant(patch map[string]json.RawMessage, key string, expected string) error {
	raw, ok := patch[key]
	if !ok {
		return nil
	}
	var actual string
	if err := json.Unmarshal(raw, &actual); err != nil || strings.TrimSpace(actual) != expected {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin cannot change core authentication context")
	}
	return nil
}

func validateGatewayAuthBoolInvariant(patch map[string]json.RawMessage, key string, expected bool) error {
	raw, ok := patch[key]
	if !ok {
		return nil
	}
	var actual bool
	if err := json.Unmarshal(raw, &actual); err != nil || actual != expected {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin cannot change core authentication context")
	}
	return nil
}

func applyGatewayRouteOrderPatch(routes []RouteSelection, data json.RawMessage) ([]RouteSelection, error) {
	var patch gatewayRouteOrderPatch
	if err := json.Unmarshal(data, &patch); err != nil {
		return nil, err
	}
	if len(patch.RouteIDs) != len(routes) {
		return nil, fmt.Errorf("route_ids has %d entries, want %d", len(patch.RouteIDs), len(routes))
	}
	routesByID := make(map[string]RouteSelection, len(routes))
	for _, route := range routes {
		if route.Route.ID == "" {
			return nil, fmt.Errorf("route candidate has no route id")
		}
		routesByID[route.Route.ID] = route
	}
	ordered := make([]RouteSelection, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, routeID := range patch.RouteIDs {
		if _, ok := seen[routeID]; ok {
			return nil, fmt.Errorf("route id %s is duplicated", routeID)
		}
		route, ok := routesByID[routeID]
		if !ok {
			return nil, fmt.Errorf("route id %s is not a core-approved candidate", routeID)
		}
		seen[routeID] = struct{}{}
		ordered = append(ordered, route)
	}
	return ordered, nil
}

func applyGatewayRouteCandidatesPatch(routes []RouteSelection, data json.RawMessage) ([]RouteSelection, error) {
	var patch gatewayRouteOrderPatch
	if err := json.Unmarshal(data, &patch); err != nil {
		return nil, err
	}
	routesByID := make(map[string]RouteSelection, len(routes))
	for _, route := range routes {
		if route.Route.ID == "" {
			return nil, fmt.Errorf("route candidate has no route id")
		}
		routesByID[route.Route.ID] = route
	}
	selected := make([]RouteSelection, 0, len(patch.RouteIDs))
	seen := make(map[string]struct{}, len(routes))
	for _, routeID := range patch.RouteIDs {
		if _, ok := seen[routeID]; ok {
			return nil, fmt.Errorf("route id %s is duplicated", routeID)
		}
		route, ok := routesByID[routeID]
		if !ok {
			return nil, fmt.Errorf("route id %s is not a core-approved candidate", routeID)
		}
		seen[routeID] = struct{}{}
		selected = append(selected, route)
	}
	return selected, nil
}

func attemptsWithAttributedUsage(call CallContext, attempts []RouteAttempt, route RouteSelection, usage Usage) []RouteAttempt {
	if len(attempts) == 0 {
		return attempts
	}
	patched := append([]RouteAttempt(nil), attempts...)
	for index := len(patched) - 1; index >= 0; index-- {
		attempt := patched[index]
		if !attempt.Invoked || attempt.Status < 200 || attempt.Status >= 300 {
			continue
		}
		if attempt.Selection.Route.ID != route.Route.ID || attempt.Selection.Provider.ID != route.Provider.ID {
			continue
		}
		attempt.Usage = priceUsageAt(call.Model, usage, call.StartedAt)
		patched[index] = attempt
		return patched
	}
	return patched
}
