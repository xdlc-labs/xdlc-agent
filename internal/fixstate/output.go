package fixstate

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// Output is a chunk of what a running Fix's agent printed, as the
// console sees it over the "fix_output" SSE event.
//
// The phase strip says what a Fix is doing; this says what the agent is
// saying while it does it. It is a live view only: the complete
// transcript is the session recording, which outlives the process.
type Output struct {
	ID   string `json:"id"`
	Repo string `json:"repo,omitempty"`
	Text string `json:"text"`
	// Snapshot marks a chunk that carries the whole tail kept so far —
	// sent to a client that just connected, or to one that fell behind
	// and had chunks dropped. It replaces what the client holds instead
	// of appending to it.
	Snapshot bool `json:"snapshot,omitempty"`
}

// tailBytes is how much output the tracker keeps per Fix. Enough to see
// how a run is going, not a transcript: a console tab is not the record.
const tailBytes = 16 << 10

// outputSubBuffer is how many chunks a slow subscriber may fall behind
// before it is marked lossy and resynchronized with a snapshot.
const outputSubBuffer = 256

type outputSub struct {
	ch chan Output
	// lost is set when a chunk had to be dropped. The next successful
	// send is then a snapshot, so a client never shows a tail with a
	// hole in it without knowing.
	lost bool
}

// Append records text printed by Fix id and publishes it. Text for a
// Fix the tracker does not know (finished, or never started) is
// dropped: there is no row for it to belong to.
func (t *Tracker) Append(id, text string) {
	if t == nil || id == "" || text == "" {
		return
	}
	t.mu.Lock()
	f, ok := t.active[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	defer t.mu.Unlock()
	if t.tails == nil {
		t.tails = map[string]*bytes.Buffer{}
	}
	tail := t.tails[id]
	if tail == nil {
		tail = &bytes.Buffer{}
		t.tails[id] = tail
	}
	// A buffer rather than string concatenation: the tail is rewritten
	// on every line an agent prints, and copying 16 KB per line under
	// the lock added up. Next advances the read offset without copying;
	// the buffer compacts itself the next time it has to grow.
	tail.WriteString(text)
	if tail.Len() > tailBytes {
		// Cut at a line boundary so the tail never starts mid-line.
		cut := tail.Len() - tailBytes
		if nl := bytes.IndexByte(tail.Bytes()[cut:], '\n'); nl >= 0 {
			cut += nl + 1
		}
		tail.Next(cut)
	}

	// Sends happen under the lock, the same lock SubscribeOutput's unsub
	// closes the channel under, so a console that disconnects while a
	// Fix is printing can never make this a send on a closed channel.
	// The sends are non-blocking, so holding the lock costs nothing.
	chunk := Output{ID: id, Repo: f.Repo, Text: text}
	var snap *Output
	for _, s := range t.outSubs {
		msg := chunk
		if s.lost {
			if snap == nil {
				// Built once, and only when a subscriber needs it: the
				// tail string is a copy of the whole buffer.
				snap = &Output{ID: id, Repo: f.Repo, Text: tail.String(), Snapshot: true}
			}
			msg = *snap
		}
		select {
		case s.ch <- msg:
			s.lost = false
		default:
			s.lost = true
		}
	}
}

// Tail returns what the tracker has kept of Fix id's output.
func (t *Tracker) Tail(id string) string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tail := t.tails[id]; tail != nil {
		return tail.String()
	}
	return ""
}

// Tails returns a snapshot chunk for every running Fix that has printed
// anything, for a client that just connected.
func (t *Tracker) Tails() []Output {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Output, 0, len(t.tails))
	for _, f := range t.active {
		if tail := t.tails[f.ID]; tail != nil && tail.Len() > 0 {
			out = append(out, Output{ID: f.ID, Repo: f.Repo, Text: tail.String(), Snapshot: true})
		}
	}
	return out
}

// SubscribeOutput returns a channel of output chunks and a function that
// stops the subscription and closes the channel.
func (t *Tracker) SubscribeOutput() (<-chan Output, func()) {
	if t == nil {
		ch := make(chan Output)
		close(ch)
		return ch, func() {}
	}
	s := &outputSub{ch: make(chan Output, outputSubBuffer)}
	t.mu.Lock()
	if t.outSubs == nil {
		t.outSubs = map[int]*outputSub{}
	}
	id := t.nextID
	t.nextID++
	t.outSubs[id] = s
	t.mu.Unlock()

	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			// Close under the lock: Append sends under it, so once this
			// returns no send can race the close.
			t.mu.Lock()
			delete(t.outSubs, id)
			close(s.ch)
			t.mu.Unlock()
		})
	}
}

// Writer returns an io.Writer that feeds Fix id's output to the tracker
// one line at a time, each line passed through render first. A render
// that returns "" drops the line, which is how a stream-json event with
// nothing human in it stays out of the console. Close flushes a final
// partial line. A nil tracker returns a writer that discards.
func (t *Tracker) Writer(id string, render func(line string) string) io.WriteCloser {
	if t == nil {
		return nopCloser{io.Discard}
	}
	if render == nil {
		render = func(s string) string { return s }
	}
	return &lineWriter{t: t, id: id, render: render}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// lineWriter buffers partial lines. Subprocess writes arrive in
// arbitrary chunks, and a stream-json event split across two writes is
// not parseable until it is whole.
type lineWriter struct {
	t      *Tracker
	id     string
	render func(string) string
	mu     sync.Mutex
	buf    bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		i := bytes.IndexByte(w.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf.Next(i + 1))
		w.emit(strings.TrimRight(line, "\r\n"))
	}
	// A partial line that grows past the tail limit will never be
	// completed by anything the console can show; flush it as is.
	if w.buf.Len() > tailBytes {
		w.emit(w.buf.String())
		w.buf.Reset()
	}
	return len(p), nil
}

func (w *lineWriter) emit(line string) {
	if line == "" {
		return
	}
	if out := w.render(line); out != "" {
		w.t.Append(w.id, out+"\n")
	}
}

// Close flushes a trailing line that had no newline.
func (w *lineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() > 0 {
		w.emit(strings.TrimRight(w.buf.String(), "\r\n"))
		w.buf.Reset()
	}
	return nil
}
