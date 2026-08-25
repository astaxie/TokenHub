package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	BackendProtocolStdioJSONV1  = "stdio-json-v1"
	defaultActionCommandTimeout = 30 * time.Second
	maxActionCommandOutputBytes = 1 << 20
)

type ActionCommandRunner struct {
	Dir     string
	Command string
	Timeout time.Duration
}

func NewActionCommandRunner(dir string, command string) ActionCommandRunner {
	return ActionCommandRunner{
		Dir:     strings.TrimSpace(dir),
		Command: strings.TrimSpace(command),
		Timeout: defaultActionCommandTimeout,
	}
}

func (r ActionCommandRunner) ExecutePluginAction(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	if strings.TrimSpace(r.Command) == "" {
		return ActionResult{}, ErrPluginActionUnavailable
	}
	commandPath, err := packageRelativePath(r.Dir, r.Command)
	if err != nil {
		return ActionResult{}, err
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultActionCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input, err := json.Marshal(invocation)
	if err != nil {
		return ActionResult{}, fmt.Errorf("encode plugin action invocation: %w", err)
	}
	cmd := exec.CommandContext(runCtx, commandPath)
	cmd.Dir = r.Dir
	cmd.Stdin = bytes.NewReader(input)
	var stdout cappedBuffer
	var stderr cappedBuffer
	stdout.limit = maxActionCommandOutputBytes
	stderr.limit = maxActionCommandOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return ActionResult{}, runCtx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return ActionResult{}, fmt.Errorf("plugin action command failed: %s", message)
	}
	if stdout.overflow {
		return ActionResult{}, fmt.Errorf("plugin action command output exceeded %d bytes", maxActionCommandOutputBytes)
	}
	output := bytes.TrimSpace(stdout.bytes)
	if len(output) == 0 {
		return ActionResult{}, nil
	}
	var result ActionResult
	if err := json.Unmarshal(output, &result); err != nil {
		return ActionResult{}, fmt.Errorf("decode plugin action command output: %w", err)
	}
	return result, nil
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
