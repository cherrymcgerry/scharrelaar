package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// runKey starts programs at login for this user only; writing it needs no
// administrator rights, unlike a logon scheduled task.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func enableAutostart(bin string) error {
	removeOldTask()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("start at login: %w", err)
	}
	defer k.Close()
	return k.SetStringValue(serviceName, fmt.Sprintf(`"%s" run --background`, bin))
}

func disableAutostart() error {
	removeOldTask()
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("start at login: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(serviceName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("start at login: %w", err)
	}
	return nil
}

func autostartStatus() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return "off"
	}
	defer k.Close()
	if _, _, err := k.GetStringValue(serviceName); err != nil {
		return "off"
	}
	return "starts at login"
}

// removeOldTask deletes the logon scheduled task earlier versions created, so
// the app does not start twice.
func removeOldTask() {
	cmd := exec.Command("schtasks", "/Delete", "/F", "/TN", serviceName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	cmd.Run()
}
