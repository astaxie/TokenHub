package server

import (
	"context"
	"encoding/json"
	"log"

	pluginmeta "tokenhub/backend/internal/plugin"
)

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

func marshalGatewayHookData(value any) (json.RawMessage, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return data, true
}
