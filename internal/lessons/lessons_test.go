package lessons

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordAndForRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LESSONS.md")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Record("svc-a", "ci", "ok", "test failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("svc-b", "ci", "error", "other"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("svc-a", "ci", "error", "same fail again"); err != nil {
		t.Fatal(err)
	}
	got := s.ForRepo("svc-a", 5)
	if !strings.Contains(got, "svc-a") || !strings.Contains(got, "same fail again") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "svc-b") {
		t.Fatal("should not include other repo")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "outcome=ok") {
		t.Fatalf("file missing record:\n%s", raw)
	}
}
