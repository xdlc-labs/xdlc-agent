package subagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTeamInstructions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("use go fmt"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(dir, ".xdlc", "skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "ci.md"), []byte("prefer table tests"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ReadTeamInstructions(dir)
	if !strings.Contains(got, "use go fmt") || !strings.Contains(got, "prefer table tests") {
		t.Fatalf("missing content: %q", got)
	}

	p := FixPrompt("svc", "fail", map[string]any{"x": 1}, "", "", got)
	if !strings.Contains(p, teamRulesBegin) || !strings.Contains(p, "use go fmt") {
		t.Fatalf("team rules missing from prompt:\n%s", p)
	}
	// Team rules must appear before untrusted evidence.
	if strings.Index(p, teamRulesBegin) > strings.Index(p, evidenceBegin) {
		t.Fatal("trusted team rules must precede untrusted evidence")
	}
	inner := p[strings.Index(p, evidenceBegin):strings.Index(p, evidenceEnd)]
	if strings.Contains(inner, "use go fmt") {
		t.Fatal("team rules leaked into untrusted evidence block")
	}
}

func TestReadTeamInstructionsMissing(t *testing.T) {
	if got := ReadTeamInstructions(t.TempDir()); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestReadTeamInstructionsTruncates(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", maxTeamInstructionsBytes+500)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadTeamInstructions(dir)
	if !strings.Contains(got, "...[truncated]...") {
		t.Fatalf("expected truncation marker: len=%d", len(got))
	}
	if len(got) > maxTeamInstructionsBytes+64 {
		t.Fatalf("still too large: %d", len(got))
	}
}
