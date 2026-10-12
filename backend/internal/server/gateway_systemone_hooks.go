package server

import (
	"context"
	"encoding/json"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewaySystemOneDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *SystemOneRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func (s *Server) runGatewaySystemOnePrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *SystemOneRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func (s *Server) runGatewaySystemOneGuardrailPreHooks(ctx context.Context, call CallContext, req *SystemOneRequest) error {
	if !s.hasGatewayHookStage(pluginmeta.StageGuardrailPre) {
		return nil
	}
	batch, err := systemOneGuardrailTargets(req)
	if err != nil {
		return err
	}
	return s.runGatewayGuardrailPreHooks(ctx, call, *req, batch.targets, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func (s *Server) runGatewaySystemOneContextOptimizeHooks(ctx context.Context, call CallContext, req *SystemOneRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}
