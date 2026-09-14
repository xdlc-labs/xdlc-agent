package ghclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestNextPollInterval(t *testing.T) {
	cases := []struct{ in, want time.Duration }{
		{5 * time.Second, 10 * time.Second},
		{10 * time.Second, 20 * time.Second},
		{20 * time.Second, maxPollInterval},
		{maxPollInterval, maxPollInterval},
		{time.Minute, maxPollInterval},
	}
	for _, c := range cases {
		if got := nextPollInterval(c.in); got != c.want {
			t.Errorf("nextPollInterval(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

// The poll loop backs off 5s → 10s → 20s → 30s and stays at the cap,
// and stops as soon as the run completes.
func TestWaitRunConclusionBacksOff(t *testing.T) {
	polls := 0
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/svc/actions/runs/99" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		polls++
		if polls <= 5 {
			_, _ = fmt.Fprint(w, `{"id":99,"status":"in_progress"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":99,"status":"completed","conclusion":"success"}`)
	}))

	var slept []time.Duration
	orig := pollSleep
	pollSleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	t.Cleanup(func() { pollSleep = orig })

	got, err := c.WaitRunConclusion(context.Background(), "https://github.com/acme/svc/actions/runs/99", 5*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got != "success" || polls != 6 {
		t.Fatalf("conclusion=%q polls=%d", got, polls)
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, 30 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("slept %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("slept %v, want %v", slept, want)
		}
	}
}

// A cancelled context ends the wait with the context's error rather
// than another poll.
func TestWaitRunConclusionStopsOnCancel(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":99,"status":"queued"}`)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	orig := pollSleep
	pollSleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	t.Cleanup(func() { pollSleep = orig })
	_, err := c.WaitRunConclusion(ctx, "https://github.com/acme/svc/actions/runs/99", time.Second, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
