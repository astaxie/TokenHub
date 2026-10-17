package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestSensitiveGatewayHookAuditEventsRetainOnlyMetadata(t *testing.T) {
	const private = "private-hook-audit-sentinel"
	store := NewMemoryStore()
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	for _, call := range []CallContext{
		{RequestID: "rerank-private", Model: Model{Name: "rank", Modality: "rerank"}},
		{RequestID: "transcription-private", RouteProtocol: "audio/transcriptions"},
		{RequestID: "translation-private", RouteProtocol: "audio/translations"},
	} {
		app.recordGatewayHookAuditEvents(call, pluginmeta.GatewayHookRunReport{
			Stage: pluginmeta.StageGuardrailPre,
			Results: []pluginmeta.GatewayHookRunResult{{
				PluginID: "test.private-audit", HookID: "record", Status: pluginmeta.HookRunSucceeded,
				AuditEvents: []json.RawMessage{json.RawMessage(`{"query":"` + private + `","document":"` + private + `","file_body":"` + private + `"}`)},
			}},
		})
	}
	events := store.ListAuditEvents()
	if len(events) != 3 {
		t.Fatalf("audit events=%d", len(events))
	}
	for _, event := range events {
		if strings.Contains(event.AfterSnapshot, private) || !strings.Contains(event.AfterSnapshot, "test.private-audit") || !strings.Contains(event.AfterSnapshot, "sensitive_endpoint_payload") {
			t.Fatalf("audit retained content or dropped hook evidence: %s", event.AfterSnapshot)
		}
	}
}

func TestSensitiveGatewayHookErrorsPreserveControlFlow(t *testing.T) {
	for _, call := range []CallContext{{Model: Model{Modality: "rerank"}}, {RouteProtocol: "audio/transcriptions"}} {
		for _, sentinel := range []error{pluginmeta.ErrGatewayHookDenied, pluginmeta.ErrGatewayHookRouteSkipped, pluginmeta.ErrGatewayHookShortCircuited} {
			err := sanitizeSensitiveGatewayHookError(call, fmt.Errorf("private-input: %w", sentinel))
			if err != sentinel {
				t.Fatalf("hook control flow changed: %v", err)
			}
		}
	}
}

func TestSensitiveGatewayHookErrorsRejectInvalidStatusMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "rerank zero", err: &HTTPError{Status: 0, Code: "provider_error", Message: "private"}, status: 502},
		{name: "rerank too high", err: &HTTPError{Status: 700, Code: "provider_error", Message: "private"}, status: 502},
		{name: "transcription zero", err: &HTTPError{Status: 0, Code: "provider_error", Message: "private"}, status: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := CallContext{Model: Model{Modality: "rerank"}}
			if strings.HasPrefix(tc.name, "transcription") {
				call = CallContext{RouteProtocol: "audio/transcriptions"}
			}
			err := sanitizeSensitiveGatewayHookError(call, tc.err)
			if got := AsHTTPError(err).Status; got != tc.status {
				t.Fatalf("sanitized status = %d, want %d", got, tc.status)
			}
		})
	}
}
