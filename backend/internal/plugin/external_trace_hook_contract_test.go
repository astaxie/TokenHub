package plugin

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExternalTraceHookFixtureDeclaresObserveOnlyContract(t *testing.T) {
	pkg := loadExternalTraceHookFixture(t)

	manifest := pkg.Manifest
	if manifest.ID != "tokenhub.extension.external-trace" {
		t.Fatalf("manifest id = %q", manifest.ID)
	}
	if len(manifest.Kinds) != 1 || manifest.Kinds[0] != KindExtension {
		t.Fatalf("manifest kinds = %+v, want extension only", manifest.Kinds)
	}
	if len(manifest.Placement) != 1 || manifest.Placement[0] != PlacementGatewayChain {
		t.Fatalf("manifest placement = %+v, want gateway_chain only", manifest.Placement)
	}
	if manifest.Entry.Backend == nil ||
		manifest.Entry.Backend.Protocol != BackendProtocolStdioJSONV1 ||
		manifest.Entry.Backend.Command != "hook.sh" {
		t.Fatalf("backend entry = %+v, want stdio-json-v1 hook.sh", manifest.Entry.Backend)
	}

	hooks := manifest.GatewayHooks()
	if len(hooks) != 1 {
		t.Fatalf("gateway hooks = %d, want 1", len(hooks))
	}
	hook := hooks[0]
	if hook.PluginID != manifest.ID ||
		hook.HookID != "export" ||
		hook.Stage != StageTraceExport ||
		hook.FailurePolicy != FailurePolicyObserveOnly {
		t.Fatalf("hook descriptor = %+v, want observe-only trace_export", hook)
	}
	if !externalTraceHasDataClass(hook.Reads, DataAudit) || !externalTraceHasDataClass(hook.Reads, DataUsage) {
		t.Fatalf("hook reads = %+v, want audit and usage", hook.Reads)
	}
	if len(hook.Writes) != 0 {
		t.Fatalf("hook writes = %+v, want no declared writes", hook.Writes)
	}
	if !externalTraceHasDataClass(manifest.Permissions.Data.Read, DataAudit) ||
		!externalTraceHasDataClass(manifest.Permissions.Data.Read, DataUsage) {
		t.Fatalf("data read permissions = %+v, want audit and usage", manifest.Permissions.Data.Read)
	}
	if len(manifest.Permissions.Data.Write) != 0 {
		t.Fatalf("data write permissions = %+v, want none", manifest.Permissions.Data.Write)
	}
}

func TestExternalTraceHookFixtureRunsThroughGatewayHookRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external trace hook fixture uses POSIX sh")
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)
	if _, err := NewRuntime(externalTraceHookFixtureDir()).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load external trace hook fixture: %v", err)
	}

	report, err := runner.RunStage(t.Context(), StageTraceExport, GatewayHookInput{
		RequestID: "req_external_trace_contract",
		Stage:     StageTraceExport,
		Envelope: GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "routed",
			Model:       "gpt-trace",
			RequestBody: json.RawMessage(`{"input":"raw prompt sentinel"}`),
		},
		Data: GatewayHookData{
			DataAudit:               json.RawMessage(`{"request_id":"req_external_trace_contract"}`),
			DataUsage:               json.RawMessage(`{"total_tokens":7}`),
			DataRequestBody:         json.RawMessage(`{"input":"raw prompt sentinel"}`),
			DataProviderCredentials: json.RawMessage(`{"api_key":"provider-secret"}`),
		},
	})
	if err != nil {
		t.Fatalf("run external trace hook: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("run results = %d, want 1: %+v", len(report.Results), report)
	}
	result := report.Results[0]
	if result.PluginID != "tokenhub.extension.external-trace" ||
		result.HookID != "export" ||
		result.Status != HookRunSucceeded ||
		result.Decision != HookDecisionContinue ||
		len(result.Writes) != 0 {
		t.Fatalf("run result = %+v, want observe-only continue with no writes", result)
	}
	if report.TerminalDecision != "" {
		t.Fatalf("terminal decision = %q, want none", report.TerminalDecision)
	}
	rawAuditEvents := bytes.Join(externalTraceAuditEventBytes(result.AuditEvents), nil)
	for _, want := range [][]byte{
		[]byte(`"event":"external_trace_hook"`),
		[]byte(`"saw_audit":true`),
		[]byte(`"saw_usage":true`),
		[]byte(`"leaked_request_body":false`),
		[]byte(`"leaked_credentials":false`),
		[]byte(`"leaked_prompt":false`),
	} {
		if !bytes.Contains(rawAuditEvents, want) {
			t.Fatalf("audit events missing %s: %s", want, rawAuditEvents)
		}
	}
}

func TestExternalTraceHookFixtureFailureIsObserveOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external trace hook fixture uses POSIX sh")
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)
	if _, err := NewRuntime(externalTraceHookFixtureDir()).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load external trace hook fixture: %v", err)
	}

	report, err := runner.RunStage(t.Context(), StageTraceExport, GatewayHookInput{
		RequestID: "req_external_trace_fail_command",
		Stage:     StageTraceExport,
		Data: GatewayHookData{
			DataAudit: json.RawMessage(`{"request_id":"req_external_trace_fail_command"}`),
			DataUsage: json.RawMessage(`{"total_tokens":7}`),
		},
	})
	if err != nil {
		t.Fatalf("run external trace hook failure: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("run results = %d, want 1: %+v", len(report.Results), report)
	}
	result := report.Results[0]
	if result.Status != HookRunSkipped ||
		result.Decision != HookDecisionContinue ||
		result.Error == "" ||
		len(result.Writes) != 0 ||
		len(result.AuditEvents) != 0 {
		t.Fatalf("failure result = %+v, want skipped observe-only failure without writes or audit events", result)
	}
	if report.TerminalDecision != "" {
		t.Fatalf("terminal decision = %q, want none", report.TerminalDecision)
	}
}

func loadExternalTraceHookFixture(t *testing.T) Package {
	t.Helper()
	packages, err := NewRuntime(externalTraceHookFixtureDir()).Discover()
	if err != nil {
		t.Fatalf("discover external trace hook fixture: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %d, want external trace hook fixture", len(packages))
	}
	return packages[0]
}

func externalTraceHookFixtureDir() string {
	return filepath.Join("testdata", "external-trace-hook")
}

func externalTraceAuditEventBytes(values []json.RawMessage) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		out = append(out, []byte(value))
	}
	return out
}

func externalTraceHasDataClass(values []GatewayDataClass, target GatewayDataClass) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
