package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// OllamaRelease is the standalone Ollama version the app downloads.
const OllamaRelease = "v0.40.11"

// Manager installs and runs a private copy of Ollama, separate from any Ollama the
// user already has, so the app never touches their models or port.
type Manager struct {
	// Dir holds the binary and the models. Defaults to the data directory.
	Dir string
	// Port is the private port. Defaults to 11435.
	Port int
	// Out receives progress messages.
	Out io.Writer
	// HostURL overrides the server address for this manager. Tests set it so they
	// do not share the package-wide HostOverride.
	HostURL string

	cmd     *exec.Cmd
	logFile *os.File
}

// HostOverride replaces the server address, for tests.
var HostOverride string

func (m *Manager) port() int {
	if m.Port == 0 {
		return 11435
	}
	return m.Port
}

func (m *Manager) dir() string { return m.Dir }

// installDir is where the Ollama archive is unpacked.
func (m *Manager) installDir() string { return filepath.Join(m.dir(), "ollama") }

// Host is the base URL of the private server.
func (m *Manager) Host() string {
	if m != nil && m.HostURL != "" {
		return m.HostURL
	}
	if HostOverride != "" {
		return HostOverride
	}
	return fmt.Sprintf("http://127.0.0.1:%d", m.port())
}

func (m *Manager) logf(format string, args ...any) {
	if m.Out != nil {
		fmt.Fprintf(m.Out, format+"\n", args...)
	}
}

// Binary is the path of the private ollama executable. The Linux archive keeps it
// in bin/, the Windows archive at the top level.
func (m *Manager) Binary() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.installDir(), "ollama.exe")
	}
	return filepath.Join(m.installDir(), "bin", "ollama")
}

// Installed reports whether the pinned version of the private binary is present.
func (m *Manager) Installed() bool {
	if _, err := os.Stat(m.Binary()); err != nil {
		return false
	}
	v, err := os.ReadFile(m.versionFile())
	return err == nil && string(v) == OllamaRelease
}

func (m *Manager) versionFile() string { return filepath.Join(m.installDir(), "VERSION") }

// Download fetches the standalone Ollama archives for this machine, checks them
// against the published checksums and unpacks them. AMD cards need the ROCm
// add-on unpacked over the base archive.
func (m *Manager) Download(ctx context.Context, hw Hardware) error {
	assets, err := ollamaAssets(runtime.GOOS, runtime.GOARCH, hw.HasAMD())
	if err != nil {
		return err
	}
	if err := os.RemoveAll(m.installDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(m.installDir(), 0o755); err != nil {
		return err
	}
	base := "https://github.com/ollama/ollama/releases/download/" + OllamaRelease + "/"
	for _, asset := range assets {
		want, err := fetchChecksum(ctx, base+"sha256sum.txt", asset)
		if err != nil {
			return err
		}
		archive := filepath.Join(m.dir(), asset)
		m.logf("downloading %s (Ollama %s)", asset, OllamaRelease)
		got, err := download(ctx, base+asset, archive, m.Out)
		if err != nil {
			os.Remove(archive)
			return err
		}
		if got != want {
			os.Remove(archive)
			return fmt.Errorf("%s is corrupt: checksum %s, expected %s", asset, got, want)
		}
		m.logf("unpacking %s", asset)
		err = unpack(archive, m.installDir())
		os.Remove(archive)
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(m.Binary()); err != nil {
		return fmt.Errorf("archive unpacked but %s is missing", m.Binary())
	}
	return os.WriteFile(m.versionFile(), []byte(OllamaRelease), 0o644)
}

func ollamaAssets(goos, goarch string, amd bool) ([]string, error) {
	var base, rocm string
	switch goos + "/" + goarch {
	case "linux/amd64":
		base, rocm = "ollama-linux-amd64.tar.zst", "ollama-linux-amd64-rocm.tar.zst"
	case "linux/arm64":
		base = "ollama-linux-arm64.tar.zst"
	case "windows/amd64":
		base, rocm = "ollama-windows-amd64.zip", "ollama-windows-amd64-rocm.zip"
	default:
		return nil, fmt.Errorf("no Ollama build for %s/%s", goos, goarch)
	}
	if amd && rocm != "" {
		return []string{base, rocm}, nil
	}
	return []string{base}, nil
}

// Start launches the private server and waits until it answers. When the server
// is already running (the background service, or another scharrel command) it is
// reused and Stop leaves it alone.
func (m *Manager) Start(ctx context.Context) error {
	if _, err := m.List(ctx); err == nil {
		return nil
	}
	if _, err := os.Stat(m.Binary()); err != nil {
		return errors.New("the model server is not installed; run: scharrel setup")
	}
	if err := os.MkdirAll(filepath.Join(m.dir(), "models"), 0o755); err != nil {
		return err
	}
	m.cmd = exec.Command(m.Binary(), "serve")
	m.cmd.Env = append(os.Environ(),
		fmt.Sprintf("OLLAMA_HOST=127.0.0.1:%d", m.port()),
		"OLLAMA_MODELS="+filepath.Join(m.dir(), "models"),
	)
	hideWindow(m.cmd)
	logFile, err := os.OpenFile(filepath.Join(m.dir(), "ollama.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	m.logFile = logFile
	m.cmd.Stdout = logFile
	m.cmd.Stderr = logFile
	if err := m.cmd.Start(); err != nil {
		m.Stop()
		return fmt.Errorf("starting ollama: %w", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := m.List(ctx); err == nil {
			m.logf("model server ready on %s", m.Host())
			return nil
		}
		select {
		case <-ctx.Done():
			m.Stop()
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	m.Stop()
	return errors.New("model server did not start within 60s; see ollama.log in the data directory")
}

// Stop shuts down the server if this manager started it.
func (m *Manager) Stop() {
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
		m.cmd.Wait()
	}
	m.cmd = nil
	if m.logFile != nil {
		m.logFile.Close()
		m.logFile = nil
	}
}

// List returns the names of the models that are already pulled.
func (m *Manager) List(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Host()+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model server answered HTTP %d", resp.StatusCode)
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	names := make([]string, len(body.Models))
	for i, model := range body.Models {
		names[i] = model.Name
	}
	return names, nil
}

// Pull downloads a model, reporting progress in percent.
func (m *Manager) Pull(ctx context.Context, model string) error {
	payload, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Host()+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("pulling %s: HTTP %d: %s", model, resp.StatusCode, body)
	}
	s := bufio.NewScanner(resp.Body)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	last := -1
	for s.Scan() {
		var line struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(s.Bytes(), &line); err != nil {
			continue
		}
		if line.Error != "" {
			return errors.New(line.Error)
		}
		if line.Total > 0 {
			pct := int(100 * line.Completed / line.Total)
			if pct != last {
				m.logf("downloading %s: %d%%", model, pct)
				last = pct
			}
		}
	}
	return s.Err()
}

// Has reports whether a model is already pulled.
func (m *Manager) Has(ctx context.Context, model string) bool {
	names, err := m.List(ctx)
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == model {
			return true
		}
	}
	return false
}
