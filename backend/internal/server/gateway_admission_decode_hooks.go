package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayAdmissionHooks(ctx context.Context, call CallContext, headers http.Header, payload any, tokenReservation int64) error {
	if !s.hasGatewayHookStage(pluginmeta.StageAdmission) {
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
	if _, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageAdmission, input); err != nil {
		return gatewayHookHTTPError(pluginmeta.StageAdmission, err)
	}
	return nil
}

func (s *Server) runGatewayDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, payload any, apply func(json.RawMessage) error) error {
	if !s.hasGatewayHookStage(pluginmeta.StageDecodeNormalize) {
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
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageDecodeNormalize, input)
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

func (s *Server) runGatewayImageDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *imageGenerationRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applyImageGatewayRequestPatch(req, data)
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
