package llm

import "fmt"

// ModelChoice is one model the user can run.
type ModelChoice struct {
	Name     string
	VRAMMiB  int // recommended minimum VRAM; 0 means CPU
	SizeGB   int // approximate download size
	Quality  string
	Fallback bool // true when it only runs on CPU
}

// String describes the choice for the wizard.
func (m ModelChoice) String() string {
	where := fmt.Sprintf("%d GB VRAM", m.VRAMMiB/1024)
	if m.Fallback {
		where = "CPU"
	}
	return fmt.Sprintf("%s (%s, %d GB download, %s)", m.Name, where, m.SizeGB, m.Quality)
}

// catalogue is ordered from most to least capable.
// The qwen3 models think before they answer, which is what lets them work out
// what an indirect wish means; qwen2.5 of the same size reads it literally.
var catalogue = []ModelChoice{
	{"qwen3:14b", 12 * 1024, 9, "best", false},
	{"qwen3:8b", 8 * 1024, 5, "good", false},
	{"qwen3:4b", 6 * 1024, 3, "basic", false},
	{"qwen3:8b", 0, 5, "good, slow", true},
}

// Recommend picks a model for the detected hardware. The bool is false when nothing
// reasonable fits, so the caller can warn and ask the user to choose explicitly.
func Recommend(h Hardware) (ModelChoice, bool) {
	vram := h.BestVRAM()
	for _, m := range catalogue {
		if m.Fallback {
			continue
		}
		if vram >= m.VRAMMiB {
			return m, true
		}
	}
	if h.RAMMiB >= 16*1024 {
		return catalogue[len(catalogue)-1], true
	}
	return ModelChoice{}, false
}

// visionCatalogue lists the models that can look at photos, most capable first.
var visionCatalogue = []ModelChoice{
	{"qwen2.5vl:7b", 8 * 1024, 6, "reads labels and model names well", false},
	{"qwen2.5vl:3b", 5 * 1024, 3, "basic", false},
}

// RecommendVision picks a photo model for the hardware. Photos need a graphics
// card; on the processor they are too slow, so the bool is false there.
func RecommendVision(h Hardware) (ModelChoice, bool) {
	vram := h.BestVRAM()
	for _, m := range visionCatalogue {
		if vram >= m.VRAMMiB {
			return m, true
		}
	}
	return ModelChoice{}, false
}

// Choices lists every model the user may pick.
func Choices() []ModelChoice { return append([]ModelChoice(nil), catalogue...) }
