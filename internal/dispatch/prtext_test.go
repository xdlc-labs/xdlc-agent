package dispatch

import (
	"strings"
	"testing"

	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
)

func TestPRTitle(t *testing.T) {
	// The summary the first real Fix PR (#51) carried. Its title shipped as
	// "…(worktree name in me...(truncated)"; the clause break is the cut.
	pr51 := "Made committingRunner's concurrent commits distinct (worktree name in message, identity via env) so the losing push is a real non-fast-forward rejection instead of a no-op on an identical SHA."

	cases := map[string]struct{ in, want string }{
		"short passes through": {"Add subtracted; now adds", "Add subtracted; now adds"},
		"clause break wins":    {pr51, "Made committingRunner's concurrent commits distinct"},
		"semicolon break": {
			"Restored the RFC3339 zone the parser dropped; TestParse expected it and every fixture carries one",
			"Restored the RFC3339 zone the parser dropped",
		},
		"no break falls back to word boundary": {
			strings.Repeat("word ", 30),
			strings.TrimSpace(strings.Repeat("word ", 13)) + "…",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := prTitle(c.in)
			if got != c.want {
				t.Fatalf("prTitle(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if len("fix: "+got) > prTitleMax+6 {
				t.Fatalf("title too long: %d", len(got))
			}
			if strings.Contains(got, "(truncated)") {
				t.Fatal("title carries the log truncation marker")
			}
		})
	}
}

func TestPRTextUsesSummaryAndLinksRun(t *testing.T) {
	s := orchestrator.Signal{
		Repo: "svc",
		Evidence: map[string]any{
			"agent_summary":  "Add subtracted; now adds",
			"agent_provider": "claude",
			"run_url":        "https://github.com/o/svc/actions/runs/9",
		},
	}
	title, body := prText(s)
	if title != "fix: Add subtracted; now adds" {
		t.Fatalf("title = %q", title)
	}
	for _, want := range []string{"https://github.com/o/svc/actions/runs/9", "**claude:**", "Opened by [xdlc]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	title, _ = prText(orchestrator.Signal{Repo: "svc", Evidence: map[string]any{}})
	if title != "xdlc fix: svc" {
		t.Fatalf("no-summary title = %q", title)
	}
}
