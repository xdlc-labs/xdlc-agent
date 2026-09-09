package fixstate

import (
	"sync"
	"testing"
	"time"
)

func TestSetTracksAndPublishes(t *testing.T) {
	tr := New()
	updates, unsub := tr.Subscribe()
	defer unsub()

	tr.Set(Fix{ID: "f1", Repo: "svc", Source: "ci", Provider: "claude", State: Queued})
	got := <-updates
	if got.ID != "f1" || got.State != Queued || got.Repo != "svc" {
		t.Fatalf("unexpected update: %+v", got)
	}
	if got.Since.IsZero() {
		t.Fatal("a transition with no timestamp is unreadable in a console")
	}
	if active := tr.Active(); len(active) != 1 || active[0].State != Queued {
		t.Fatalf("active = %+v", active)
	}
}

// A transition should not have to restate the whole row: dispatch knows
// the new state, and repeating repo/source/provider at seven call sites
// is how they drift apart.
func TestSetCarriesForwardUnsetFields(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", Source: "ci", Provider: "claude", State: Queued})
	tr.Set(Fix{ID: "f1", State: Fixing, SessionID: "20260909T000000Z-svc", Attempt: 1})

	active := tr.Active()
	if len(active) != 1 {
		t.Fatalf("want one active Fix, got %+v", active)
	}
	f := active[0]
	if f.Repo != "svc" || f.Source != "ci" || f.Provider != "claude" {
		t.Fatalf("identity lost on transition: %+v", f)
	}
	if f.State != Fixing || f.SessionID != "20260909T000000Z-svc" || f.Attempt != 1 {
		t.Fatalf("new fields not applied: %+v", f)
	}
}

// "Cloning for four minutes" is the reading that matters, so a repeated
// state must not restart its own clock.
func TestSetKeepsSinceForARepeatedState(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	first := tr.Active()[0].Since

	time.Sleep(2 * time.Millisecond)
	tr.Set(Fix{ID: "f1", State: Fixing, Attempt: 2})
	if got := tr.Active()[0].Since; !got.Equal(first) {
		t.Fatalf("Since restarted on an unchanged state: %s -> %s", first, got)
	}

	time.Sleep(2 * time.Millisecond)
	tr.Set(Fix{ID: "f1", State: Pushing})
	if got := tr.Active()[0].Since; got.Equal(first) {
		t.Fatal("Since did not advance on a real transition")
	}
}

// A finished Fix leaves the live list, but the transition still has to
// reach subscribers — otherwise a console row hangs on "verifying"
// forever.
func TestTerminalStatesLeaveActiveButArePublished(t *testing.T) {
	for _, state := range []State{OK, Error} {
		tr := New()
		updates, unsub := tr.Subscribe()
		tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
		<-updates
		tr.Set(Fix{ID: "f1", State: state})

		got := <-updates
		if got.State != state {
			t.Fatalf("terminal transition not published: %+v", got)
		}
		if active := tr.Active(); len(active) != 0 {
			t.Fatalf("%s left the Fix in the live list: %+v", state, active)
		}
		unsub()
	}
}

func TestActiveIsOrderedByStateAge(t *testing.T) {
	tr := New()
	base := time.Now().UTC()
	tr.Set(Fix{ID: "newer", Repo: "b", State: Fixing, Since: base.Add(time.Minute)})
	tr.Set(Fix{ID: "older", Repo: "a", State: Cloning, Since: base})

	active := tr.Active()
	if len(active) != 2 || active[0].ID != "older" || active[1].ID != "newer" {
		t.Fatalf("want oldest first, got %+v", active)
	}
}

// A console that stops reading must not be able to wedge a Fix, so a
// full subscriber channel drops updates instead of blocking Set.
func TestSetDoesNotBlockOnASlowSubscriber(t *testing.T) {
	tr := New()
	_, unsub := tr.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := range subBuffer * 3 {
			tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing, Since: time.Now().Add(time.Duration(i))})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Set blocked on a subscriber that never read")
	}
}

func TestUnsubStopsUpdatesAndIsIdempotent(t *testing.T) {
	tr := New()
	updates, unsub := tr.Subscribe()
	unsub()
	unsub() // a double stop must not panic on a closed channel

	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	if _, open := <-updates; open {
		t.Fatal("want a closed channel after unsub")
	}
}

// A nil Tracker is the "console not wired up" case, and dispatch calls
// these on every transition.
func TestNilTrackerIsANoOp(t *testing.T) {
	var tr *Tracker
	tr.Set(Fix{ID: "f1", State: Fixing})
	if got := tr.Active(); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
	updates, unsub := tr.Subscribe()
	unsub()
	if _, open := <-updates; open {
		t.Fatal("a nil tracker must hand back a closed channel")
	}
}

func TestSetIgnoresAnEntryWithNoID(t *testing.T) {
	tr := New()
	tr.Set(Fix{Repo: "svc", State: Fixing})
	if got := tr.Active(); len(got) != 0 {
		t.Fatalf("an unkeyed Fix cannot be a console row: %+v", got)
	}
}

func TestConcurrentSetAndActive(t *testing.T) {
	tr := New()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			tr.Set(Fix{ID: string(rune('a' + n)), Repo: "svc", State: Fixing})
		}(i)
		go func() {
			defer wg.Done()
			_ = tr.Active()
		}()
	}
	wg.Wait()
	if got := tr.Active(); len(got) != 8 {
		t.Fatalf("want 8 live Fixes, got %d", len(got))
	}
}
