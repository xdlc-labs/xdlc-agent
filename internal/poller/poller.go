// Package poller runs a Gate.Check on a ticker for each repo it applies
// to, turning the Result into a Signal. Used for the dev-smoke and
// prod-health gates, which don't have (or don't need) a real-time
// webhook wired up.
package poller

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/xdlc-labs/xdlc-agent/internal/gate"
	"github.com/xdlc-labs/xdlc-agent/internal/orchestrator"
	"github.com/xdlc-labs/xdlc-agent/internal/otel"

	"go.opentelemetry.io/otel/metric"
)

// Poller ticks Gate.Check for every repo in Repos on Interval, emitting
// a Signal per result. See Run.
//
// Emissions are edge-triggered: a Signal is sent only when a repo's Kind
// (or, when SHA is set, the gated commit) changes since the last
// successful Check. Level-triggering would promote/fix/revert on every
// tick while the condition holds (sustained breach → revert-the-revert
// cascade).
type Poller struct {
	Gate     gate.Gate
	Repos    []string
	Interval time.Duration
	Source   orchestrator.Source
	Signals  chan<- orchestrator.Signal
	Log      *slog.Logger
	Metrics  *otel.Metrics // optional
	// Parallelism caps concurrent Gate.Check calls per tick (issue #10).
	// 0 → default 8.
	Parallelism int
	// SHA optionally resolves the commit a Check's verdict applies to —
	// the repo's dev branch tip (repos.Manager.RemoteSHA). Set it for
	// gates whose pass authorizes a promote (dev-smoke), so the promote
	// can be pinned to the commit that was probed instead of shipping
	// whatever the branch has become. nil → unpinned Signals.
	//
	// Costs one `git ls-remote` per repo per tick. That is deliberate:
	// it is read *before* Gate.Check, so a commit landing mid-probe
	// cannot inherit the probe's verdict (the promote then fails the pin
	// check rather than shipping it untested).
	SHA func(ctx context.Context, repo string) (string, error)
	// Timeout bounds one whole tick — every Gate.Check (and SHA lookup)
	// it starts shares the deadline. 0 → tickTimeoutRatio × Interval.
	//
	// A gate that can block indefinitely is not just a slow gate, it is
	// a *disabled* one: Run calls tick synchronously, so a Check with no
	// deadline (promclient used to inherit the daemon's root context and
	// http.DefaultClient, neither of which has one) parks the ticker
	// loop forever. The next tick never happens, nothing is logged
	// because the "tick slow" warn sits after the wait, and the gate
	// stops working while looking healthy.
	//
	// Keeping the default under Interval also means ticks cannot stack:
	// a tick is always finished (or cancelled) before the next is due.
	Timeout time.Duration

	// BlockedRepeatWindow bounds how often one repo's "gate could not
	// run" is re-reported when the reason *text* changes between ticks.
	// 0 → defaultBlockedRepeatWindow. See blockedEpisode.
	BlockedRepeatWindow time.Duration

	mu   sync.Mutex
	last map[string]edgeState // repo → last emitted Kind + SHA

	// blockedMu guards blocked, the "gate could not run" episode per
	// repo. See blockedEpisode.
	blockedMu sync.Mutex
	blocked   map[string]blockedState
}

// blockedState is the "gate could not run" episode a repo is currently
// in: the reason already reported, and when it was reported.
type blockedState struct {
	reason string
	at     time.Time
}

// defaultBlockedRepeatWindow is how long a repo stays quiet after a
// "gate could not run" record, even if the reason text changes.
//
// Reason equality alone is not enough, because real CLI diagnostics are
// not stable strings: `argocd` prefixes its fatal line with its own
// timestamp, so a permanently misconfigured argocd_app produced a
// *different* reason every tick and wrote a record per tick after all
// (measured against a stub argocd). An hour is long enough that a dead
// gate cannot flood a BACKLOG.md, and short enough that an operator who
// starts looking later still finds a recent record rather than one
// buried at daemon start.
const defaultBlockedRepeatWindow = time.Hour

// edgeState is what a repo last emitted, and so what a new tick has to
// differ from to emit again.
type edgeState struct {
	kind orchestrator.Kind
	sha  string
}

// Run blocks, ticking every p.Interval until ctx is cancelled. Each tick
// checks every configured repo and emits a Signal only on Kind change.
//
// Ticks are serial: tick is called synchronously and bounded by
// p.Timeout (see tickTimeout), which defaults to under one interval, so
// a tick always ends before the next is due and slow ticks can neither
// stack up nor wedge the loop.
func (p *Poller) Run(ctx context.Context) {
	interval := p.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx, interval)
		}
	}
}

