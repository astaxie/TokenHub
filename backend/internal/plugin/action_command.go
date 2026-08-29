package plugin

import (
	"context"
	"strings"
	"time"
)

const (
	BackendProtocolStdioJSONV1 = "stdio-json-v1"
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
		Timeout: defaultStdioJSONCommandTimeout,
	}
}

func (r ActionCommandRunner) ExecutePluginAction(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	var result ActionResult
	if err := RunCommandJSON(ctx, r.Dir, r.Command, r.Timeout, invocation, &result); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}
