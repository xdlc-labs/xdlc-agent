package subagent

import (
	"strings"
	"testing"
)

func BenchmarkFixPrompt(b *testing.B) {
	ev := map[string]any{
		"run_url": "https://github.com/org/svc/actions/runs/1",
		"log":     strings.Repeat("FAIL line\n", 200),
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = FixPrompt("svc-a", "build failed", ev, "direct", "", "", "")
	}
}

func BenchmarkFrameEvidence(b *testing.B) {
	ev := map[string]any{
		"log": strings.Repeat("x", 8<<10),
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = frameEvidence(ev)
	}
}
