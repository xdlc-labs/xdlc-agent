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

func BenchmarkAll(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if err := s.Append(Record{
			At: time.Now().UTC(), Repo: "svc", Source: "ci", Kind: "fail",
			Action: "fix", Status: StatusOK, Evidence: map[string]any{"i": i},
		}); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		b.Fatal(err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ro.Close() })

	b.ReportAllocs()
	for b.Loop() {
		if _, err := ro.All(); err != nil {
			b.Fatal(err)
		}
	}
}
