package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecuteJSONCommandReportsExitStatusWithoutStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	commandPath := writeCommandFixture(t, "#!/bin/sh\nexit 7\n")

	_, _, err := executeJSONCommand(t.Context(), filepath.Dir(commandPath), commandPath, nil)
	if err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("error = %v, want exit status", err)
	}
}

func TestExecuteJSONCommandTimeoutKillsChildProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	marker := filepath.Join(t.TempDir(), "child-survived")
	t.Setenv("TOKENHUB_DEVKIT_TEST_MARKER", marker)
	commandPath := writeCommandFixture(t, "#!/bin/sh\n(sleep 1; printf child-survived > \"$TOKENHUB_DEVKIT_TEST_MARKER\") &\nwait\n")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	_, _, err := executeJSONCommand(ctx, filepath.Dir(commandPath), commandPath, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("command timeout left a child process alive; marker %s was written", marker)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat child marker: %v", statErr)
	}
}

func writeCommandFixture(t *testing.T, contents string) string {
	t.Helper()
	commandPath := filepath.Join(t.TempDir(), "plugin-command.sh")
	if err := os.WriteFile(commandPath, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return commandPath
}
