package subagent

import (
	"strings"
	"testing"
)

// The tail writer must hand back exactly the last limit bytes of
// whatever was written, however the writes were chunked, and the whole
// stream when it never reached the limit.
func TestTailWriterKeepsTheEnd(t *testing.T) {
	const limit = 16
	full := "0123456789abcdefghijklmnopqrstuvwxyz" // 36 bytes
	cases := []struct {
		name   string
		chunks []int
	}{
		{"one byte at a time", []int{1}},
		{"small chunks", []int{3, 5, 7}},
		{"chunk that straddles the limit", []int{10, 10, 10, 6}},
		{"chunk bigger than the limit", []int{5, 31}},
		{"single write bigger than the limit", []int{36}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newTailWriter(limit)
			for i, off := 0, 0; off < len(full); i++ {
				size := c.chunks[i%len(c.chunks)]
				end := min(off+size, len(full))
				n, err := w.Write([]byte(full[off:end]))
				if err != nil || n != end-off {
					t.Fatalf("Write returned (%d, %v), want (%d, nil)", n, err, end-off)
				}
				off = end
			}
			if got, want := w.String(), full[len(full)-limit:]; got != want {
				t.Fatalf("tail=%q want %q", got, want)
			}
		})
	}
}

func TestTailWriterUnderLimitKeepsEverything(t *testing.T) {
	w := newTailWriter(64)
	for _, part := range []string{"hello", " ", "world\n"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.String(); got != "hello world\n" {
		t.Fatalf("tail=%q", got)
	}
	// Exactly the limit is not over it.
	w = newTailWriter(4)
	_, _ = w.Write([]byte("abcd"))
	if got := w.String(); got != "abcd" {
		t.Fatalf("tail=%q", got)
	}
	_, _ = w.Write([]byte("e"))
	if got := w.String(); got != "bcde" {
		t.Fatalf("tail=%q", got)
	}
}

// Run's readers only need the end of the stream: a verdict printed
// after megabytes of chatter must still parse out of the tail.
func TestTailWriterKeepsVerdictAfterLongOutput(t *testing.T) {
	w := newTailWriter(1 << 10)
	_, _ = w.Write([]byte(strings.Repeat("noise\n", 1000)))
	verdict := `{"xdlc_outcome":"fixed","summary":"done"}` + "\n"
	_, _ = w.Write([]byte(verdict))
	if v := ParseVerdict(w.String()); v.Outcome != OutcomeFixed {
		t.Fatalf("verdict lost from tail: %+v", v)
	}
}
