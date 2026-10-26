package llm

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// GPU is one detected graphics card.
type GPU struct {
	Name    string
	VRAMMiB int
	Vendor  string // "nvidia" or "amd"
}

// Hardware is what the probe found.
type Hardware struct {
	GPUs   []GPU
	RAMMiB int
}

// BestVRAM returns the largest detected VRAM, in MiB.
func (h Hardware) BestVRAM() int {
	best := 0
	for _, g := range h.GPUs {
		if g.VRAMMiB > best {
			best = g.VRAMMiB
		}
	}
	return best
}

// Summary is a one-line description for the setup wizard.
func (h Hardware) Summary() string {
	ram := fmt.Sprintf("%d GB RAM", h.RAMMiB/1024)
	if len(h.GPUs) == 0 {
		return "no GPU detected, " + ram
	}
	parts := make([]string, len(h.GPUs))
	for i, g := range h.GPUs {
		parts[i] = fmt.Sprintf("%s (%d GB)", g.Name, g.VRAMMiB/1024)
	}
	return strings.Join(parts, ", ") + ", " + ram
}

// Probe detects GPUs and memory. Missing tools are not an error; the result then
// simply reports no GPU.
func Probe() Hardware {
	h := Hardware{RAMMiB: platformRAMMiB()}
	h.GPUs = append(h.GPUs, nvidiaGPUs()...)
	if len(h.GPUs) == 0 {
		h.GPUs = append(h.GPUs, platformAMDGPUs()...)
	}
	return h
}

// HasAMD reports whether an AMD card was found, which needs Ollama's ROCm add-on.
func (h Hardware) HasAMD() bool {
	for _, g := range h.GPUs {
		if g.Vendor == "amd" {
			return true
		}
	}
	return false
}

func nvidiaGPUs() []GPU {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var gpus []GPU
	s := bufio.NewScanner(bytes.NewReader(out))
	for s.Scan() {
		fields := strings.Split(s.Text(), ",")
		if len(fields) != 2 {
			continue
		}
		vram, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			continue
		}
		gpus = append(gpus, GPU{Name: strings.TrimSpace(fields[0]), VRAMMiB: vram, Vendor: "nvidia"})
	}
	return gpus
}

func amdGPUs() []GPU {
	out, err := exec.Command("rocm-smi", "--showmeminfo", "vram", "--csv").Output()
	if err != nil {
		return nil
	}
	// CSV: device,VRAM Total Memory (B),...
	var gpus []GPU
	s := bufio.NewScanner(bytes.NewReader(out))
	header := true
	for s.Scan() {
		if header {
			header = false
			continue
		}
		fields := strings.Split(s.Text(), ",")
		if len(fields) < 2 {
			continue
		}
		bytes, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil || bytes == 0 {
			continue
		}
		gpus = append(gpus, GPU{Name: "AMD GPU " + strings.TrimSpace(fields[0]), VRAMMiB: int(bytes / (1024 * 1024)), Vendor: "amd"})
	}
	return gpus
}
