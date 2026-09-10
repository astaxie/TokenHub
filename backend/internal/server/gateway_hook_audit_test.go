package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestGatewayHookAuditEventsAreWrappedAndRedacted(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	call := CallContext{
		RequestID: "req_hook_audit",
		Project:   Project{ID: "prj_hook_audit"},
		Key:       APIKey{ID: "key_hook_audit"},
		Model:     Model{Name: "gpt-audit"},
		Stream:    true,
	}
	report := pluginmeta.GatewayHookRunReport{
		Stage: pluginmeta.StageAdmission,
		Results: []pluginmeta.GatewayHookRunResult{
			{
				PluginID:      "tokenhub.policy",
				HookID:        "admit",
				Decision:      pluginmeta.HookDecisionDeny,
				FailurePolicy: pluginmeta.FailurePolicyFailClosed,
				Status:        pluginmeta.HookRunSucceeded,
				DurationMS:    7,
				AuditEvents: []json.RawMessage{
					json.RawMessage(`{"reason":"pii_match","authorization":"Bearer secret","nested":{"refresh_token":"refresh-secret"}}`),
				},
			},
		},
	}

	server.recordGatewayHookAuditEvents(call, report)

	events := store.ListAuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	event := events[0]
	if event.Action != "plugin.gateway.admission" {
		t.Fatalf("action = %q, want plugin.gateway.admission", event.Action)
	}
	if event.ResourceType != "gateway_request" || event.ResourceID != "req_hook_audit" {
		t.Fatalf("resource = %s/%s, want gateway_request/req_hook_audit", event.ResourceType, event.ResourceID)
	}
	if event.Status != "denied" {
		t.Fatalf("status = %q, want denied", event.Status)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(event.AfterSnapshot), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v; snapshot=%s", err, event.AfterSnapshot)
	}
	if snapshot["request_id"] != "req_hook_audit" || snapshot["project_id"] != "prj_hook_audit" || snapshot["api_key_id"] != "key_hook_audit" {
		t.Fatalf("snapshot context = %+v", snapshot)
	}
	pluginEvent, ok := snapshot["plugin_event"].(map[string]any)
	if !ok {
		t.Fatalf("plugin event = %#v, want object", snapshot["plugin_event"])
	}
	if pluginEvent["authorization"] != "[redacted]" {
		t.Fatalf("authorization = %v, want redacted", pluginEvent["authorization"])
	}
	nested, ok := pluginEvent["nested"].(map[string]any)
	if !ok || nested["refresh_token"] != "[redacted]" {
		t.Fatalf("nested event = %#v, want redacted refresh token", pluginEvent["nested"])
	}
}

func TestGatewayHookAuditEventsSkipEmptyResults(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)

	server.recordGatewayHookAuditEvents(CallContext{RequestID: "req_empty"}, pluginmeta.GatewayHookRunReport{
		Stage: pluginmeta.StagePrivacyPre,
		Results: []pluginmeta.GatewayHookRunResult{
			{
				PluginID: "tokenhub.privacy",
				HookID:   "mask",
				Status:   pluginmeta.HookRunSucceeded,
			},
		},
	})

	if events := store.ListAuditEvents(); len(events) != 0 {
		t.Fatalf("audit events = %+v, want none", events)
	}
}

func TestGatewayHookAuditEventsAreRecordedByTraceExportStage(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.trace",
		HookID:        "export",
		Stage:         pluginmeta.StageTraceExport,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataAudit},
		FailurePolicy: pluginmeta.FailurePolicyObserveOnly,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register trace hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{
			AuditEvents: []json.RawMessage{json.RawMessage(`{"exported":true}`)},
		}, nil
	})); err != nil {
		t.Fatalf("register trace handler: %v", err)
	}

	server.runGatewayTraceExportHooks(context.Background(), GatewayCallCompletion{
		Kind: CompletionKindRouted,
		Call: CallContext{
			RequestID: "req_trace_audit",
			Project:   Project{ID: "prj_trace_audit"},
			Key:       APIKey{ID: "key_trace_audit"},
			Model:     Model{Name: "gpt-trace"},
		},
		StatusCode: 200,
	})

	events := store.ListAuditEvents()
	if len(events) != 1 {
		t.Fatalf("audit events = %+v, want one trace-export audit event", events)
	}
	if events[0].Action != "plugin.gateway.trace_export" || events[0].ResourceID != "req_trace_audit" {
		t.Fatalf("trace audit event = %+v", events[0])
	}
}

func TestGatewayHookAuditEventsAreBoundedPerReport(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	auditEvents := make([]json.RawMessage, 0, gatewayHookAuditMaxEventsPerReport+1)
	for index := 0; index < gatewayHookAuditMaxEventsPerReport+1; index++ {
		auditEvents = append(auditEvents, json.RawMessage(`{"event":"kept"}`))
	}

	server.recordGatewayHookAuditEvents(CallContext{RequestID: "req_many"}, pluginmeta.GatewayHookRunReport{
		Stage: pluginmeta.StagePrivacyPre,
		Results: []pluginmeta.GatewayHookRunResult{
			{
				PluginID:    "tokenhub.privacy",
				HookID:      "many",
				Status:      pluginmeta.HookRunSucceeded,
				AuditEvents: auditEvents,
			},
		},
	})

	events := store.ListAuditEvents()
	if len(events) != gatewayHookAuditMaxEventsPerReport {
		t.Fatalf("audit events = %d, want capped at %d", len(events), gatewayHookAuditMaxEventsPerReport)
	}
	if events[0].Status != "truncated" {
		t.Fatalf("latest audit event status = %q, want truncated", events[0].Status)
	}
}

func TestDecodeGatewayPluginAuditEventRejectsUnsafeShapes(t *testing.T) {
	empty := decodeGatewayPluginAuditEvent(json.RawMessage("   "))
	emptyMap, ok := empty.(map[string]any)
	if !ok || emptyMap["error"] != "empty_audit_event" {
		t.Fatalf("empty event = %#v, want empty error", empty)
	}

	invalid := decodeGatewayPluginAuditEvent(json.RawMessage(`{"unterminated"`))
	invalidMap, ok := invalid.(map[string]any)
	if !ok || invalidMap["error"] != "invalid_json" {
		t.Fatalf("invalid event = %#v, want invalid JSON error", invalid)
	}

	oversized := decodeGatewayPluginAuditEvent(json.RawMessage(`{"note":"` + strings.Repeat("x", pluginmeta.MaxGatewayHookAuditEventBytes) + `"}`))
	oversizedMap, ok := oversized.(map[string]any)
	if !ok || oversizedMap["truncated"] != true || oversizedMap["limit_bytes"] != pluginmeta.MaxGatewayHookAuditEventBytes {
		t.Fatalf("oversized event = %#v, want truncated marker", oversized)
	}

	scalar := decodeGatewayPluginAuditEvent(json.RawMessage(`"Bearer secret"`))
	scalarMap, ok := scalar.(map[string]any)
	if !ok || scalarMap["error"] != "audit_event_must_be_object" {
		t.Fatalf("scalar event = %#v, want object-shape error", scalar)
	}

	array := decodeGatewayPluginAuditEvent(json.RawMessage(`["refresh-secret"]`))
	arrayMap, ok := array.(map[string]any)
	if !ok || arrayMap["error"] != "audit_event_must_be_object" {
		t.Fatalf("array event = %#v, want object-shape error", array)
	}
}
