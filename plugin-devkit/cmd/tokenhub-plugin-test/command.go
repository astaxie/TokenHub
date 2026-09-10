package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const commandWaitDelay = time.Second

func executeJSONCommand(ctx context.Context, packageDir string, commandPath string, payload []byte) ([]byte, []byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, commandPath)
	cmd.Dir = packageDir
	configureCommandProcess(cmd)
	cmd.WaitDelay = commandWaitDelay
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return stdout.Bytes(), stdout.Bytes(), runCtx.Err()
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.Bytes(), stdout.Bytes(), fmt.Errorf("command failed: %s", message)
	}
	return stdout.Bytes(), stdout.Bytes(), nil
}
