// Package lessons stores short Fix outcomes for the next run (issue #19 / C1).
// One line per lesson in LESSONS.md; size-capped reads for FixPrompt inject.
package lessons

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const maxLessonBytes = 8 * 1024

// Store appends and reads lessons from a markdown log file.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open returns a Store at path (created empty if missing).
func Open(path string) (*Store, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("# LESSONS\n\n"), 0o600); err != nil {
			return nil, fmt.Errorf("lessons: init %s: %w", path, err)
		}
	}
	return &Store{path: path}, nil
}

// Record appends one lesson line for repo.
func (s *Store) Record(repo, source, outcome, symptom string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("lessons: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	symptom = strings.ReplaceAll(strings.TrimSpace(symptom), "\n", " ")
	if len(symptom) > 200 {
		symptom = symptom[:200] + "…"
	}
	line := fmt.Sprintf("- [%s] repo=%s source=%s outcome=%s symptom=%s\n",
		time.Now().UTC().Format(time.RFC3339), repo, source, outcome, symptom)
	_, err = f.WriteString(line)
	return err
}

// ForRepo returns up to k most recent lessons mentioning repo (newest last).
func (s *Store) ForRepo(repo string, k int) string {
	if s == nil || k <= 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return ""
	}
	var matched []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "repo="+repo+" ") || strings.HasSuffix(line, "repo="+repo) {
			matched = append(matched, strings.TrimPrefix(line, "- "))
		}
	}
	if len(matched) == 0 {
		return ""
	}
	if len(matched) > k {
		matched = matched[len(matched)-k:]
	}
	out := strings.Join(matched, "\n")
	if len(out) > maxLessonBytes {
		out = out[len(out)-maxLessonBytes:]
	}
	return out
}
