//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

func configureClaudeSubscriptionProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
func killClaudeSubscriptionProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
