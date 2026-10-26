package llm

import (
	"archive/tar"
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestUnpackZip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("bin/ollama.exe")
	if err != nil {
		t.Fatal(err)
	}
	entry.Write([]byte("fake"))
	w.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	if err := unpack(archive, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "bin", "ollama.exe"))
	if err != nil || string(got) != "fake" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestUnpackRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	w.Create("../escape.txt")
	w.Close()
	f.Close()

	if err := unpack(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("path escape was accepted")
	}
}

func TestOllamaAssets(t *testing.T) {
	got, err := ollamaAssets("windows", "amd64", true)
	if err != nil || len(got) != 2 || got[0] != "ollama-windows-amd64.zip" || got[1] != "ollama-windows-amd64-rocm.zip" {
		t.Fatalf("windows amd: %v %v", got, err)
	}
	got, err = ollamaAssets("linux", "amd64", false)
	if err != nil || len(got) != 1 || got[0] != "ollama-linux-amd64.tar.zst" {
		t.Fatalf("linux nvidia: %v %v", got, err)
	}
	if _, err := ollamaAssets("darwin", "arm64", false); err == nil {
		t.Fatal("darwin accepted")
	}
}

func TestParseChecksum(t *testing.T) {
	sums := "abc123  ./ollama-linux-amd64.tar.zst\nDEF456  ./ollama-windows-amd64.zip\n"
	got, err := parseChecksum(strings.NewReader(sums), "ollama-windows-amd64.zip")
	if err != nil || got != "def456" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := parseChecksum(strings.NewReader(sums), "ollama-linux-arm64.tar.zst"); err == nil {
		t.Fatal("missing asset accepted")
	}
}

func TestUnpackTarZstWithSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs developer mode on Windows; the .tar.zst archives are Linux-only")
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.zst")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw, _ := zstd.NewWriter(f)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: "bin/ollama", Mode: 0o755, Size: 4, Typeflag: tar.TypeReg})
	tw.Write([]byte("fake"))
	tw.WriteHeader(&tar.Header{Name: "lib/ollama/libggml.so.1", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("so"))
	tw.WriteHeader(&tar.Header{Name: "lib/ollama/libggml.so", Linkname: "libggml.so.1", Typeflag: tar.TypeSymlink})
	tw.Close()
	zw.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	if err := unpack(archive, dest); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "lib", "ollama", "libggml.so")); err != nil || string(got) != "so" {
		t.Fatalf("symlink: %q %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(dest, "bin", "ollama")); err != nil || info.Mode()&0o100 == 0 {
		t.Fatalf("binary not executable: %v %v", info, err)
	}
}

func TestBinaryPath(t *testing.T) {
	m := &Manager{Dir: "data"}
	want := filepath.Join("data", "ollama", "bin", "ollama")
	if runtime.GOOS == "windows" {
		want = filepath.Join("data", "ollama", "ollama.exe")
	}
	if m.Binary() != want {
		t.Fatalf("binary %s, want %s", m.Binary(), want)
	}
}
