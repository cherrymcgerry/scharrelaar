//go:build !windows

package llm

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func hideWindow(*exec.Cmd) {}

func platformRAMMiB() int {
	f, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(f), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.Atoi(fields[1])
				return kb / 1024
			}
		}
	}
	return 0
}

func platformAMDGPUs() []GPU { return amdGPUs() }
