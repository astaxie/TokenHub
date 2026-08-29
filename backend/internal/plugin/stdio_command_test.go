package plugin

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

const stdioCommandTestTimeout = 5 * time.Second

func TestStdioJSONCommandFailureModesAcrossPlanes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	for _, plane := range stdioCommandPlanes() {
		t.Run(plane.name+"/stderr", func(t *testing.T) {
			dir, command := writeStdioCommandScript(t, `#!/bin/sh
printf 'plane rejected request' >&2
exit 7
`)
			err := plane.run(t.Context(), dir, command, stdioCommandTestTimeout)
			if err == nil {
				t.Fatal("stdio command succeeded after non-zero exit")
			}
			if !strings.Contains(err.Error(), "plugin command failed: plane rejected request") {
				t.Fatalf("error = %q", err.Error())
			}
		})

		t.Run(plane.name+"/invalid JSON", func(t *testing.T) {
			dir, command := writeStdioCommandScript(t, `#!/bin/sh
printf 'not-json'
`)
			err := plane.run(t.Context(), dir, command, stdioCommandTestTimeout)
			if err == nil {
				t.Fatal("stdio command succeeded with invalid JSON output")
			}
			if !strings.Contains(err.Error(), "decode plugin command output") {
				t.Fatalf("error = %q", err.Error())
			}
		})

		t.Run(plane.name+"/timeout", func(t *testing.T) {
			dir, command := writeStdioCommandScript(t, `#!/bin/sh
sleep 2
printf '{}'
`)
			err := plane.run(t.Context(), dir, command, 20*time.Millisecond)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context deadline exceeded", err)
			}
		})

		t.Run(plane.name+"/cancel", func(t *testing.T) {
			dir, command := writeStdioCommandScript(t, `#!/bin/sh
sleep 2
printf '{}'
`)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err := plane.run(ctx, dir, command, stdioCommandTestTimeout)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context canceled", err)
			}
		})
	}
}

func TestStdioJSONCommandIgnoresSuccessStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir, command := writeStdioCommandScript(t, `#!/bin/sh
printf 'debug line' >&2
printf '{"data":{"ok":true}}'
`)
	var result ActionResult
	if err := RunCommandJSON(t.Context(), dir, command, stdioCommandTestTimeout, ActionInvocation{PluginID: "tokenhub.test", ActionID: "ok"}, &result); err != nil {
		t.Fatalf("run stdio command: %v", err)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["ok"] != true {
		t.Fatalf("result data = %+v", result.Data)
	}
}

func TestStdioJSONCommandRejectsOversizedStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	dir, command := writeStdioCommandScript(t, `#!/bin/sh
dd if=/dev/zero bs=1048577 count=1 2>/dev/null | tr '\000' x
`)
	err := RunCommandJSON(t.Context(), dir, command, stdioCommandTestTimeout, ActionInvocation{PluginID: "tokenhub.test", ActionID: "too-large"}, &ActionResult{})
	if err == nil {
		t.Fatal("oversized stdio command output was accepted")
	}
	if !strings.Contains(err.Error(), "plugin command output exceeded") {
		t.Fatalf("error = %q", err.Error())
	}
}

type stdioCommandPlane struct {
	name string
	run  func(context.Context, string, string, time.Duration) error
}

func stdioCommandPlanes() []stdioCommandPlane {
	return []stdioCommandPlane{
		{
			name: "action",
			run: func(ctx context.Context, dir string, command string, timeout time.Duration) error {
				runner := ActionCommandRunner{Dir: dir, Command: command, Timeout: timeout}
				_, err := runner.ExecutePluginAction(ctx, ActionInvocation{PluginID: "tokenhub.test", ActionID: "run"})
				return err
			},
		},
		{
			name: "gateway",
			run: func(ctx context.Context, dir string, command string, timeout time.Duration) error {
				runner := GatewayCommandRunner{Dir: dir, Command: command, Timeout: timeout}
				_, err := runner.ExecuteGatewayHook(ctx, GatewayHookInput{RequestID: "req_1", Stage: StagePrivacyPre})
				return err
			},
		},
		{
			name: "background",
			run: func(ctx context.Context, dir string, command string, timeout time.Duration) error {
				runner := BackgroundCommandRunner{Dir: dir, Command: command, Timeout: timeout}
				_, err := runner.ExecuteBackgroundJob(ctx, BackgroundJobInvocation{PluginID: "tokenhub.test", JobID: "run"})
				return err
			},
		},
		{
			name: "provider",
			run: func(ctx context.Context, dir string, command string, timeout time.Duration) error {
				runner := ProviderCommandRunner{Dir: dir, Command: command, Timeout: timeout}
				var result struct {
					Response map[string]any `json:"response"`
				}
				return runner.ExecuteProviderCommand(ctx, ProviderCommandRequest{Operation: "chat", ProviderModel: "model"}, &result)
			},
		},
	}
}

func writeStdioCommandScript(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	name := "command.sh"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write command script: %v", err)
	}
	return dir, name
}
