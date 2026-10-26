package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadSave(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollS != 240 || cfg.MinLearningScore != 7 || cfg.UIPort != DefaultUIPort {
		t.Fatalf("defaults: %+v", cfg)
	}
	cfg.Country = "BE"
	cfg.Postcode = "9000"
	cfg.PollS = 10
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Postcode != "9000" || got.Country != "BE" {
		t.Errorf("got %+v", got)
	}
	if got.PollS != 180 {
		t.Errorf("poll interval %d not raised to the minimum", got.PollS)
	}
	info, err := os.Stat(filepath.Join(dir, "scharrelaar", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not honour Unix permission bits.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("config.yaml is readable by others: %o", info.Mode().Perm())
	}
}
