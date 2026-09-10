package subagent

import (
	"fmt"
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

// The over-budget path is the one that used to re-marshal the whole map
// per key; many keys of moderate size is where that showed.
func BenchmarkFrameEvidenceOverBudget(b *testing.B) {
	ev := make(map[string]any, 400)
	for i := range 400 {
		ev[fmt.Sprintf("key_%03d", i)] = strings.Repeat("y", 512)
	}
	ev["logs"] = strings.Repeat("FAIL line\n", 100)
	ev["run_url"] = "https://github.com/org/svc/actions/runs/1"
	b.ReportAllocs()
	for b.Loop() {
		_ = frameEvidence(ev)
	}
}
