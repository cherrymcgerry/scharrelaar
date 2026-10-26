package main

import (
	"os/exec"
	"syscall"
)

// detachConsole closes the console window Windows opens at login for the
// process the Run key starts.
func detachConsole() {
	syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// spawnDetached starts a process with no console, outside the terminal's job, so
// closing the window it was started from does not end it.
func spawnDetached(bin string, args ...string) error {
	flags := uint32(detachedProcess | createNewProcessGroup)
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags | createBreakawayFromJob}
	if err := cmd.Start(); err != nil {
		// Some terminals run their children in a job that may not be left.
		cmd = exec.Command(bin, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
	}
	return cmd.Process.Release()
}
