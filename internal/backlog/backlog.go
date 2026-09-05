// Package backlog manages BACKLOG.md — the human+agent-readable task
// queue. Every orchestrator decision gets appended here so a person can
// audit "what did the agent decide and why" without digging through logs.
package backlog

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// Entry is one recorded decision — repo, the Action the orchestrator
// took, and the evidence the triggering Signal carried.
type Entry struct {
	At       time.Time
	Repo     string
	Action   string
	Evidence map[string]any
}

// Store appends Entries to a markdown file, one Entry per line under a
// running "## Log" section. Concurrency-safe: the orchestrator loop is
// single-threaded but CLI commands (backlog add) may write concurrently.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open returns a Store backed by path, creating it with a starter
// "# BACKLOG" header if it doesn't exist yet.
func Open(path string) (*Store, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte("# BACKLOG\n\n## Log\n"), 0o600); err != nil {
			return nil, fmt.Errorf("backlog: init %s: %w", path, err)
		}
	}
	return &Store{path: path}, nil
}

// Record appends one line — timestamp, repo, action, evidence — to
// BACKLOG.md.
func (s *Store) Record(repo, action string, evidence map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("backlog: open %s: %w", s.path, err)
	}

	line := fmt.Sprintf("- [%s] repo=%s action=%s %s\n",
		time.Now().UTC().Format(time.RFC3339), repo, action, formatEvidence(evidence))

	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

func formatEvidence(evidence map[string]any) string {
	if len(evidence) == 0 {
		return ""
	}
	keys := make([]string, 0, len(evidence))
	for k := range evidence {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s=%v ", k, evidence[k])
	}
	return out
}
