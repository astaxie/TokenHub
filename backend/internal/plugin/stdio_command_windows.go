//go:build windows

package plugin

import (
	"os"
	"os/exec"
	"strconv"
)

func configureStdioCommandProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		return kill.Run()
	}
}
