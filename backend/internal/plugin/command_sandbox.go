package plugin

import (
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

type CommandPlane string

const (
	CommandPlaneAction     CommandPlane = "action"
	CommandPlaneGateway    CommandPlane = "gateway"
	CommandPlaneBackground CommandPlane = "background"
	CommandPlaneProvider   CommandPlane = "provider"
)

type SandboxEnforcementStatus string

const (
	SandboxEnforcementEnforced    SandboxEnforcementStatus = "enforced"
	SandboxEnforcementUnsupported SandboxEnforcementStatus = "unsupported"
)

type CommandSandboxOptions struct {
	Dir         string
	Command     string
	Timeout     time.Duration
	Permissions PermissionGrant
	Plane       CommandPlane
	TempDir     string
}

type CommandSandboxPolicy struct {
	Env                 []string
	WorkDir             string
	Executable          string
	AllowedNetwork      []string
	TempDir             string
	OutputLimitBytes    int
	StderrLimitBytes    int
	ProcessEnforcement  SandboxEnforcementStatus
	NetworkEnforcement  SandboxEnforcementStatus
	ResourceEnforcement SandboxEnforcementStatus
}

func BuildCommandSandboxPolicy(options CommandSandboxOptions) (CommandSandboxPolicy, error) {
	executable, err := packageRelativePath(options.Dir, options.Command)
	if err != nil {
		return CommandSandboxPolicy{}, err
	}
	tempDir := strings.TrimSpace(options.TempDir)
	policy := CommandSandboxPolicy{
		Env:                 sandboxCommandEnv(tempDir),
		WorkDir:             strings.TrimSpace(options.Dir),
		Executable:          executable,
		AllowedNetwork:      sandboxAllowedNetwork(options.Permissions),
		TempDir:             tempDir,
		OutputLimitBytes:    maxStdioJSONCommandOutputBytes,
		StderrLimitBytes:    maxStdioJSONCommandOutputBytes,
		ProcessEnforcement:  commandProcessEnforcementStatus(),
		NetworkEnforcement:  SandboxEnforcementUnsupported,
		ResourceEnforcement: SandboxEnforcementUnsupported,
	}
	return policy, nil
}

func sandboxCommandEnv(tempDir string) []string {
	env := []string{
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"PATH=" + sandboxDefaultPath(),
	}
	if tempDir != "" {
		env = append(env,
			"TMPDIR="+tempDir,
			"TMP="+tempDir,
			"TEMP="+tempDir,
		)
	}
	if runtime.GOOS == "windows" {
		if systemRoot := strings.TrimSpace(os.Getenv("SystemRoot")); systemRoot != "" {
			env = append(env, "SystemRoot="+systemRoot)
		}
	}
	sort.Strings(env)
	return env
}

func sandboxDefaultPath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32;C:\Windows;C:\Windows\System32\WindowsPowerShell\v1.0`
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

func sandboxAllowedNetwork(grant PermissionGrant) []string {
	var allowed []string
	for _, permission := range NormalizePermissionDescriptors(grant.Permissions) {
		if permission.Kind == PermissionKindNetwork && permission.Access == PermissionAccessConnect {
			allowed = append(allowed, strings.TrimSpace(permission.Name))
		}
	}
	sort.Strings(allowed)
	return allowed
}
