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
	Dir         string
	Command     string
	Timeout     time.Duration
	permissions PermissionGrant
}

func NewActionCommandRunner(dir string, command string, permissions ...PermissionGrant) ActionCommandRunner {
	return ActionCommandRunner{
		Dir:         strings.TrimSpace(dir),
		Command:     strings.TrimSpace(command),
		Timeout:     defaultStdioJSONCommandTimeout,
		permissions: firstPermissionGrant(permissions),
	}
}

func (r ActionCommandRunner) PluginPermissionGrant() PermissionGrant {
	return r.permissions
}

func (r ActionCommandRunner) ExecutePluginAction(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
	var result ActionResult
	if err := runCommandJSON(ctx, CommandSandboxOptions{
		Dir:         r.Dir,
		Command:     r.Command,
		Timeout:     r.Timeout,
		Permissions: r.permissions,
		Plane:       CommandPlaneAction,
	}, invocation, &result); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}
