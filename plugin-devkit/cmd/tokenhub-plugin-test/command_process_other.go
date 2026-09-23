//go:build !unix && !windows

package main

import "os/exec"

func configureCommandProcess(*exec.Cmd) {}
