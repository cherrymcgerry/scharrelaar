package llm

import "testing"

func TestRecommend(t *testing.T) {
	cases := []struct {
		h    Hardware
		want string
		ok   bool
	}{
		{Hardware{GPUs: []GPU{{VRAMMiB: 12 * 1024}}}, "qwen3:14b", true},
		{Hardware{GPUs: []GPU{{VRAMMiB: 8 * 1024}}}, "qwen3:8b", true},
		{Hardware{GPUs: []GPU{{VRAMMiB: 6 * 1024}}}, "qwen3:4b", true},
		{Hardware{RAMMiB: 32 * 1024}, "qwen3:8b", true},
		{Hardware{RAMMiB: 8 * 1024}, "", false},
	}
	for _, c := range cases {
		got, ok := Recommend(c.h)
		if ok != c.ok || got.Name != c.want {
			t.Errorf("%+v: got %s ok=%v, want %s ok=%v", c.h, got.Name, ok, c.want, c.ok)
		}
	}
}

func TestCPUFallbackIsFlagged(t *testing.T) {
	got, ok := Recommend(Hardware{RAMMiB: 16 * 1024})
	if !ok || !got.Fallback {
		t.Errorf("CPU recommendation not flagged: %+v", got)
	}
}

func TestSummary(t *testing.T) {
	h := Hardware{GPUs: []GPU{{Name: "RTX 3060", VRAMMiB: 12 * 1024}}, RAMMiB: 32 * 1024}
	if got := h.Summary(); got != "RTX 3060 (12 GB), 32 GB RAM" {
		t.Errorf("got %q", got)
	}
	if got := (Hardware{RAMMiB: 16 * 1024}).Summary(); got != "no GPU detected, 16 GB RAM" {
		t.Errorf("got %q", got)
	}
}
