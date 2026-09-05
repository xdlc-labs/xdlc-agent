package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

func TestFlapTransitions(t *testing.T) {
	if n := flapTransitions([]string{"fix", "revert", "fix", "revert"}); n != 3 {
		t.Fatalf("got %d want 3", n)
	}
	if n := flapTransitions([]string{"fix", "fix", "promote", "revert"}); n != 1 {
		t.Fatalf("got %d want 1", n)
	}
	if n := flapTransitions(nil); n != 0 {
		t.Fatalf("got %d", n)
	}
}

func TestRootCauseSuppressesRevert(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.RepoDeps = map[string][]string{"web": {"api"}}
	o.Fleet.RepoCount = 2

	auditCh := make(chan Action, 2)
	o.Audit = func(s Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceProdHealth, Repo: "api", Kind: KindBreach}
	select {
	case a := <-auditCh:
		if a != ActionRevert {
			t.Fatalf("api want revert, got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout api")
	}

	o.Signals <- Signal{Source: SourceProdHealth, Repo: "web", Kind: KindBreach}
	select {
	case a := <-auditCh:
		if a != ActionNoop {
			t.Fatalf("web want noop (root_cause), got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout web")
	}
	cancel()
	<-done

	if len(disp.revertCalls) != 1 || disp.revertCalls[0].Repo != "api" {
		t.Fatalf("want only api reverted, got %+v", disp.revertCalls)
	}
}

func TestCircuitSuppressesFleet(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.Fleet = FleetPolicy{CircuitBreachRatio: 0.5, RepoCount: 2}

	auditCh := make(chan Action, 4)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	// Mark both breaching via prod-health signals (first may revert before ratio hits)
	o.Signals <- Signal{Source: SourceProdHealth, Repo: "a", Kind: KindBreach}
	// Wait for first audit so breach map has a
	select {
	case <-auditCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout first")
	}
	o.Signals <- Signal{Source: SourceProdHealth, Repo: "b", Kind: KindBreach}
	select {
	case a := <-auditCh:
		if a != ActionNoop {
			t.Fatalf("second breach want noop (circuit), got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout second")
	}
	// CI fail should also be suppressed
	o.Signals <- Signal{Source: SourceCI, Repo: "a", Kind: KindFail}
	select {
	case a := <-auditCh:
		if a != ActionNoop {
			t.Fatalf("fix want noop, got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout fix")
	}

	cancel()
	<-done
	if len(disp.fixCalls) != 0 {
		t.Fatalf("fix should be suppressed: %+v", disp.fixCalls)
	}
}

func TestFlapSuppressesFix(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.Fleet = FleetPolicy{FlapMaxCycles: 2, FlapWindow: time.Hour, RepoCount: 1}
	o.RecentActions = func(repo string, since time.Time) ([]string, error) {
		return []string{"fix", "revert", "fix", "revert"}, nil // 3 transitions ≥ 2
	}

	auditCh := make(chan Action, 1)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceCI, Repo: "svc", Kind: KindFail}
	select {
	case a := <-auditCh:
		if a != ActionNoop {
			t.Fatalf("got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	<-done
	if len(disp.fixCalls) != 0 {
		t.Fatalf("expected suppress, got %+v", disp.fixCalls)
	}
}

func TestPromoteSuppressedWhenDepBreaching(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.RepoDeps = map[string][]string{"web": {"api"}}
	o.Fleet.RepoCount = 2

	auditCh := make(chan Action, 2)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error {
		auditCh <- action
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceProdHealth, Repo: "api", Kind: KindBreach}
	select {
	case <-auditCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout breach")
	}

	o.Signals <- Signal{Source: SourceDevGate, Repo: "web", Kind: KindPass}
	select {
	case a := <-auditCh:
		if a != ActionNoop {
			t.Fatalf("want noop deps_unhealthy, got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout promote")
	}
	cancel()
	<-done
	if len(disp.promoteCalls) != 0 {
		t.Fatalf("promote should be suppressed: %+v", disp.promoteCalls)
	}
}

func TestNotifyEscalate(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	old := notifyHTTPClient
	notifyHTTPClient = srv.Client()
	t.Cleanup(func() { notifyHTTPClient = old })

	if err := notifyEscalate(context.Background(), srv.URL, "web", "promote", "deps_unhealthy"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "deps_unhealthy") || !strings.Contains(got, "web") {
		t.Fatalf("body %s", got)
	}
}

func TestTagAtLeast(t *testing.T) {
	if !tagAtLeast("v1.2.0", "v1.1.0") {
		t.Fatal("1.2 >= 1.1")
	}
	if tagAtLeast("v1.0.0", "v1.2.0") {
		t.Fatal("1.0 < 1.2")
	}
	if !tagAtLeast("abc", "abc") {
		t.Fatal("exact non-semver")
	}
}

func TestPromotePinSuppresses(t *testing.T) {
	bl, err := backlog.Open(filepath.Join(t.TempDir(), "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	disp := &fakeDispatcher{}
	o := New(disp, bl, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o.PromotePins = map[string][]PromotePin{"web": {{Repo: "api", MinTag: "v1.2.0"}}}
	o.ProdTag = func(repo string) (string, error) { return "v1.0.0", nil }
	o.Fleet.RepoCount = 2

	auditCh := make(chan Action, 1)
	o.Audit = func(_ Signal, action Action, _ error, _ time.Time) error { auditCh <- action; return nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	o.Signals <- Signal{Source: SourceDevGate, Repo: "web", Kind: KindPass}
	select {
	case a := <-auditCh:
		if a != ActionNoop {
			t.Fatalf("want noop deps_pin, got %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	<-done
	if len(disp.promoteCalls) != 0 {
		t.Fatal("promote should be suppressed")
	}
}
