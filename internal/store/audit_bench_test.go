package store

import (
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkAppend(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })

	rec := Record{
		At: time.Now().UTC(), Repo: "svc", Source: "ci", Kind: "fail",
		Action: "fix", Status: StatusOK, Evidence: map[string]any{"n": 1},
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := s.Append(rec); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAuditSince(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 2000; i++ {
		repo := "noise"
		if i%100 == 0 {
			repo = "target"
		}
		if err := s.Append(Record{
			At: now, Repo: repo, Source: "ci", Kind: "fail",
			Action: "fix", Status: StatusOK, Evidence: map[string]any{"i": i},
		}); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}
	ro, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ro.Close() })

	b.ReportAllocs()
	for b.Loop() {
		got, err := ro.Since("target", now.Add(-time.Hour))
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("expected target hits")
		}
	}
}

// BenchmarkAuditSinceWindow is the flap-detection shape: one repo with a
// long history and a 2h window over the tail of it. Since walks backwards
// and stops at the first record outside the window, so the cost tracks
// the window, not the history.
func BenchmarkAuditSinceWindow(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	const total = 5000
	for i := 0; i < total; i++ {
		// Oldest first, one record a minute, the newest at now.
		at := now.Add(-time.Duration(total-i) * time.Minute)
		if err := s.Append(Record{
			At: at, Repo: "target", Source: "ci", Kind: "fail",
			Action: "fix", Status: StatusOK, Evidence: map[string]any{"i": i},
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.Cleanup(func() { _ = s.Close() })

	b.ReportAllocs()
	for b.Loop() {
		got, err := s.Since("target", now.Add(-2*time.Hour))
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 120 {
			b.Fatalf("got %d records, want 120", len(got))
		}
	}
}

// BenchmarkAuditRecent is the /api/history shape: newest 100 of a long log.
func BenchmarkAuditRecent(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 5000; i++ {
		if err := s.Append(Record{
			At: now, Repo: "r", Source: "ci", Kind: "fail",
			Action: "fix", Status: StatusOK, Evidence: map[string]any{"i": i},
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.Cleanup(func() { _ = s.Close() })

	b.ReportAllocs()
	for b.Loop() {
		got, err := s.Recent("", 100)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != 100 {
			b.Fatalf("got %d", len(got))
		}
	}
}