// tickTimeoutRatio is the fraction of Interval a tick gets to finish in
// when Timeout is unset. It is deliberately the same fraction the "tick
// slow" warn already used: the point at which a tick was considered too
// slow to be healthy is now also the point at which it is abandoned, so
// an overrun is both bounded and logged instead of silent.
const tickTimeoutRatio = 0.8

// defaultTickTimeout bounds a tick when neither Timeout nor a positive
// interval is available (a direct tick call in a test, say).
const defaultTickTimeout = 30 * time.Second

// tickTimeout is the deadline one tick gets.
func (p *Poller) tickTimeout(interval time.Duration) time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	if interval > 0 {
		return time.Duration(float64(interval) * tickTimeoutRatio)
	}
	return defaultTickTimeout
}

func (p *Poller) tick(ctx context.Context, interval time.Duration) {
	start := time.Now()
	timeout := p.tickTimeout(interval)
	// One deadline for the whole tick, shared by every Check it starts:
	// without it a gate that never returns blocks Run's ticker loop and
	// the gate stops polling entirely.
	tickCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	n := p.Parallelism
	if n <= 0 {
		n = 8
	}
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for _, repo := range p.Repos {
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-tickCtx.Done():
				return
			}
			p.checkOne(tickCtx, repo)
		}(repo)
	}
	wg.Wait()
	elapsed := time.Since(start)
	switch {
	case ctx.Err() != nil:
		// Daemon shutdown, not a gate problem.
	case tickCtx.Err() != nil:
		// The loud case. A Prometheus (or ArgoCD, or probe Job) that
		// stops answering has to be visible to an operator, because the
		// gate's verdict for this tick is "unknown", not "healthy".
		p.Log.Error("poller tick timed out",
			"gate", p.Gate.Name(), "timeout", timeout, "elapsed", elapsed,
			"interval", interval, "repos", len(p.Repos))
	case interval > 0 && elapsed > time.Duration(float64(interval)*tickTimeoutRatio):
		p.Log.Warn("poller tick slow",
			"gate", p.Gate.Name(), "elapsed", elapsed, "interval", interval, "repos", len(p.Repos))
	}
}

func (p *Poller) checkOne(ctx context.Context, repo string) {
	if p.Metrics != nil {
		// Recorded before Check so a stalled/hanging Gate.Check still
		// shows the tick attempt — staleness alerts key off this.
		p.Metrics.PollerLastTick.Record(ctx, float64(time.Now().Unix()), metric.WithAttributes(
			otel.AttrGate(p.Gate.Name()), otel.AttrRepo(repo)))
	}
	// Read the gated commit before the check, not after: the verdict
	// belongs to whatever was deployed when the probe started.
	var sha string
	if p.SHA != nil {
		var err error
		if sha, err = p.SHA(ctx, repo); err != nil {
			// A pass we can't attribute to a commit would promote an
			// unverified tip, so skip the repo this tick instead.
			p.Log.Error("poller: cannot resolve gated sha", "gate", p.Gate.Name(), "repo", repo, "error", err)
			return
		}
	}

	result, err := p.Gate.Check(ctx, repo)
	if err != nil {
		// Counted on every tick: the counter is the continuous,
		// alertable view of "this gate is not running", and a counter
		// cannot flood. The human-readable channels below are
		// edge-triggered instead.
		if p.Metrics != nil {
			p.Metrics.GateChecks.Add(ctx, 1, metric.WithAttributes(
				otel.AttrGate(p.Gate.Name()), otel.AttrStatus("error")))
		}
		if ctx.Err() != nil {
			// The tick deadline (or a daemon shutdown) cancelled the
			// check, so the error says nothing about the gate's own
			// configuration. tick() reports an overrun itself, at
			// ERROR, on every tick — that is the issue #42 signal — and
			// the tick context is already dead, so there is nothing
			// left to carry a Signal.
			p.Log.Error("poller: gate check failed", "gate", p.Gate.Name(), "repo", repo, "error", err)
			return
		}
		if !p.blockedEpisode(repo, err.Error()) {
			// Same failure as last tick. A permanently misconfigured
			// argocd_app must not write one of these per interval
			// forever.
			p.Log.Debug("poller: gate still cannot run",
				"gate", p.Gate.Name(), "repo", repo, "error", err)
			return
		}
		p.Log.Error("poller: gate check failed", "gate", p.Gate.Name(), "repo", repo, "error", err)
		// A gate that cannot run is not a failing gate: emitting
		// KindFail here would route dev-smoke to ActionFix and pay a
		// coding agent to fix a repo that is not broken. KindBlocked is
		// ActionNoop plus an operator-visible record (issue #45).
		p.send(ctx, orchestrator.Blocked(p.Source, repo, p.Gate.Name(), sha, err))
		return
	}
	p.clearBlocked(repo)

	kind := orchestrator.KindPass
	status := "pass"
	if result.Status == gate.StatusFail {
		kind = orchestrator.KindFail
		status = "fail"
		if p.Source == orchestrator.SourceProdHealth {
			kind = orchestrator.KindBreach
			status = "breach"
		}
	}
	if p.Metrics != nil {
		p.Metrics.GateChecks.Add(ctx, 1, metric.WithAttributes(
			otel.AttrGate(p.Gate.Name()), otel.AttrStatus(status)))
	}

	if !p.edge(repo, kind, sha) {
		return
	}

	p.send(ctx, orchestrator.Signal{
		Source:   p.Source,
		Repo:     repo,
		Kind:     kind,
		SHA:      sha,
		Evidence: result.Evidence,
		At:       time.Now(),
	})
}

