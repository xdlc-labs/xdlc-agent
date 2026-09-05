package backlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "BACKLOG.md")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# BACKLOG") || !strings.Contains(string(raw), "## Log") {
		t.Fatalf("init header missing: %q", raw)
	}

	if err := s.Record("svc", "fix", map[string]any{"z": 1, "a": "x"}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(raw)
	if !strings.Contains(line, "repo=svc action=fix") {
		t.Fatalf("missing repo/action: %s", line)
	}
	// formatEvidence sorts keys — a before z
	if !strings.Contains(line, "a=x z=1") {
		t.Fatalf("evidence not sorted: %s", line)
	}

	// reopen existing file must not rewrite header
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Record("svc", "promote", nil); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "# BACKLOG") != 1 {
		t.Fatalf("header rewritten on reopen: %s", raw)
	}
	if !strings.Contains(string(raw), "action=promote") {
		t.Fatalf("second record missing: %s", raw)
	}
}
