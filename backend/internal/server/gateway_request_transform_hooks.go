package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, payload any, protocol string, apply func(json.RawMessage) error) error {
	hooks := s.gatewayRequestTransformHooksForRoute(route, protocol)
	if len(hooks) == 0 {
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
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageRequestTransform, input, hooks)
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
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, providerRouteProtocolChatCompletions, func(data json.RawMessage) error {
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
	return s.runGatewayResponsesRequestTransformHooksForProtocol(ctx, call, route, req, providerRouteProtocolResponses)
}

func (s *Server) runGatewayResponsesRequestTransformHooksForProtocol(ctx context.Context, call CallContext, route RouteSelection, req *ResponsesRequest, protocol string) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, protocol, func(data json.RawMessage) error {
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
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, providerRouteProtocolEmbeddings, func(data json.RawMessage) error {
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
	err := s.runGatewayRequestTransformHooks(ctx, call, route, body, providerRouteProtocolResponses, func(data json.RawMessage) error {
		var patched map[string]json.RawMessage
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		body = cloneRawJSON(patched, 0)
		return nil
	})
	return body, err
}

func (s *Server) runGatewayAnthropicRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *anthropicMessagesRequest, protocol string) error {
	if req == nil {
		return nil
	}
	return s.runGatewayRequestTransformHooks(ctx, call, route, req.Raw, protocol, func(data json.RawMessage) error {
		return applyAnthropicGatewayRequestPatch(req, data)
	})
}
