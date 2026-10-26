package llm

import (
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func platformRAMMiB() int {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return 0
	}
	return int(m.TotalPhys / (1024 * 1024))
}

// platformAMDGPUs finds Radeon cards by name. Windows does not report their VRAM
// reliably (Win32_VideoController caps it at 4 GB), so VRAM stays 0 and the model
// choice falls back to system memory.
func platformAMDGPUs() []GPU {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_VideoController | ForEach-Object { $_.Name }")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		lower := strings.ToLower(name)
		if strings.Contains(lower, "radeon") || strings.HasPrefix(lower, "amd") {
			gpus = append(gpus, GPU{Name: name, Vendor: "amd"})
		}
	}
	return gpus
}
