package cli

import (
	"os"
	"path/filepath"
)

const serviceName = "scharrelaar"

// EnableAutostart makes the background process start when the user logs in.
func EnableAutostart() error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	return enableAutostart(bin)
}

// DisableAutostart stops the background process from starting at login. A
// process that is already running keeps running.
func DisableAutostart() error {
	return disableAutostart()
}

// ServiceStatus describes start at login: "starts at login", "off" or "not supported".
func ServiceStatus() string {
	return autostartStatus()
}
