package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultStdioJSONCommandTimeout = 30 * time.Second
	maxStdioJSONCommandInputBytes  = 4 << 20
	maxStdioJSONCommandOutputBytes = 1 << 20
)

func RunCommandJSON(ctx context.Context, dir string, command string, timeout time.Duration, input any, output any) error {
	return runCommandJSON(ctx, CommandSandboxOptions{
		Dir:     dir,
		Command: command,
		Timeout: timeout,
	}, input, output)
}

func runCommandJSON(ctx context.Context, options CommandSandboxOptions, input any, output any) error {
	if strings.TrimSpace(options.Command) == "" {
		return ErrPluginActionUnavailable
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultStdioJSONCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode plugin command input: %w", err)
	}
	if len(payload) > maxStdioJSONCommandInputBytes {
		return fmt.Errorf("plugin command input exceeded %d bytes", maxStdioJSONCommandInputBytes)
	}
	tempDir, err := os.MkdirTemp("", "tokenhub-plugin-command-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	options.TempDir = tempDir
	policy, err := BuildCommandSandboxPolicy(options)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(runCtx, policy.Executable)
	cmd.Dir = policy.WorkDir
	cmd.Env = policy.Env
	configureStdioCommandProcess(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout cappedBuffer
	var stderr cappedBuffer
	stdout.limit = policy.OutputLimitBytes
	stderr.limit = policy.StderrLimitBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("plugin command failed: %s", message)
	}
	if stdout.overflow {
		return fmt.Errorf("plugin command output exceeded %d bytes", maxStdioJSONCommandOutputBytes)
	}
	rawOutput := bytes.TrimSpace(stdout.bytes)
	if len(rawOutput) == 0 || output == nil {
		return nil
	}
	if err := json.Unmarshal(rawOutput, output); err != nil {
		return fmt.Errorf("decode plugin command output: %w", err)
	}
	return nil
}

type cappedBuffer struct {
	bytes    []byte
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - len(b.bytes)
	if remaining <= 0 {
		b.overflow = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.bytes = append(b.bytes, p[:remaining]...)
		b.overflow = true
		return len(p), nil
	}
	b.bytes = append(b.bytes, p...)
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return string(b.bytes)
}
