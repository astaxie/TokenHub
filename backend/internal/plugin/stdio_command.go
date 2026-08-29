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
	defaultStdioJSONCommandTimeout = 30 * time.Second
	maxStdioJSONCommandOutputBytes = 1 << 20
)

func RunCommandJSON(ctx context.Context, dir string, command string, timeout time.Duration, input any, output any) error {
	if strings.TrimSpace(command) == "" {
		return ErrPluginActionUnavailable
	}
	commandPath, err := packageRelativePath(dir, command)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = defaultStdioJSONCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode plugin command input: %w", err)
	}
	cmd := exec.CommandContext(runCtx, commandPath)
	cmd.Dir = dir
	configureStdioCommandProcess(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout cappedBuffer
	var stderr cappedBuffer
	stdout.limit = maxStdioJSONCommandOutputBytes
	stderr.limit = maxStdioJSONCommandOutputBytes
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
