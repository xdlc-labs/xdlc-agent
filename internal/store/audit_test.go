package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	want := []Record{
		{At: time.Now().UTC(), Repo: "svc-a", Source: "ci", Kind: "fail", Action: "fix", Evidence: map[string]any{"run_url": "http://x"}},
		{At: time.Now().UTC().Add(time.Second), Repo: "svc-b", Source: "dev-gate", Kind: "pass", Action: "promote", Evidence: map[string]any{}},
	}
	for _, r := range want {
		if err := s.Append(r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen read-only, as `xdlc-agent history` does, and confirm both
	// records round-trip correctly.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = ro.Close() }()

	got, err := ro.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(got), got)
	}

	byRepo := map[string]Record{}
	for _, r := range got {
		byRepo[r.Repo] = r
	}
	if byRepo["svc-a"].Action != "fix" || byRepo["svc-a"].Source != "ci" {
		t.Errorf("svc-a record wrong: %+v", byRepo["svc-a"])
	}
	if byRepo["svc-b"].Action != "promote" || byRepo["svc-b"].Kind != "pass" {
		t.Errorf("svc-b record wrong: %+v", byRepo["svc-b"])
	}
}

func TestOpenReadOnlyMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.db")
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("expected error opening a nonexistent db read-only")
	}
}

func TestAppendUniqueKeysUnderConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	const n = 50
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			errCh <- s.Append(Record{
				At: time.Now().UTC(), Repo: "svc", Action: "fix", Status: StatusOK,
			})
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != n {
		t.Fatalf("got %d records, want %d (key collision?)", len(all), n)
	}
}

func TestActionsSinceSkipsFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now().UTC()
	_ = s.Append(Record{At: now, Repo: "a", Action: "fix", Status: StatusError, Error: "boom"})
	_ = s.Append(Record{At: now.Add(time.Second), Repo: "a", Action: "fix", Status: StatusOK})
	got, err := s.ActionsSince("a", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "fix" {
		t.Fatalf("got %v", got)
	}
}
