//go:build windows

package server

import "os/exec"

func configureClaudeSubscriptionProcess(_ *exec.Cmd) {}
func killClaudeSubscriptionProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
