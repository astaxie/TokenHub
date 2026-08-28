package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

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
	if !s.hasGatewayHookStage(pluginmeta.StageContextOptimize) {
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
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageContextOptimize, input)
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

func (s *Server) runGatewayImageContextOptimizeHooks(ctx context.Context, call CallContext, req *imageGenerationRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		return applyImageGatewayRequestPatch(req, data)
	})
}
