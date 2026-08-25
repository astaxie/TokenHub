package plugin

import (
	"context"
	"strings"
	"time"
)

type BackgroundCommandRunner struct {
	Dir     string
	Command string
	Timeout time.Duration
}

func NewBackgroundCommandRunner(dir string, command string) BackgroundCommandRunner {
	return BackgroundCommandRunner{
		Dir:     strings.TrimSpace(dir),
		Command: strings.TrimSpace(command),
		Timeout: defaultActionCommandTimeout,
	}
}

func (r BackgroundCommandRunner) ExecuteBackgroundJob(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
	var result BackgroundJobResult
	if err := RunCommandJSON(ctx, r.Dir, r.Command, r.Timeout, invocation, &result); err != nil {
		return BackgroundJobResult{}, err
	}
	return result, nil
}
