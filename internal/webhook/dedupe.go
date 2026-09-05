package webhook

import (
	"sync"
	"time"
)

// Default bounds for the delivery-id replay guard. 4096 ids is minutes
// of history at GitHub's delivery rate for a large fleet; the TTL is
// what actually decides how long a replay is refused.
const (
	defaultDedupeSize = 4096
	defaultDedupeTTL  = 30 * time.Minute
)

// dedupe remembers webhook delivery ids so a replayed delivery — the
// same signed body POSTed twice, which passes HMAC verification just
// fine — cannot dispatch the same action twice.
//
// Bounded (defaultDedupeSize entries) and time-limited
// (defaultDedupeTTL): a flood of unique ids evicts the oldest rather
// than growing without limit, so this can't be turned into a memory DoS.
//
// Single-replica caveat: this state is in-memory and per-process. Two
// daemon replicas behind one webhook URL each keep their own set, so a
// delivery seen by both is de-duplicated per replica, not globally.
// That is acceptable today because the daemon is single-replica by
// construction (bbolt takes an exclusive flock on the audit DB and the
// chart's PVC is ReadWriteOnce); making it correct for N replicas means
// moving the set into shared storage.
type dedupe struct {
	ttl time.Duration
	max int
	now func() time.Time // swappable in tests

	mu   sync.Mutex
	seen map[string]time.Time
	// order is the ids in seen in insertion order, oldest first — what
	// makes eviction bounded without a heap.
	order []string
}

// newDedupe returns a dedupe with the default bounds.
func newDedupe() *dedupe {
	return &dedupe{ttl: defaultDedupeTTL, max: defaultDedupeSize}
}

// seenBefore reports whether id was recorded within the TTL, recording
// it when it was not. An empty id is never "seen": a delivery with no id
// can't be de-duplicated, so it is let through rather than dropped.
func (d *dedupe) seenBefore(id string) bool {
	if id == "" {
		return false
	}
	clock := d.now
	if clock == nil {
		clock = time.Now
	}
	now := clock()

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = make(map[string]time.Time, d.max)
	}

	if at, ok := d.seen[id]; ok {
		if now.Sub(at) < d.ttl {
			return true
		}
		// Expired. Refresh in place — this id already has a slot in
		// order, so appending again would leave a stale duplicate.
		d.seen[id] = now
		return false
	}

	// Evict from the front (oldest first): everything expired, plus one
	// more when the set is already at max.
	for len(d.order) > 0 {
		front := d.order[0]
		at, ok := d.seen[front]
		if ok && now.Sub(at) < d.ttl && len(d.seen) < d.max {
			break
		}
		delete(d.seen, front)
		d.order = d.order[1:]
	}

	d.seen[id] = now
	d.order = append(d.order, id)
	return false
}
