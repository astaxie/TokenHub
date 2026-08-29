package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBackgroundCommandRunnerExecutesStdioJSONBackgroundJob(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "background.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"plugin_id":"tokenhub.jobs"'*'"job_id":"quota.refresh"'*'"resource_id":"rsrc_1"'*)
    printf '{"data":{"resource_id":"rsrc_1"}}'
    ;;
  *)
    printf 'unexpected background payload: %s' "$payload" >&2
    exit 2
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewBackgroundCommandRunner(dir, "background.sh")
	result, err := runner.ExecuteBackgroundJob(t.Context(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
		Payload:  json.RawMessage(`{"resource_id":"rsrc_1"}`),
	})
	if err != nil {
		t.Fatalf("execute background command: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["resource_id"] != "rsrc_1" {
		t.Fatalf("result data = %+v, want resource id", data)
	}
}

func TestBackgroundCommandRunnerRejectsEscapingCommandPath(t *testing.T) {
	runner := NewBackgroundCommandRunner(t.TempDir(), "../background.sh")
	_, err := runner.ExecuteBackgroundJob(t.Context(), BackgroundJobInvocation{
		PluginID: "tokenhub.jobs",
		JobID:    "quota.refresh",
	})
	if err == nil {
		t.Fatal("escaping command path was accepted")
	}
}