// send hands a Signal to the orchestrator, or gives up when ctx ends
// first. The channel is buffered, but a daemon shutting down (or a tick
// past its deadline) stops draining it, and a bare send would then park
// this goroutine forever with the tick's WaitGroup waiting on it.
func (p *Poller) send(ctx context.Context, sig orchestrator.Signal) {
	select {
	case p.Signals <- sig:
	case <-ctx.Done():
		p.Log.Warn("poller: signal dropped; context ended before the orchestrator took it",
			"gate", p.Gate.Name(), "repo", sig.Repo, "kind", sig.Kind)
	}
}

// blockedEpisode reports whether this "gate could not run" is new
// enough to be worth telling an operator about, and remembers it when
// it is.
//
// The poller re-checks every interval, so without this a typo'd
// argocd_app would write an ERROR log line, a BACKLOG.md entry and an
// audit row every tick for the lifetime of the process.
//
// Two guards, because either one alone leaks. An identical reason is
// never re-reported, which covers the common case. But a reason is a
// CLI's diagnostic, not a stable key — `argocd` stamps its own
// timestamp into the fatal line it prints — so a changed reason is also
// held to one report per BlockedRepeatWindow. That keeps a *genuinely*
// different failure (the app name was fixed and now the session has
// expired) visible, without letting a noisy message reinstate the
// per-tick flood.
//
// It deliberately keeps its own state instead of using the verdict edge
// (p.last). Recording "blocked" there would make the next real verdict
// look like a transition, so a sustained prod-health breach interrupted
// by one failed check would emit a second breach and revert the revert.
// A recovered check clears the episode (clearBlocked), so a gate that
// breaks again later is reported again.
func (p *Poller) blockedEpisode(repo, reason string) bool {
	window := p.BlockedRepeatWindow
	if window <= 0 {
		window = defaultBlockedRepeatWindow
	}
	p.blockedMu.Lock()
	defer p.blockedMu.Unlock()
	if p.blocked == nil {
		p.blocked = make(map[string]blockedState)
	}
	now := time.Now()
	if prev, ok := p.blocked[repo]; ok {
		if prev.reason == reason || now.Sub(prev.at) < window {
			return false
		}
	}
	p.blocked[repo] = blockedState{reason: reason, at: now}
	return true
}

// clearBlocked forgets repo's blocked episode after a Check that
// actually returned a verdict, re-arming the report for the next break.
func (p *Poller) clearBlocked(repo string) {
	p.blockedMu.Lock()
	defer p.blockedMu.Unlock()
	delete(p.blocked, repo)
}

// edge returns true if kind — or, when SHA resolution is wired, the
// gated commit — differs from what repo last emitted (including the
// first observation). Updates last when true.
//
// The SHA is part of the edge because a promote is now pinned to it: a
// second commit that also passes the gate is a *new* thing to promote,
// even though the Kind never changed, and keying on Kind alone would
// leave it gated-but-unshipped forever.
func (p *Poller) edge(repo string, kind orchestrator.Kind, sha string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil {
		p.last = make(map[string]edgeState)
	}
	next := edgeState{kind: kind, sha: sha}
	if prev, ok := p.last[repo]; ok && prev == next {
		return false
	}
	p.last[repo] = next
	return true
}
