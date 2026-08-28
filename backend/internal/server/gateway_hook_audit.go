package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const (
	gatewayHookAuditMaxEventsPerReport = 64
)

func (s *Server) runAuditedGatewayHookStage(ctx context.Context, call CallContext, stage pluginmeta.GatewayHookStage, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookRunReport, error) {
	report, err := s.gatewayHooks.RunStage(ctx, stage, input)
	s.recordGatewayHookAuditEvents(call, report)
	return report, err
}

func (s *Server) runAuditedGatewayHookStageHooks(ctx context.Context, call CallContext, stage pluginmeta.GatewayHookStage, input pluginmeta.GatewayHookInput, hooks []pluginmeta.GatewayHookDescriptor) (pluginmeta.GatewayHookRunReport, error) {
	report, err := s.gatewayHooks.RunStageHooks(ctx, stage, input, hooks)
	s.recordGatewayHookAuditEvents(call, report)
	return report, err
}

func (s *Server) recordGatewayHookAuditEvents(call CallContext, report pluginmeta.GatewayHookRunReport) {
	if s == nil || s.store == nil {
		return
	}
	recorded := 0
	for _, result := range report.Results {
		for index, raw := range result.AuditEvents {
			if recorded >= gatewayHookAuditMaxEventsPerReport-1 {
				s.recordGatewayHookAuditLimitEvent(call, report.Stage, result, recorded)
				return
			}
			s.store.RecordAuditEvent(AuditEvent{
				Action:        "plugin.gateway." + string(report.Stage),
				ResourceType:  "gateway_request",
				ResourceID:    call.RequestID,
				Status:        gatewayHookAuditStatus(result),
				Message:       fmt.Sprintf("Gateway hook %s/%s emitted audit event", result.PluginID, result.HookID),
				AfterSnapshot: auditSnapshotJSON(gatewayHookAuditSnapshot(call, report.Stage, result, index, raw)),
			})
			recorded++
		}
	}
}

func (s *Server) recordGatewayHookAuditLimitEvent(call CallContext, stage pluginmeta.GatewayHookStage, result pluginmeta.GatewayHookRunResult, recorded int) {
	s.store.RecordAuditEvent(AuditEvent{
		Action:       "plugin.gateway." + string(stage),
		ResourceType: "gateway_request",
		ResourceID:   call.RequestID,
		Status:       "truncated",
		Message:      fmt.Sprintf("Gateway hook audit events exceeded the per-stage limit after %d records", recorded),
		AfterSnapshot: auditSnapshotJSON(map[string]any{
			"request_id":        call.RequestID,
			"project_id":        call.Project.ID,
			"api_key_id":        call.Key.ID,
			"model":             call.Model.Name,
			"stage":             stage,
			"plugin_id":         result.PluginID,
			"hook_id":           result.HookID,
			"recorded_events":   recorded,
			"max_event_records": gatewayHookAuditMaxEventsPerReport,
			"truncated":         true,
		}),
	})
}

func gatewayHookAuditStatus(result pluginmeta.GatewayHookRunResult) string {
	if result.Status == pluginmeta.HookRunFailed {
		return "failed"
	}
	if result.Status == pluginmeta.HookRunSkipped {
		return "skipped"
	}
	if result.Decision == pluginmeta.HookDecisionDeny {
		return "denied"
	}
	if result.Decision == pluginmeta.HookDecisionShortCircuit {
		return "short_circuited"
	}
	return "success"
}

func gatewayHookAuditSnapshot(call CallContext, stage pluginmeta.GatewayHookStage, result pluginmeta.GatewayHookRunResult, eventIndex int, raw json.RawMessage) map[string]any {
	return map[string]any{
		"request_id":     call.RequestID,
		"project_id":     call.Project.ID,
		"api_key_id":     call.Key.ID,
		"model":          call.Model.Name,
		"stream":         call.Stream,
		"stage":          stage,
		"plugin_id":      result.PluginID,
		"hook_id":        result.HookID,
		"decision":       result.Decision,
		"status":         result.Status,
		"event_index":    eventIndex,
		"plugin_event":   decodeGatewayPluginAuditEvent(raw),
		"duration_ms":    result.DurationMS,
		"failure_policy": result.FailurePolicy,
	}
}

func decodeGatewayPluginAuditEvent(raw json.RawMessage) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]any{"error": "empty_audit_event"}
	}
	if len(trimmed) > pluginmeta.MaxGatewayHookAuditEventBytes {
		return map[string]any{
			"truncated":   true,
			"bytes":       len(trimmed),
			"limit_bytes": pluginmeta.MaxGatewayHookAuditEventBytes,
		}
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return map[string]any{
			"error": "invalid_json",
			"bytes": len(trimmed),
		}
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return map[string]any{
			"error": "audit_event_must_be_object",
			"bytes": len(trimmed),
		}
	}
	return object
}
