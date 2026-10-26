//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func detachConsole() {}

// spawnDetached starts a process in its own session, so closing the terminal it
// was started from does not end it.
func spawnDetached(bin string, args ...string) error {
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
