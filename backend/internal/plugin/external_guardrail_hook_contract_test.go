package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExternalGuardrailHookFixtureDeclaresGuardrailPostContract(t *testing.T) {
	pkg := loadExternalGuardrailHookFixture(t)

	manifest := pkg.Manifest
	if manifest.ID != "tokenhub.extension.external-guardrail" {
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
		hook.HookID != "policy" ||
		hook.Stage != StageGuardrailPost ||
		hook.FailurePolicy != FailurePolicyFailClosed {
		t.Fatalf("hook descriptor = %+v, want guardrail_post fail_closed", hook)
	}
	for _, dataClass := range []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataProviderResponse, DataStreamEvents, DataUsage} {
		if !externalGuardrailHasDataClass(hook.Reads, dataClass) {
			t.Fatalf("hook reads = %+v, want %s", hook.Reads, dataClass)
		}
	}
	if !externalGuardrailHasDataClass(hook.Writes, DataProviderResponse) || !externalGuardrailHasDataClass(hook.Writes, DataAudit) {
		t.Fatalf("hook writes = %+v, want provider_response and audit", hook.Writes)
	}
	for _, dataClass := range []GatewayDataClass{DataAuthContext, DataProjectMetadata, DataAPIKeyMetadata, DataProviderResponse, DataStreamEvents, DataUsage} {
		if !externalGuardrailHasDataClass(manifest.Permissions.Data.Read, dataClass) {
			t.Fatalf("data read permissions = %+v, want %s", manifest.Permissions.Data.Read, dataClass)
		}
	}
	if !externalGuardrailHasDataClass(manifest.Permissions.Data.Write, DataProviderResponse) || !externalGuardrailHasDataClass(manifest.Permissions.Data.Write, DataAudit) {
		t.Fatalf("data write permissions = %+v, want provider_response and audit", manifest.Permissions.Data.Write)
	}
}

func TestExternalGuardrailHookFixtureRunsThroughGatewayHookRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external guardrail hook fixture uses POSIX sh")
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)
	if _, err := NewRuntime(externalGuardrailHookFixtureDir()).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load external guardrail hook fixture: %v", err)
	}

	report, err := runner.RunStage(t.Context(), StageGuardrailPost, GatewayHookInput{
		RequestID: "req_external_guardrail_contract",
		Stage:     StageGuardrailPost,
		Envelope: GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "guardrail_post",
			Model:       "gpt-guardrail",
			RequestBody: json.RawMessage(`{"id":"resp_external_guardrail","object":"response","status":"completed","output_text":"safe response"}`),
		},
		Data: GatewayHookData{
			DataAuthContext:      json.RawMessage(`{"request_id":"req_external_guardrail_contract"}`),
			DataProjectMetadata:  json.RawMessage(`{"id":"prj_external_guardrail_contract"}`),
			DataAPIKeyMetadata:   json.RawMessage(`{"id":"key_external_guardrail_contract"}`),
			DataProviderResponse: json.RawMessage(`{"id":"resp_external_guardrail","object":"response","status":"completed","output_text":"safe response"}`),
			DataStreamEvents:     json.RawMessage(`[]`),
			DataUsage:            json.RawMessage(`{"total_tokens":7}`),
		},
	})
	if err != nil {
		t.Fatalf("run external guardrail hook: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("run results = %d, want 1: %+v", len(report.Results), report)
	}
	result := report.Results[0]
	if result.PluginID != "tokenhub.extension.external-guardrail" ||
		result.HookID != "policy" ||
		result.Status != HookRunSucceeded ||
		result.Decision != HookDecisionContinue {
		t.Fatalf("run result = %+v, want successful guardrail hook", result)
	}
	patch, ok := result.Writes[DataProviderResponse]
	if !ok {
		t.Fatalf("guardrail hook writes = %+v, want provider response patch", result.Writes)
	}
	var patched map[string]any
	if err := json.Unmarshal(patch.Value, &patched); err != nil {
		t.Fatalf("decode guardrail patch: %v", err)
	}
	if patched["output_text"] != "guardrail approved" {
		t.Fatalf("patched provider response = %+v, want guardrail approved", patched)
	}
	rawAuditEvents := bytes.Join(externalGuardrailAuditEventBytes(result.AuditEvents), nil)
	for _, want := range [][]byte{
		[]byte(`"event":"external_guardrail_hook"`),
		[]byte(`"saw_auth_context":true`),
		[]byte(`"saw_project_metadata":true`),
		[]byte(`"saw_api_key_metadata":true`),
		[]byte(`"saw_provider_response":true`),
		[]byte(`"saw_stream_events":true`),
		[]byte(`"saw_usage":true`),
		[]byte(`"unsafe_output":false`),
	} {
		if !bytes.Contains(rawAuditEvents, want) {
			t.Fatalf("audit events missing %s: %s", want, rawAuditEvents)
		}
	}
}

func TestExternalGuardrailHookFixtureDenyIsFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external guardrail hook fixture uses POSIX sh")
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)
	if _, err := NewRuntime(externalGuardrailHookFixtureDir()).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load external guardrail hook fixture: %v", err)
	}

	_, err := runner.RunStage(t.Context(), StageGuardrailPost, GatewayHookInput{
		RequestID: "req_external_guardrail_fail_command",
		Stage:     StageGuardrailPost,
		Data: GatewayHookData{
			DataProviderResponse: json.RawMessage(`{"id":"resp_external_guardrail","object":"response","status":"completed","output_text":"unsafe prompt sentinel"}`),
			DataUsage:            json.RawMessage(`{"total_tokens":7}`),
		},
	})
	if !errors.Is(err, ErrGatewayHookDenied) {
		t.Fatalf("guardrail deny error = %v, want ErrGatewayHookDenied", err)
	}
}

func loadExternalGuardrailHookFixture(t *testing.T) Package {
	t.Helper()
	packages, err := NewRuntime(externalGuardrailHookFixtureDir()).Discover()
	if err != nil {
		t.Fatalf("discover external guardrail hook fixture: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %d, want external guardrail hook fixture", len(packages))
	}
	return packages[0]
}

func externalGuardrailHookFixtureDir() string {
	return filepath.Join("testdata", "external-guardrail-hook")
}

func externalGuardrailAuditEventBytes(values []json.RawMessage) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		out = append(out, []byte(value))
	}
	return out
}

func externalGuardrailHasDataClass(values []GatewayDataClass, target GatewayDataClass) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
