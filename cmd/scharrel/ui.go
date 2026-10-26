package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/cherrymcgerry/scharrelaar/internal/config"
	"github.com/cherrymcgerry/scharrelaar/internal/web"
)

// ui opens the browser interface, first starting the background process when it
// is not already running. That process outlives this window and the page.
func ui(ctx context.Context) error {
	port := uiPort()
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	if probeUI(port) {
		fmt.Println("Scharrelaar is already running at", url)
		return openBrowser(url)
	}
	fmt.Println("Starting Scharrelaar in the background at", url)
	if err := startBackground(port); err != nil {
		return err
	}
	fmt.Println("It keeps running when you close this window or the page. Stop the alerts from the page.")
	return openBrowser(url)
}

func uiPort() int {
	cfg, err := config.Load()
	if err == nil && cfg.UIPort > 0 {
		return cfg.UIPort
	}
	return config.DefaultUIPort
}

// startBackground starts scharrel run --background and waits until it answers.
func startBackground(port int) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if err := spawnDetached(bin, "run", "--background"); err != nil {
		return fmt.Errorf("starting in the background: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if probeUI(port) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("scharrelaar did not start within 30 seconds; see scharrel logs")
}

// probeUI reports whether this app is answering on the port.
func probeUI(port int) bool {
	client := http.Client{Timeout: 400 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		App string `json:"app"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
	return body.App == "scharrelaar"
}

// postUI calls the running app the way its page does, so the origin check passes.
func postUI(port int, path string, out any) error {
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		return fmt.Errorf("%s", orDefault(e.Error, resp.Status))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// runningAlerts asks the running app for the alert state.
func runningAlerts(port int) (web.AlertState, bool) {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		return web.AlertState{}, false
	}
	defer resp.Body.Close()
	var body struct {
		App    string         `json:"app"`
		Alerts web.AlertState `json:"alerts"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil || body.App != "scharrelaar" {
		return web.AlertState{}, false
	}
	return body.Alerts, true
}

// openBrowser opens a URL with the system browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
