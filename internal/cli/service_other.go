//go:build !windows && !linux

package cli

import (
	"fmt"
	"runtime"
)

func enableAutostart(string) error {
	return fmt.Errorf("starting at login is not supported on %s", runtime.GOOS)
}

func disableAutostart() error { return nil }

func autostartStatus() string { return "not supported" }
