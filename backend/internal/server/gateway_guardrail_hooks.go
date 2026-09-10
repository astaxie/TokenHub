package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayGuardrailPreHooks(ctx context.Context, call CallContext, payload any, targets []guardrailTextTarget, apply func(json.RawMessage) error) error {
	if !s.hasGatewayHookStage(pluginmeta.StageGuardrailPre) {
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
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageGuardrailPre, input)
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

func (s *Server) runGatewayImageGuardrailPreHooks(ctx context.Context, call CallContext, req *imageGenerationRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *req, imageGuardrailTargets(req), func(data json.RawMessage) error {
		return applyImageGatewayRequestPatch(req, data)
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
