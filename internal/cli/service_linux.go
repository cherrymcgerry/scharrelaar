package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func unitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", serviceName+".service"), nil
}

func enableAutostart(bin string) error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=Scharrelaar classifieds alerts

[Service]
ExecStart="%s" run --background
Restart=on-failure
RestartSec=30

[Install]
WantedBy=default.target
`, bin)
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("start at login: systemctl daemon-reload: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("start at login: systemctl enable: %s", strings.TrimSpace(string(out)))
	}
	// Lingering starts user services at boot rather than at login. Most systems
	// allow it for your own account; where they do not, it starts at login.
	exec.Command("loginctl", "enable-linger").Run()
	return nil
}

func disableAutostart() error {
	path, err := unitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if out, err := exec.Command("systemctl", "--user", "disable", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("start at login: systemctl disable: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func autostartStatus() string {
	out, _ := exec.Command("systemctl", "--user", "is-enabled", serviceName).Output()
	if strings.TrimSpace(string(out)) == "enabled" {
		return "starts at login"
	}
	return "off"
}
