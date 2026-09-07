package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGatewayCommandRunnerExecutesStdioJSONHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat >/dev/null
printf '{"decision":"continue","writes":{"audit":{"value":{"hook":"seen"}}}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewGatewayCommandRunner(dir, "hook.sh")
	result, err := runner.ExecuteGatewayHook(t.Context(), GatewayHookInput{
		RequestID: "req_1",
		Stage:     StagePrivacyPre,
		Envelope:  GatewayEnvelope{Operation: "chat"},
	})
	if err != nil {
		t.Fatalf("execute gateway command: %v", err)
	}
	if result.Decision != HookDecisionContinue {
		t.Fatalf("decision = %q, want continue", result.Decision)
	}
	var audit map[string]string
	if err := json.Unmarshal(result.Writes[DataAudit].Value, &audit); err != nil {
		t.Fatalf("decode audit write: %v", err)
	}
	if audit["hook"] != "seen" {
		t.Fatalf("audit write = %+v, want seen", audit)
	}
}

func TestGatewayCommandRunnerRejectsEscapingCommandPath(t *testing.T) {
	runner := NewGatewayCommandRunner(t.TempDir(), "../hook.sh")
	_, err := runner.ExecuteGatewayHook(t.Context(), GatewayHookInput{
		RequestID: "req_1",
		Stage:     StagePrivacyPre,
	})
	if err == nil {
		t.Fatal("escaping command path was accepted")
	}
}

func TestGatewayCommandRunnerRejectsExternalHookWithoutEnforcedIsolation(t *testing.T) {
	runner := NewGatewayCommandRunner(t.TempDir(), "missing.sh", PermissionGrant{Enforced: true})
	_, err := runner.ExecuteGatewayHook(t.Context(), GatewayHookInput{
		RequestID: "req_1",
		Stage:     StagePrivacyPre,
	})
	if err == nil {
		t.Fatal("external gateway command ran without enforced isolation")
	}
	if code, ok := PluginErrorCodeOf(err); !ok || code != PluginErrorPermissionUnsupported {
		t.Fatalf("error code = %q, %t; want %q for error %v", code, ok, PluginErrorPermissionUnsupported, err)
	}
}

func trustedGatewayHookFixtureRunner(t *testing.T, pkg Package) *GatewayHookRunner {
	t.Helper()
	hooks := pkg.Manifest.GatewayHooks()
	if len(hooks) != 1 {
		t.Fatalf("gateway hooks = %d, want 1", len(hooks))
	}
	chain := NewGatewayChainRegistry()
	if err := chain.RegisterHook(hooks[0]); err != nil {
		t.Fatalf("register gateway hook: %v", err)
	}
	runner := NewGatewayHookRunner(chain)
	// Contract fixtures bypass runtime permission enforcement to exercise only
	// the stdio protocol, projection, and result-validation behavior.
	handler := NewGatewayCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command)
	if err := runner.RegisterHandler(hooks[0], handler); err != nil {
		t.Fatalf("register gateway hook handler: %v", err)
	}
	return runner
}
