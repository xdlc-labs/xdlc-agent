// Package fixstate tracks what each in-flight Fix is doing right now.
//
// The audit store answers "what happened" once a Fix is over, and
// `fix_queue_depth` answers "how many are there" as a pair of integers.
// Neither can tell a Fix that is three minutes into a clone apart from
// one that is three minutes into an agent run, so an operator watching
// the console had no way to see progress — only a count that stayed the
// same until the row appeared in history.
//
// A Tracker holds one entry per running Fix and publishes every
// transition. It is deliberately in-memory and daemon-local: this is a
// live view, not a record. The record is the audit row and the session
// on disk, both of which outlive the process.
package fixstate

import (
	"bytes"
	"sort"
	"sync"
	"time"
)

// State is where a Fix has got to. The order below is the order a
// healthy Fix passes through, though a run skips what it does not do:
// no Planning without agent.fix_plan, no Pushing outside worktree mode,
// no Verifying without agent.fix_reverify.
type State string

// The states a Fix reports.
const (
	// Queued: waiting for a Fix slot (agent.max_concurrent_fixes).
	Queued State = "queued"
	// Cloning: fetching the repo and creating the per-Fix worktree.
	Cloning State = "cloning"
	// Planning: the optional diagnose pass (agent.fix_plan).
	Planning State = "planning"
	// Fixing: the coding agent is running.
	Fixing State = "fixing"
	// Pushing: xdlc is pushing the agent's commits (worktree mode).
	Pushing State = "pushing"
	// Verifying: the gate re-check after a Fix (agent.fix_reverify).
	Verifying State = "verifying"
	// OK and Error are terminal: the entry leaves Active, and the
	// transition is still published so a console row can clear itself.
	OK    State = "ok"
	Error State = "error"
)

// Terminal reports whether s ends a Fix.
func (s State) Terminal() bool { return s == OK || s == Error }

// Fix is one in-flight Fix as the console sees it.
type Fix struct {
	// ID is stable for the whole run, including the queued phase that
	// happens before a session directory exists. Without it a console
	// row would appear, vanish and reappear under a new key the moment
	// recording started.
	ID string `json:"id"`
	// SessionID is the recording this Fix is writing, once it has one.
	// Empty while queued, and empty for the whole run when session
	// recording is off.
	SessionID string `json:"session_id,omitempty"`
	Repo      string `json:"repo"`
	Source    string `json:"source"`
	Provider  string `json:"provider,omitempty"`
	State     State  `json:"state"`
	// Since is when this Fix entered State — not when the Fix started,
	// because "cloning for 4 minutes" is the reading that matters.
	Since time.Time `json:"since"`
	// Attempt is the 1-based agent run within this Fix, so a retry
	// ladder is visible as it climbs rather than only in the audit row.
	Attempt int `json:"attempt,omitempty"`
}

// subBuffer is how many transitions a slow subscriber may fall behind
// before its updates are dropped. A Fix must never block on a console
// that stopped reading, and the /api/fixes/active snapshot is always
// there to resynchronize from.
const subBuffer = 64

// Tracker holds the live Fixes and fans transitions out to subscribers.
// A nil *Tracker is valid and every method is a no-op, so callers never
// branch on whether the console is wired up.
type Tracker struct {
	mu     sync.Mutex
	active map[string]Fix
	subs   map[int]chan Fix
	nextID int
	// tails holds the last tailBytes of each running Fix's agent output;
	// outSubs are the "fix_output" listeners. See output.go.
	tails   map[string]*bytes.Buffer
	outSubs map[int]*outputSub
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{active: map[string]Fix{}, subs: map[int]chan Fix{}}
}

// Set records f's state and publishes the transition.
//
// Since is stamped here unless the caller set it: the tracker is the
// only place that knows whether this is a new state or a repeat, and a
// repeated state must not restart its own clock.
func (t *Tracker) Set(f Fix) {
	if t == nil || f.ID == "" {
		return
	}
	t.mu.Lock()
	prev, existed := t.active[f.ID]
	if existed {
		// Carry forward what a transition does not restate, so a caller
		// can publish a state change without repeating the whole row.
		if f.Repo == "" {
			f.Repo = prev.Repo
		}
		if f.Source == "" {
			f.Source = prev.Source
		}
		if f.Provider == "" {
			f.Provider = prev.Provider
		}
		if f.SessionID == "" {
			f.SessionID = prev.SessionID
		}
		if f.Attempt == 0 {
			f.Attempt = prev.Attempt
		}
	}
	if f.Since.IsZero() {
		if existed && prev.State == f.State {
			f.Since = prev.Since
		} else {
			f.Since = time.Now().UTC()
		}
	}
	if f.State.Terminal() {
		delete(t.active, f.ID)
		// The tail was a live view; the transcript is on disk.
		delete(t.tails, f.ID)
	} else {
		t.active[f.ID] = f
	}
	// Send while still holding the lock. The sends never block, and the
	// lock is what makes them safe: unsub closes a channel under the
	// same lock, so no send can land on a channel that has been closed.
	// Snapshotting the map and sending after Unlock left exactly that
	// gap open, and a console disconnecting mid-transition hit it.
	for _, ch := range t.subs {
		select {
		case ch <- f:
		default:
			// Subscriber is behind; dropping is correct here. The next
			// snapshot read carries the truth, and a Fix is not held up
			// by a console that stopped reading.
		}
	}
	t.mu.Unlock()
}

// Active returns the running Fixes, oldest state change first, so a
// console list does not reorder itself on every transition.
func (t *Tracker) Active() []Fix {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	out := make([]Fix, 0, len(t.active))
	for _, f := range t.active {
		out = append(out, f)
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Since.Equal(out[j].Since) {
			return out[i].ID < out[j].ID
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// Subscribe returns a channel of transitions and a function that stops
// the subscription. The channel is closed by unsub, so a range over it
// ends when the caller lets go.
func (t *Tracker) Subscribe() (<-chan Fix, func()) {
	if t == nil {
		ch := make(chan Fix)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan Fix, subBuffer)
	t.mu.Lock()
	id := t.nextID
	t.nextID++
	t.subs[id] = ch
	t.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			// Close under the lock: Set sends under it, so once this
			// returns no send can race the close.
			t.mu.Lock()
			delete(t.subs, id)
			close(ch)
			t.mu.Unlock()
		})
	}
}
