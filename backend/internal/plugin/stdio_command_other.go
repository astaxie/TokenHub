//go:build !unix && !windows

package plugin

import "os/exec"

func configureStdioCommandProcess(*exec.Cmd) {}
