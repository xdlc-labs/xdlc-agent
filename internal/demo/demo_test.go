package demo

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

func TestDemoFakeAll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dir := t.TempDir()
	if err := Run(ctx, Options{
		Provider: "fake",
		Scenario: "all",
		WorkDir:  dir,
		Out:      io.Discard,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	audit, err := store.OpenReadOnly(filepath.Join(dir, "xdlc-agent-history.db"))
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = audit.Close() }()

	records, err := audit.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	got := map[string]bool{}
	for _, r := range records {
		got[r.Action] = true
	}
	for _, want := range []string{"fix", "promote", "revert"} {
		if !got[want] {
			t.Errorf("audit missing action %q; got %+v", want, got)
		}
	}
}
