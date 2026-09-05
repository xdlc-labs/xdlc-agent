// Package store persists a structured Signal+Action history for the
// `xdlc-agent history` command and, later, a dashboard. BACKLOG.md stays
// the human-facing view; this is the queryable one.
package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/otel"

	"go.opentelemetry.io/otel/metric"

	bolt "go.etcd.io/bbolt"
)

const (
	bucket     = "history"
	bucketRepo = "by_repo"
)

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
	// Seq is the bbolt sequence id (set on Append / Since reads). Used as SSE id.
	Seq uint64 `json:"seq,omitempty"`
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

	hubMu   sync.Mutex
	subs    map[chan Record]struct{}
	recent  []Record // ring for SSE Last-Event-ID replay
	recentN int
}

const recentCap = 256

// Open opens (creating if needed) the audit store at path for reading
// and writing — takes bbolt's exclusive file lock, so only one process
// (the daemon) should hold this open at a time; see OpenReadOnly for
// concurrent readers.
func Open(path string) (*AuditStore, error) {
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	s := &AuditStore{db: db, subs: map[chan Record]struct{}{}, recentN: recentCap}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucket)); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketRepo)); err != nil {
			return err
		}
		return rebuildRepoIndex(tx)
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: init: %w", err)
	}
	return s, nil
}

// rebuildRepoIndex fills by_repo from history when empty (upgrade path).
func rebuildRepoIndex(tx *bolt.Tx) error {
	rb := tx.Bucket([]byte(bucketRepo))
	if rb.Stats().KeyN > 0 {
		return nil
	}
	hb := tx.Bucket([]byte(bucket))
	return hb.ForEach(func(k, v []byte) error {
		var r Record
		if err := json.Unmarshal(v, &r); err != nil {
			return err
		}
		if r.Repo == "" || len(k) != 8 {
			return nil
		}
		return rb.Put(repoKey(r.Repo, binary.BigEndian.Uint64(k)), v)
	})
}

func repoKey(repo string, seq uint64) []byte {
	k := make([]byte, len(repo)+1+8)
	copy(k, repo)
	k[len(repo)] = 0
	binary.BigEndian.PutUint64(k[len(repo)+1:], seq)
	return k
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
	return &AuditStore{db: db, subs: map[chan Record]struct{}{}, recentN: recentCap}, nil
}

// Close releases the store's file lock.
func (s *AuditStore) Close() error { return s.db.Close() }

// Append writes one Record. Keys are bbolt NextSequence values (8-byte
// big-endian) so concurrent per-repo workers cannot collide the way
// RFC3339Nano timestamps can on a coarse clock. Also indexes by repo
// (issue #16) and fans out to SSE subscribers (issue #6).
func (s *AuditStore) Append(r Record) error {
	var seq uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		seq = id
		r.Seq = id
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, id)
		val, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if err := b.Put(key, val); err != nil {
			return err
		}
		rb := tx.Bucket([]byte(bucketRepo))
		if rb == nil {
			var cerr error
			rb, cerr = tx.CreateBucketIfNotExists([]byte(bucketRepo))
			if cerr != nil {
				return cerr
			}
		}
		return rb.Put(repoKey(r.Repo, id), val)
	})
	s.countError("append", err)
	if err == nil {
		r.Seq = seq
		s.publish(r)
	}
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
			if len(k) == 8 {
				r.Seq = binary.BigEndian.Uint64(k)
			}
			out = append(out, r)
			return nil
		})
	})
	s.countError("all", err)
	return out, err
}

// Since returns records for repo with At >= since, chronological by seq.
// Uses the by_repo secondary index (issue #16) — sub-linear in other repos.
func (s *AuditStore) Since(repo string, since time.Time) ([]Record, error) {
	var out []Record
	var usedIndex bool
	prefix := append([]byte(repo), 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		rb := tx.Bucket([]byte(bucketRepo))
		if rb == nil {
			return nil
		}
		usedIndex = true
		c := rb.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var r Record
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			if r.At.Before(since) {
				continue
			}
			if len(k) >= 8 {
				r.Seq = binary.BigEndian.Uint64(k[len(k)-8:])
			}
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		s.countError("since", err)
		return nil, err
	}
	if !usedIndex {
		all, aerr := s.All()
		if aerr != nil {
			return nil, aerr
		}
		for _, r := range all {
			if r.Repo == repo && !r.At.Before(since) {
				out = append(out, r)
			}
		}
	}
	return out, nil
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
func (s *AuditStore) ActionsSince(repo string, since time.Time) ([]string, error) {
	recs, err := s.Since(repo, since)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range recs {
		if !r.Succeeded() {
			continue
		}
		out = append(out, r.Action)
	}
	return out, nil
}

// Subscribe returns a channel of new Records and an unsubscribe func (issue #6).
func (s *AuditStore) Subscribe() (<-chan Record, func()) {
	ch := make(chan Record, 16)
	s.hubMu.Lock()
	if s.subs == nil {
		s.subs = map[chan Record]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.hubMu.Unlock()
	unsub := func() {
		s.hubMu.Lock()
		delete(s.subs, ch)
		s.hubMu.Unlock()
		close(ch)
	}
	return ch, unsub
}

// ReplaySinceSeq returns buffered recent records with Seq > after (SSE reconnect).
func (s *AuditStore) ReplaySinceSeq(after uint64) []Record {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	var out []Record
	for _, r := range s.recent {
		if r.Seq > after {
			out = append(out, r)
		}
	}
	return out
}

func (s *AuditStore) publish(r Record) {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	s.recent = append(s.recent, r)
	if len(s.recent) > s.recentN && s.recentN > 0 {
		s.recent = s.recent[len(s.recent)-s.recentN:]
	}
	for ch := range s.subs {
		select {
		case ch <- r:
		default:
			// slow subscriber — drop
		}
	}
}
