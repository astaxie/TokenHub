package plugin

import (
	"context"
	"strings"
	"time"
)

type GatewayCommandRunner struct {
	Dir     string
	Command string
	Timeout time.Duration
}

func NewGatewayCommandRunner(dir string, command string) GatewayCommandRunner {
	return GatewayCommandRunner{
		Dir:     strings.TrimSpace(dir),
		Command: strings.TrimSpace(command),
		Timeout: defaultStdioJSONCommandTimeout,
	}
}

func (r GatewayCommandRunner) ExecuteGatewayHook(ctx context.Context, input GatewayHookInput) (GatewayHookResult, error) {
	var result GatewayHookResult
	if err := RunCommandJSON(ctx, r.Dir, r.Command, r.Timeout, input, &result); err != nil {
		return GatewayHookResult{}, err
	}
	return result, nil
}
