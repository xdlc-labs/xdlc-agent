package store

import (
	"fmt"
	"path/filepath"
	"strings"
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

	// Reopen read-only, as `xdlc history` does, and confirm both
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
		go func() {
			errCh <- s.Append(Record{
				At: time.Now().UTC(), Repo: "svc", Action: "fix", Status: StatusOK,
			})
		}()
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

func TestSinceFiltersByRepo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		_ = s.Append(Record{At: now, Repo: "noise", Action: "fix", Status: StatusOK})
	}
	_ = s.Append(Record{At: now, Repo: "target", Action: "promote", Status: StatusOK})
	got, err := s.Since("target", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Repo != "target" || got[0].Action != "promote" {
		t.Fatalf("got %+v", got)
	}
}

func TestSubscribeReceivesAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ch, unsub := s.Subscribe()
	defer unsub()
	go func() {
		_ = s.Append(Record{At: time.Now().UTC(), Repo: "svc", Action: "fix", Status: StatusOK})
	}()
	select {
	case r := <-ch:
		if r.Repo != "svc" || r.Seq == 0 {
			t.Fatalf("%+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SSE fan-out")
	}
}

func TestRecentNewestFirst(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		repo := "a"
		if i%2 == 1 {
			repo = "b"
		}
		if err := s.Append(Record{At: base.Add(time.Duration(i) * time.Minute), Repo: repo, Action: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Recent("", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(all); got != "9,8,7" {
		t.Fatalf("Recent(\"\", 3) = %s", got)
	}
	if all[0].Seq != 10 {
		t.Fatalf("Seq not set from key: %d", all[0].Seq)
	}
	onlyA, err := s.Recent("a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(onlyA); got != "8,6" {
		t.Fatalf("Recent(a, 2) = %s", got)
	}
	// More than exist: returns what there is, no error.
	onlyB, err := s.Recent("b", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(onlyB); got != "9,7,5,3,1" {
		t.Fatalf("Recent(b, 100) = %s", got)
	}
	if none, _ := s.Recent("zzz", 5); len(none) != 0 {
		t.Fatalf("unknown repo returned %d records", len(none))
	}
}

func TestSinceStopsAtWindow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		if err := s.Append(Record{At: base.Add(time.Duration(i) * time.Hour), Repo: "a", Action: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Since("a", base.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if actions(got) != "3,4,5" {
		t.Fatalf("Since = %s, want chronological 3,4,5", actions(got))
	}
	if got[0].Seq != 4 {
		t.Fatalf("Seq = %d, want 4", got[0].Seq)
	}
}

func TestAllCacheInvalidatedByAppend(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Append(Record{Repo: "a", Action: "1"}); err != nil {
		t.Fatal(err)
	}
	first, _ := s.All()
	// Mutating the returned slice must not affect later reads.
	first[0].Action = "mutated"
	again, _ := s.All()
	if again[0].Action != "1" {
		t.Fatalf("cache leaked caller mutation: %q", again[0].Action)
	}
	if err := s.Append(Record{Repo: "a", Action: "2"}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.All()
	if actions(after) != "1,2" {
		t.Fatalf("stale cache after Append: %s", actions(after))
	}
}

func actions(recs []Record) string {
	parts := make([]string, 0, len(recs))
	for _, r := range recs {
		parts = append(parts, r.Action)
	}
	return strings.Join(parts, ",")
}
