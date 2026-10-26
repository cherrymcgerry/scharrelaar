package llm

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// download saves url to dest and returns the file's SHA-256.
func download(ctx context.Context, url, dest string, out io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()

	src := io.Reader(resp.Body)
	if out != nil && resp.ContentLength > 0 {
		src = &progressReader{r: resp.Body, total: resp.ContentLength, out: out}
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), src); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChecksum returns the published SHA-256 of asset from a sha256sum.txt file.
func fetchChecksum(ctx context.Context, url, asset string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)
	}
	return parseChecksum(resp.Body, asset)
}

func parseChecksum(r io.Reader, asset string) (string, error) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no checksum published for %s", asset)
}

type progressReader struct {
	r     io.Reader
	read  int64
	total int64
	last  int
	out   io.Writer
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	pct := int(100 * p.read / p.total)
	if pct/10 != p.last/10 {
		fmt.Fprintf(p.out, "download: %d%%\n", pct)
		p.last = pct
	}
	return n, err
}

func unpack(archive, dest string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return unzip(archive, dest)
	case strings.HasSuffix(archive, ".tgz") || strings.HasSuffix(archive, ".tar.gz"):
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		return untar(gz, dest)
	case strings.HasSuffix(archive, ".tar.zst"):
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		zr, err := zstd.NewReader(f)
		if err != nil {
			return err
		}
		defer zr.Close()
		return untar(zr, dest)
	default:
		return fmt.Errorf("unknown archive type: %s", archive)
	}
}

func unzip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if err := extractOne(f.Name, dest, f.FileInfo(), f.Open); err != nil {
			return err
		}
	}
	return nil
}

func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			if err := symlink(hdr.Name, hdr.Linkname, dest); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeDir:
			if err := extractOne(hdr.Name, dest, hdr.FileInfo(), func() (io.ReadCloser, error) {
				return io.NopCloser(tr), nil
			}); err != nil {
				return err
			}
		}
	}
}

func safeTarget(name, dest string) (string, error) {
	target := filepath.Join(dest, filepath.Clean(name))
	if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

func symlink(name, link, dest string) error {
	target, err := safeTarget(name, dest)
	if err != nil {
		return err
	}
	if filepath.IsAbs(link) {
		return fmt.Errorf("archive link %s points outside the archive", name)
	}
	if _, err := safeTarget(filepath.Join(filepath.Dir(name), link), dest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	os.Remove(target)
	return os.Symlink(link, target)
}

func extractOne(name, dest string, info os.FileInfo, open func() (io.ReadCloser, error)) error {
	target, err := safeTarget(name, dest)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	r, err := open()
	if err != nil {
		return err
	}
	defer r.Close()
	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, r)
	return err
}
