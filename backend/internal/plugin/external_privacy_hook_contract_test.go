package plugin

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExternalPrivacyHookFixtureDeclaresPrivacyPreContract(t *testing.T) {
	pkg := loadExternalPrivacyHookFixture(t)

	manifest := pkg.Manifest
	if manifest.ID != "tokenhub.extension.external-privacy" {
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
		hook.HookID != "mask" ||
		hook.Stage != StagePrivacyPre ||
		hook.FailurePolicy != FailurePolicyFailClosed {
		t.Fatalf("hook descriptor = %+v, want privacy_pre fail_closed", hook)
	}
	if !externalPrivacyHasDataClass(hook.Reads, DataRequestHeaders) || !externalPrivacyHasDataClass(hook.Reads, DataRequestBody) {
		t.Fatalf("hook reads = %+v, want request_headers and request_body", hook.Reads)
	}
	if !externalPrivacyHasDataClass(hook.Writes, DataRequestBody) || !externalPrivacyHasDataClass(hook.Writes, DataAudit) {
		t.Fatalf("hook writes = %+v, want request_body and audit", hook.Writes)
	}
	if !externalPrivacyHasDataClass(manifest.Permissions.Data.Read, DataRequestHeaders) || !externalPrivacyHasDataClass(manifest.Permissions.Data.Read, DataRequestBody) {
		t.Fatalf("data read permissions = %+v, want request_headers and request_body", manifest.Permissions.Data.Read)
	}
	if !externalPrivacyHasDataClass(manifest.Permissions.Data.Write, DataRequestBody) || !externalPrivacyHasDataClass(manifest.Permissions.Data.Write, DataAudit) {
		t.Fatalf("data write permissions = %+v, want request_body and audit", manifest.Permissions.Data.Write)
	}
}

func TestExternalPrivacyHookFixtureRunsThroughGatewayHookRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external privacy hook fixture uses POSIX sh")
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)
	if _, err := NewRuntime(externalPrivacyHookFixtureDir()).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load external privacy hook fixture: %v", err)
	}

	report, err := runner.RunStage(t.Context(), StagePrivacyPre, GatewayHookInput{
		RequestID: "req_external_privacy_contract",
		Stage:     StagePrivacyPre,
		Envelope: GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "privacy_pre",
			Model:       "gpt-privacy",
			RequestBody: json.RawMessage(`{"model":"gpt-privacy","messages":[{"role":"user","content":"raw prompt sentinel"}]}`),
		},
		Data: GatewayHookData{
			DataRequestHeaders:  json.RawMessage(`{"x-request-id":"req_external_privacy_contract"}`),
			DataRequestBody:     json.RawMessage(`{"model":"gpt-privacy","messages":[{"role":"user","content":"raw prompt sentinel"}]}`),
			DataProjectMetadata: json.RawMessage(`{"id":"prj_external_privacy_contract"}`),
			DataAPIKeyMetadata:  json.RawMessage(`{"id":"key_external_privacy_contract"}`),
		},
	})
	if err != nil {
		t.Fatalf("run external privacy hook: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("run results = %d, want 1: %+v", len(report.Results), report)
	}
	result := report.Results[0]
	if result.PluginID != "tokenhub.extension.external-privacy" ||
		result.HookID != "mask" ||
		result.Status != HookRunSucceeded ||
		result.Decision != HookDecisionContinue {
		t.Fatalf("run result = %+v, want successful privacy hook", result)
	}
	patch, ok := result.Writes[DataRequestBody]
	if !ok {
		t.Fatalf("privacy hook writes = %+v, want request body patch", result.Writes)
	}
	var patched map[string]any
	if err := json.Unmarshal(patch.Value, &patched); err != nil {
		t.Fatalf("decode privacy patch: %v", err)
	}
	if patched["model"] != "gpt-privacy" {
		t.Fatalf("patched model = %v, want gpt-privacy", patched["model"])
	}
	messages, ok := patched["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("patched messages = %#v, want one message", patched["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok || message["content"] != "[masked-by-privacy]" {
		t.Fatalf("patched message = %#v, want masked content", messages[0])
	}
	rawAuditEvents := bytes.Join(externalPrivacyAuditEventBytes(result.AuditEvents), nil)
	for _, want := range [][]byte{
		[]byte(`"event":"external_privacy_hook"`),
		[]byte(`"saw_request_headers":true`),
		[]byte(`"saw_request_body":true`),
		[]byte(`"saw_project_metadata":false`),
		[]byte(`"saw_api_key_metadata":false`),
		[]byte(`"leaked_prompt":false`),
	} {
		if !bytes.Contains(rawAuditEvents, want) {
			t.Fatalf("audit events missing %s: %s", want, rawAuditEvents)
		}
	}
}

func TestExternalPrivacyHookFixtureFailureIsFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external privacy hook fixture uses POSIX sh")
	}
	chain := NewGatewayChainRegistry()
	runner := NewGatewayHookRunner(chain)
	if _, err := NewRuntime(externalPrivacyHookFixtureDir()).LoadIntoWithActions(NewRegistry(), chain, nil, nil, runner); err != nil {
		t.Fatalf("load external privacy hook fixture: %v", err)
	}

	_, err := runner.RunStage(t.Context(), StagePrivacyPre, GatewayHookInput{
		RequestID: "req_external_privacy_fail_command",
		Stage:     StagePrivacyPre,
		Data: GatewayHookData{
			DataRequestHeaders: json.RawMessage(`{"x-request-id":"req_external_privacy_fail_command"}`),
			DataRequestBody:    json.RawMessage(`{"model":"gpt-privacy","messages":[{"role":"user","content":"secret"}]}`),
		},
	})
	if err == nil {
		t.Fatal("privacy hook failure was accepted")
	}
}

func loadExternalPrivacyHookFixture(t *testing.T) Package {
	t.Helper()
	packages, err := NewRuntime(externalPrivacyHookFixtureDir()).Discover()
	if err != nil {
		t.Fatalf("discover external privacy hook fixture: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %d, want external privacy hook fixture", len(packages))
	}
	return packages[0]
}

func externalPrivacyHookFixtureDir() string {
	return filepath.Join("testdata", "external-privacy-hook")
}

func externalPrivacyAuditEventBytes(values []json.RawMessage) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, value := range values {
		out = append(out, []byte(value))
	}
	return out
}

func externalPrivacyHasDataClass(values []GatewayDataClass, target GatewayDataClass) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
