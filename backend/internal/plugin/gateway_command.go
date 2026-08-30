package plugin

import (
	"context"
	"strings"
	"time"
)

type GatewayCommandRunner struct {
	Dir         string
	Command     string
	Timeout     time.Duration
	permissions PermissionGrant
}

func NewGatewayCommandRunner(dir string, command string, permissions ...PermissionGrant) GatewayCommandRunner {
	return GatewayCommandRunner{
		Dir:         strings.TrimSpace(dir),
		Command:     strings.TrimSpace(command),
		Timeout:     defaultStdioJSONCommandTimeout,
		permissions: firstPermissionGrant(permissions),
	}
}

func (r GatewayCommandRunner) PluginPermissionGrant() PermissionGrant {
	return r.permissions
}

func (r GatewayCommandRunner) ExecuteGatewayHook(ctx context.Context, input GatewayHookInput) (GatewayHookResult, error) {
	var result GatewayHookResult
	if err := runCommandJSON(ctx, CommandSandboxOptions{
		Dir:         r.Dir,
		Command:     r.Command,
		Timeout:     r.Timeout,
		Permissions: r.permissions,
		Plane:       CommandPlaneGateway,
	}, input, &result); err != nil {
		return GatewayHookResult{}, err
	}
	return result, nil
}
