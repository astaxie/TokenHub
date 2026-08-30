package plugin

import (
	"context"
	"strings"
	"time"
)

type BackgroundCommandRunner struct {
	Dir         string
	Command     string
	Timeout     time.Duration
	permissions PermissionGrant
}

func NewBackgroundCommandRunner(dir string, command string, permissions ...PermissionGrant) BackgroundCommandRunner {
	return BackgroundCommandRunner{
		Dir:         strings.TrimSpace(dir),
		Command:     strings.TrimSpace(command),
		Timeout:     defaultStdioJSONCommandTimeout,
		permissions: firstPermissionGrant(permissions),
	}
}

func (r BackgroundCommandRunner) PluginPermissionGrant() PermissionGrant {
	return r.permissions
}

func (r BackgroundCommandRunner) ExecuteBackgroundJob(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
	var result BackgroundJobResult
	if err := RunCommandJSON(ctx, r.Dir, r.Command, r.Timeout, invocation, &result); err != nil {
		return BackgroundJobResult{}, err
	}
	return result, nil
}
