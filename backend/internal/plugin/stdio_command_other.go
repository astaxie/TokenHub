//go:build !unix && !windows

package plugin

import "os/exec"

func configureStdioCommandProcess(*exec.Cmd) {}

func commandProcessEnforcementStatus() SandboxEnforcementStatus {
	return SandboxEnforcementUnsupported
}
