// Package store persists a structured Signal+Action history for the
// `xdlc-agent history` command and, later, a dashboard. BACKLOG.md stays
// the human-facing view; this is the queryable one.
package store

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/otel"

	"go.opentelemetry.io/otel/metric"

	bolt "go.etcd.io/bbolt"
)

const bucket = "history"

// Status values for Record.Status.
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// Record is one persisted signal+action — repo, where the Signal came
// from, its Kind, the Action the orchestrator dispatched, and whether
// that dispatch succeeded.
type Record struct {
	At            time.Time      `json:"at"`
	Repo          string         `json:"repo"`
	Source        string         `json:"source"`
	Kind          string         `json:"kind"`
	Action        string         `json:"action"`
	Status        string         `json:"status"` // StatusOK | StatusError (empty = legacy ok)
	Error         string         `json:"error,omitempty"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
	AgentProvider string         `json:"agent_provider,omitempty"`
	ChainID       string         `json:"chain_id,omitempty"`
	Evidence      map[string]any `json:"evidence"`
}

// Succeeded reports whether the dispatch completed without error.
// Empty Status (pre-C1 records) counts as success for backward compat.
func (r Record) Succeeded() bool {
	return r.Status == "" || r.Status == StatusOK
}

// AuditStore is a bbolt-backed append log of Records, read by
// `xdlc-agent history`.
type AuditStore struct {
	db *bolt.DB
	// Metrics, if set, records a StoreErrors increment on every failed
	// Append/All — this is the PVC/bbolt-errors signal
	// observability/prometheus/rules/prod-health.yaml alerts on. Optional;
	// callers set it after Open (see cmd/xdlc-agent/main.go).
	Metrics *otel.Metrics
}

// Open opens (creating if needed) the audit store at path for reading
// and writing — takes bbolt's exclusive file lock, so only one process
// (the daemon) should hold this open at a time; see OpenReadOnly for
// concurrent readers.
func Open(path string) (*AuditStore, error) {
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucket))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("store: init bucket: %w", err)
	}
	return &AuditStore{db: db}, nil
}

// OpenReadOnly opens the store for reads only, using bbolt's shared
// (not exclusive) file lock — safe to call while a `daemon` process
// holds the store open for writing, which Open's exclusive lock is not.
// Used by `xdlc-agent history` so it doesn't block on, or steal the
// lock from, a running daemon.
func OpenReadOnly(path string) (*AuditStore, error) {
	db, err := bolt.Open(path, 0o644, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("store: open %s read-only: %w", path, err)
	}
	return &AuditStore{db: db}, nil
}

// Close releases the store's file lock.
func (s *AuditStore) Close() error { return s.db.Close() }

// Append writes one Record. Keys are bbolt NextSequence values (8-byte
// big-endian) so concurrent per-repo workers cannot collide the way
// RFC3339Nano timestamps can on a coarse clock.
func (s *AuditStore) Append(r Record) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, id)
		val, err := json.Marshal(r)
		if err != nil {
			return err
		}
		return b.Put(key, val)
	})
	s.countError("append", err)
	return err
}

// All returns every Record in the store, in bbolt key (sequence) order.
func (s *AuditStore) All() ([]Record, error) {
	var out []Record
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		return b.ForEach(func(k, v []byte) error {
			var r Record
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	})
	s.countError("all", err)
	return out, err
}

// countError increments StoreErrors (labeled by op) when err is non-nil
// and Metrics is configured. No-op otherwise.
func (s *AuditStore) countError(op string, err error) {
	if err == nil || s.Metrics == nil {
		return
	}
	s.Metrics.StoreErrors.Add(context.Background(), 1, metric.WithAttributes(otel.AttrOp(op)))
}

// ActionsSince returns chronological successful action strings for repo
// with At >= since. Failed dispatches are skipped so flap detection
// does not treat a crashed Fix as a completed cycle.
// ponytail: scans All(); add a secondary index if audit volume hurts flap checks.
func (s *AuditStore) ActionsSince(repo string, since time.Time) ([]string, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range all {
		if r.Repo != repo || r.At.Before(since) || !r.Succeeded() {
			continue
		}
		out = append(out, r.Action)
	}
	return out, nil
}
