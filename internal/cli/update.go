package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
)

const repo = "cherrymcgerry/scharrelaar"

// Update replaces the running binary with the newest GitHub release.
func Update(current string) error {
	resp, err := http.Get("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checking for updates: HTTP %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return err
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == strings.TrimPrefix(current, "v") {
		fmt.Printf("Already on the newest version (%s).\n", current)
		return nil
	}
	asset := fmt.Sprintf("scharrelaar_%s_%s_%s", latest, runtime.GOOS, runtime.GOARCH)
	var url string
	for _, a := range release.Assets {
		if strings.HasPrefix(a.Name, asset) {
			url = a.URL
			break
		}
	}
	if url == "" {
		return fmt.Errorf("no download for %s/%s in version %s", runtime.GOOS, runtime.GOARCH, latest)
	}
	fmt.Printf("Updating %s -> %s\n", current, latest)

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	tmp := bin + ".new"
	if err := downloadFile(url, tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	backup := bin + ".old"
	os.Remove(backup)
	if err := os.Rename(bin, backup); err != nil {
		return fmt.Errorf("replacing the binary: %w", err)
	}
	if err := os.Rename(tmp, bin); err != nil {
		os.Rename(backup, bin)
		return err
	}
	fmt.Printf("Updated to %s. Restart scharrel to use it.\n", latest)
	return nil
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
