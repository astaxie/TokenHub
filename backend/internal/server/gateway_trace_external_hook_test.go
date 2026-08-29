package server

import (
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExternalTraceHookFixtureRunsFromGatewayCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external trace hook fixture uses POSIX sh")
	}
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken: "external-trace-admin",
		PluginDir:  filepath.Join("..", "plugin", "testdata", "external-trace-hook"),
	})
	emitter := &recordingTraceEmitter{}
	app.traceEmitter = emitter

	app.finishCall(GatewayCallCompletion{
		Kind: CompletionKindRouted,
		Call: CallContext{
			RequestID: "req_external_trace_hook",
			Project:   Project{ID: "prj_external_trace"},
			Key:       APIKey{ID: "key_external_trace"},
			Model:     Model{Name: "gpt-trace"},
		},
		Route: RouteSelection{
			Provider: Provider{ID: "prv_external_trace", Type: ProviderMock, APIKey: "provider-secret"},
			Route:    ModelRoute{ID: "route_external_trace"},
		},
		Usage:           Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		StatusCode:      http.StatusOK,
		RequestPayload:  map[string]any{"input": "raw prompt sentinel"},
		ResponsePayload: map[string]any{"output": "ok"},
	})

	if completions := emitter.take(); len(completions) != 1 {
		t.Fatalf("trace emitter completions = %d, want 1", len(completions))
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].RequestID != "req_external_trace_hook" || logs[0].StatusCode != http.StatusOK {
		t.Fatalf("trace hook affected settlement logs: %+v", logs)
	}

	var traceAudit string
	for _, event := range store.ListAuditEvents() {
		if event.Action == "plugin.gateway.trace_export" && event.ResourceID == "req_external_trace_hook" {
			traceAudit = event.AfterSnapshot
			break
		}
	}
	if traceAudit == "" {
		t.Fatalf("external trace hook audit event was not recorded: %+v", store.ListAuditEvents())
	}
	for _, want := range []string{
		`"event":"external_trace_hook"`,
		`"saw_audit":true`,
		`"saw_usage":true`,
		`"leaked_request_body":false`,
		`"leaked_credentials":false`,
		`"leaked_prompt":false`,
		`"decision":"continue"`,
		`"status":"succeeded"`,
	} {
		if !strings.Contains(traceAudit, want) {
			t.Fatalf("trace audit missing %q: %s", want, traceAudit)
		}
	}
	for _, forbidden := range []string{"raw prompt sentinel", "provider-secret"} {
		if strings.Contains(traceAudit, forbidden) {
			t.Fatalf("trace audit leaked %q: %s", forbidden, traceAudit)
		}
	}
}

func TestExternalTraceHookFixtureFailureDoesNotAffectGatewayCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external trace hook fixture uses POSIX sh")
	}
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken: "external-trace-admin",
		PluginDir:  filepath.Join("..", "plugin", "testdata", "external-trace-hook"),
	})
	emitter := &recordingTraceEmitter{}
	app.traceEmitter = emitter

	app.finishCall(GatewayCallCompletion{
		Kind: CompletionKindRouted,
		Call: CallContext{
			RequestID: "req_external_trace_fail_command",
			Project:   Project{ID: "prj_external_trace"},
			Key:       APIKey{ID: "key_external_trace"},
			Model:     Model{Name: "gpt-trace"},
		},
		Route:      RouteSelection{Provider: Provider{ID: "prv_external_trace", Type: ProviderMock}, Route: ModelRoute{ID: "route_external_trace"}},
		Usage:      Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		StatusCode: http.StatusOK,
	})

	if completions := emitter.take(); len(completions) != 1 {
		t.Fatalf("trace emitter completions = %d, want 1", len(completions))
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].RequestID != "req_external_trace_fail_command" || logs[0].StatusCode != http.StatusOK {
		t.Fatalf("external trace hook failure affected settlement logs: %+v", logs)
	}
	for _, event := range store.ListAuditEvents() {
		if event.Action == "plugin.gateway.trace_export" && strings.Contains(event.AfterSnapshot, "provider-secret") {
			t.Fatalf("failed trace hook leaked credentials: %s", event.AfterSnapshot)
		}
	}
}
