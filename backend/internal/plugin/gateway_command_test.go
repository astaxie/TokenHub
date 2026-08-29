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
