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
