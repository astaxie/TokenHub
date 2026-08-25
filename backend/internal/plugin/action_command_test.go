package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestActionCommandRunnerExecutesStdioJSONAction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "action.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"action_id":"quota.read"'*'"id":"admin_1"'*)
    printf '{"data":{"ok":true}}'
    ;;
  *)
    printf 'unexpected invocation: %s' "$payload" >&2
    exit 2
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewActionCommandRunner(dir, "action.sh")
	result, err := runner.ExecutePluginAction(t.Context(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
		Actor:    ActionActor{ID: "admin_1"},
		Payload:  json.RawMessage(`{"resource_id":"rsrc_1"}`),
	})
	if err != nil {
		t.Fatalf("execute action command: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["ok"] != true {
		t.Fatalf("result data = %+v, want ok", data)
	}
}

func TestActionCommandRunnerRejectsEscapingCommandPath(t *testing.T) {
	runner := NewActionCommandRunner(t.TempDir(), "../action.sh")
	_, err := runner.ExecutePluginAction(t.Context(), ActionInvocation{
		PluginID: "tokenhub.test",
		ActionID: "quota.read",
	})
	if err == nil {
		t.Fatal("escaping command path was accepted")
	}
}
