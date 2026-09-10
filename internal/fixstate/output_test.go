package fixstate

import (
	"strings"
	"testing"
	"time"
)

func TestAppendKeepsTailAndPublishes(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	ch, unsub := tr.SubscribeOutput()
	defer unsub()

	tr.Append("f1", "hello\n")
	tr.Append("f1", "world\n")
	if got := tr.Tail("f1"); got != "hello\nworld\n" {
		t.Fatalf("tail=%q", got)
	}
	for _, want := range []string{"hello\n", "world\n"} {
		select {
		case o := <-ch:
			if o.ID != "f1" || o.Repo != "svc" || o.Text != want || o.Snapshot {
				t.Fatalf("chunk=%+v", o)
			}
		case <-time.After(time.Second):
			t.Fatal("no chunk")
		}
	}
}

func TestAppendIgnoresUnknownFix(t *testing.T) {
	tr := New()
	tr.Append("ghost", "boo\n")
	if tr.Tail("ghost") != "" || len(tr.Tails()) != 0 {
		t.Fatal("output for an untracked Fix must be dropped")
	}
}

func TestTailIsBoundedAtLineBoundary(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	line := strings.Repeat("x", 99) + "\n"
	for range tailBytes/len(line) + 20 {
		tr.Append("f1", line)
	}
	tail := tr.Tail("f1")
	if len(tail) > tailBytes {
		t.Fatalf("tail %d bytes > %d", len(tail), tailBytes)
	}
	if !strings.HasPrefix(tail, "xxx") || len(strings.Split(strings.TrimSuffix(tail, "\n"), "\n")[0]) != 99 {
		t.Fatalf("tail does not start on a line boundary: %q", tail[:120])
	}
}

func TestTerminalDropsTail(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	tr.Append("f1", "work\n")
	tr.Set(Fix{ID: "f1", State: OK})
	if tr.Tail("f1") != "" {
		t.Fatal("finished Fix should keep no tail")
	}
	if len(tr.Tails()) != 0 {
		t.Fatal("no snapshot for a finished Fix")
	}
}

func TestTailsSnapshotForConnectingClient(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "a", State: Fixing})
	tr.Set(Fix{ID: "f2", Repo: "b", State: Cloning})
	tr.Append("f1", "one\n")
	snaps := tr.Tails()
	if len(snaps) != 1 || snaps[0].ID != "f1" || !snaps[0].Snapshot || snaps[0].Text != "one\n" {
		t.Fatalf("snapshots=%+v", snaps)
	}
}

// A subscriber that stops reading must not hold a Fix up, and when it
// reads again it gets the whole tail, not a stream with a hole in it.
func TestSlowSubscriberGetsSnapshotAfterDrop(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	ch, unsub := tr.SubscribeOutput()
	defer unsub()
	for range outputSubBuffer + 5 {
		tr.Append("f1", "l\n")
	}
	// Drain what was buffered; none of it is a snapshot.
	for i := 0; i < outputSubBuffer; i++ {
		if o := <-ch; o.Snapshot {
			t.Fatalf("chunk %d was a snapshot before any drop was visible", i)
		}
	}
	tr.Append("f1", "after\n")
	o := <-ch
	if !o.Snapshot || !strings.HasSuffix(o.Text, "after\n") {
		t.Fatalf("want snapshot ending in the new line, got %+v", o)
	}
}

func TestWriterAssemblesLinesAndRenders(t *testing.T) {
	tr := New()
	tr.Set(Fix{ID: "f1", Repo: "svc", State: Fixing})
	w := tr.Writer("f1", func(s string) string {
		if s == "skip" {
			return ""
		}
		return "> " + s
	})
	_, _ = w.Write([]byte("par"))
	_, _ = w.Write([]byte("tial\nskip\nsecond\nno-newline"))
	if got := tr.Tail("f1"); got != "> partial\n> second\n" {
		t.Fatalf("tail before close=%q", got)
	}
	_ = w.Close()
	if got := tr.Tail("f1"); got != "> partial\n> second\n> no-newline\n" {
		t.Fatalf("tail after close=%q", got)
	}
}

func TestNilTrackerOutputIsNoop(t *testing.T) {
	var tr *Tracker
	tr.Append("x", "y")
	if tr.Tail("x") != "" || tr.Tails() != nil {
		t.Fatal("nil tracker must be inert")
	}
	ch, unsub := tr.SubscribeOutput()
	unsub()
	if _, ok := <-ch; ok {
		t.Fatal("nil tracker channel must be closed")
	}
	w := tr.Writer("x", nil)
	if _, err := w.Write([]byte("z\n")); err != nil {
		t.Fatal(err)
	}
}
