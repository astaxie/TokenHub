package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

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

func (s *Server) runGatewayResponsesPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *ResponsesRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
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

func (s *Server) runGatewayEmbeddingsPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *EmbeddingsRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applyEmbeddingsGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayImagePrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *imageGenerationRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applyImageGatewayRequestPatch(req, data)
	})
}

func (s *Server) runGatewayCompactPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, request map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	body := cloneRawJSON(request, 0)
	err := s.runGatewayPrivacyPreHooks(ctx, call, headers, body, func(data json.RawMessage) error {
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

func (s *Server) runGatewayPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, payload any, apply func(json.RawMessage) error) error {
	if !s.hasGatewayHookStage(pluginmeta.StagePrivacyPre) {
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
		Data: gatewayHookCallData(call, body),
	}
	if requestHeaders, ok := marshalGatewayHookData(sanitizedGatewayHookHeaders(headers)); ok {
		input.Data[pluginmeta.DataRequestHeaders] = requestHeaders
	}
	input.Data[pluginmeta.DataRequestBody] = body
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StagePrivacyPre, input)
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
