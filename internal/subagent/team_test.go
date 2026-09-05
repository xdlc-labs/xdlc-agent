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

	got := ReadTeamInstructions(dir, "")
	if !strings.Contains(got, "use go fmt") || !strings.Contains(got, "prefer table tests") {
		t.Fatalf("missing content: %q", got)
	}

	p := FixPrompt("svc", "fail", map[string]any{"x": 1}, "", "", got, "")
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
	if got := ReadTeamInstructions(t.TempDir(), ""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestReadTeamInstructionsTruncates(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", maxRuleFileBytes+500)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadTeamInstructions(dir, "")
	if !strings.Contains(got, "...[truncated]...") {
		t.Fatalf("expected truncation marker: len=%d", len(got))
	}
	if len(got) > maxRuleFileBytes+64 {
		t.Fatalf("still too large: %d", len(got))
	}
}

func TestReadTeamInstructionsAllSources(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "agents rule")
	write(t, filepath.Join(dir, "CLAUDE.md"), "claude rule")
	if err := os.MkdirAll(filepath.Join(dir, ".xdlc"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, ".xdlc", "rules.md"), "xdlc rule")

	globalDir := t.TempDir()
	global := filepath.Join(globalDir, "rules.md")
	write(t, global, "global rule")

	got := ReadTeamInstructions(dir, global)
	for _, want := range []string{"agents rule", "claude rule", "xdlc rule", "global rule"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Repo rules must precede the daemon-wide file, so a repo can
	// override a fleet-wide convention by contradicting it later.
	if strings.Index(got, "agents rule") > strings.Index(got, "global rule") {
		t.Error("global rules must come after repo rules")
	}
}

func TestReadTeamInstructionsDedupes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "same body")
	write(t, filepath.Join(dir, "CLAUDE.md"), "same body")
	got := ReadTeamInstructions(dir, "")
	if n := strings.Count(got, "same body"); n != 1 {
		t.Fatalf("duplicate body injected %d times:\n%s", n, got)
	}
}

func TestRuleSources(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "AGENTS.md"), "a")
	write(t, filepath.Join(dir, "CLAUDE.md"), strings.Repeat("b", maxRuleFileBytes+10))

	got := RuleSources(dir, "")
	if len(got) != 2 {
		t.Fatalf("want 2 sources, got %d: %+v", len(got), got)
	}
	if got[0].Path != "AGENTS.md" || got[1].Path != "CLAUDE.md" {
		t.Fatalf("unexpected order: %+v", got)
	}
	if !got[1].Truncated || got[1].Bytes != maxRuleFileBytes {
		t.Fatalf("want per-file truncation, got %+v", got[1])
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
