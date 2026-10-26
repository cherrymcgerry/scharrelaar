// Package config reads and writes the user's configuration and finds the
// directories the app keeps its files in.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

const keyringService = "scharrelaar"

// Config is everything in config.yaml.
type Config struct {
	Country   string   `yaml:"country"` // "BE" or "NL"
	Postcode  string   `yaml:"postcode"`
	DistanceM int      `yaml:"distance_m"`
	Sites     []string `yaml:"sites"`
	Model     string   `yaml:"model"`
	// VisionModel judges listings with their photos. Empty means text only.
	VisionModel string `yaml:"vision_model,omitempty"`
	ChatID      string `yaml:"chat_id"`
	// PollS is the base interval between scans. The minimum is 180 seconds.
	PollS int `yaml:"poll_s"`
	// MinDealScore and MinLearningScore are the alert thresholds.
	MinDealScore     int `yaml:"min_deal_score"`
	MinLearningScore int `yaml:"min_learning_score"`
	// Digest is "off" or "daily".
	Digest string `yaml:"digest"`
	// UIPort is the localhost port of the browser interface. 0 means the default.
	UIPort int `yaml:"ui_port,omitempty"`
	// AlertsOff is set when the user stopped the alerts. It survives a restart.
	AlertsOff bool `yaml:"alerts_off,omitempty"`
	// WebLookup lets the scanner search the web for the brand and model an ad
	// names. Off by default: it is the one thing that sends text off the machine
	// other than to the marketplaces.
	WebLookup bool `yaml:"web_lookup,omitempty"`
}

// Default returns a configuration with sensible defaults.
// DefaultUIPort is the localhost port of the browser interface.
const DefaultUIPort = 8765

func Default() Config {
	return Config{DistanceM: 50000, PollS: 240, MinDealScore: 4, MinLearningScore: 7, Digest: "off", UIPort: DefaultUIPort}
}

// ConfigDir is where config.yaml lives.
func ConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(base, "scharrelaar"), nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "scharrelaar"), nil
}

// DataDir is where the database, models and logs live.
func DataDir() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(base, "scharrelaar", "data"), nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "scharrelaar"), nil
}

// ConfigPath is the path of config.yaml.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// Load reads the configuration. A missing file returns the defaults.
func Load() (Config, error) {
	cfg := Default()
	path, err := ConfigPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.PollS < 180 {
		cfg.PollS = 180
	}
	if cfg.UIPort < 1 || cfg.UIPort > 65535 {
		cfg.UIPort = DefaultUIPort
	}
	return cfg, nil
}

// Save writes the configuration, creating the directory if needed.
func Save(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// SetAlerts records whether alerts should run.
func SetAlerts(on bool) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.AlertsOff = !on
	return Save(cfg)
}

// SetWebLookup records whether brand and model may be looked up on the web.
func SetWebLookup(on bool) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.WebLookup = on
	return Save(cfg)
}

// useKeyring is false on Linux: the secret service is usually locked or absent for a
// systemd user service, which would leave the background scanner without a token.
var useKeyring = runtime.GOOS == "windows"

// SetToken stores the Telegram bot token in Windows Credential Manager, or on Linux
// in a file only the user can read.
func SetToken(token string) error {
	if useKeyring {
		if err := keyring.Set(keyringService, "telegram", token); err == nil {
			return nil
		}
	}
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "token"), []byte(token), 0o600)
}

// Token reads the Telegram bot token.
func Token() (string, error) {
	if useKeyring {
		if token, err := keyring.Get(keyringService, "telegram"); err == nil && token != "" {
			return token, nil
		}
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "token"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
