// Package orchestrator is the single loop: read a Signal, Decide an
// Action, dispatch it. See loop.go for the loop itself and decide.go
// for the (pure, easily-tested) policy.
package orchestrator

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xdlc-labs/xdlc-agent/internal/backlog"
)

// FixResult is what a Fix run delivered, as opposed to whether it
// errored. The two are not the same thing: an agent that timed out, was
// OOM-killed, or edited files without ever committing exits 0 and leaves
// the Dispatcher nothing to report but a nil error (issue #34).
//
// Delivered means code actually reached the branch this Fix targets —
// the daemon pushed the agent's commits, or (in the shared-clone mode
// where the agent does its own pushing) the daemon has no push of its
// own to judge and takes the run's own word for it. Only a delivered
// Fix marks the SHA fixed; see finishFixSHA.
type FixResult struct {
	Delivered bool
	// PushedSHAs are the commits the daemon itself pushed to the tracked
	// branch during this Fix. GitHub will report a workflow_run for each;
	// those runs are the reverify's subject, never a new failure to Fix.
	PushedSHAs []string
}

// Dispatcher performs the side-effecting part of an Action: running a
// subagent, reverting, or promoting. Kept as an interface so loop.go
// stays testable without shelling out to git/claude for real.
type Dispatcher interface {
	Fix(ctx context.Context, s Signal) (FixResult, error)
	Revert(ctx context.Context, s Signal) error
	Promote(ctx context.Context, s Signal) error
}

// RerunCIFunc tries GitHub rerun-failed-jobs for a CI fail signal.
// Returns green=true when the rerun concludes success (skip Fix).
// nil disables the ladder (issue #3).
type RerunCIFunc func(ctx context.Context, s Signal) (green bool, err error)

// AuditFunc persists a structured record of one signal+action, e.g. to
// the bbolt-backed internal/store for `xdlc history`. Optional —
// nil skips it. Kept as a func type (not a store.AuditStore field)
// so orchestrator doesn't need to import store.
// May be called concurrently from different repo workers; implementations
// must be safe for concurrent use (bbolt Append is).
// dispatchErr is nil when the Action completed (or was noop); started is
// when handle began dispatch so DurationMS can be recorded.
type AuditFunc func(s Signal, action Action, dispatchErr error, started time.Time) error

// Orchestrator reads Signals off a channel (fed by gate webhooks/pollers —
// see cmd/xdlc-agent), decides an Action, dispatches it, and records what
// happened to BACKLOG.md (and, if set, Audit). Different repos run
// concurrently; each repo is processed serially.
type Orchestrator struct {
	Signals    chan Signal
	Dispatcher Dispatcher
	Backlog    *backlog.Store
	Audit      AuditFunc
	Log        *slog.Logger

	// RerunCI is the flake ladder before Fix (issue #3). Optional.
	RerunCI RerunCIFunc
	// reran tracks run_urls this process asked GitHub to rerun, with the
	// run_attempt the rerun's own completion will carry (the attempt we
	// saw, plus one). GitHub delivers that completion like any other;
	// it is the answer the ladder already read, not a new failure.
	reranMu sync.Mutex
	reran   map[string]int

	// ownSHA is every commit the daemon pushed to a tracked branch
	// (repo+SHA). A fail for one of those is the reverify's subject —
	// or a Fix that reverify already judged — never a fresh red to Fix.
	ownMu  sync.Mutex
	ownSHA map[string]struct{}

	// TipSHA optionally reports the tracked branch's current remote tip
	// for a repo, so a fail signal for a commit that is no longer the tip
	// is dropped rather than reran and fixed on a checkout that does not
	// contain it. nil skips the check.
	TipSHA func(ctx context.Context, repo string) (string, error)

	// fixSHA is one delivered Fix per repo+SHA this process lifetime.
	// A flake-ladder rerun of the same commit used to open a second PR
	// after the first Fix already succeeded. Empty SHA (manual Fix) is
	// never claimed. A failed Fix — or one that delivered nothing —
	// clears inflight without latching ok, so a later delivery can retry.
	fixSHAMu sync.Mutex
	fixSHA   map[string]fixSHAState

	// Fleet policy (optional; zero Fleet = no suppressions).
	Fleet    FleetPolicy
	RepoDeps map[string][]string // short name → depends_on
	// PromotePins: repo → required dependency min tags (v2).
	PromotePins map[string][]PromotePin
	// ProdTag returns current gitops prod image.tag for a repo; nil skips pin checks.
	ProdTag       func(repo string) (string, error)
	RecentActions RecentActionsFunc
	// Suppressions is optional; when set, Incremented on each suppress.
	Suppressions metric.Int64Counter

	breachMu sync.Mutex
	breach   map[string]bool // prod-health breach by repo

	// patientZeroFired: upstream already enqueued this breach episode (#4).
	pzMu             sync.Mutex
	patientZeroFired map[string]struct{}
}

// New returns an Orchestrator ready to Run — its Signals channel
// (buffered 64) isn't fed by anything yet; wire gate webhooks/pollers to
// write to it before calling Run.
func New(dispatcher Dispatcher, bl *backlog.Store, log *slog.Logger) *Orchestrator {
	return &Orchestrator{
		Signals:    make(chan Signal, 64),
		Dispatcher: dispatcher,
		Backlog:    bl,
		Log:        log,
		breach:     map[string]bool{},
		RepoDeps:   map[string][]string{},
		reran:      map[string]int{},
		ownSHA:     map[string]struct{}{},
		fixSHA:     map[string]fixSHAState{},
	}
}

type fixSHAState struct {
	inflight bool
	ok       bool
}

// Run blocks, processing signals until ctx is cancelled. Fan-out is by
// repo: one worker goroutine per repo, serial within that repo.
func (o *Orchestrator) Run(ctx context.Context) error {
	// ponytail: one goroutine+chan per repo forever; add idle eviction if repo cardinality unbounded
	workers := make(map[string]chan Signal)
	var wg sync.WaitGroup
	defer func() {
		for _, ch := range workers {
			close(ch)
		}
		wg.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case s := <-o.Signals:
			ch, ok := workers[s.Repo]
			if !ok {
				ch = make(chan Signal, 64)
				workers[s.Repo] = ch
				wg.Add(1)
				go func(ch <-chan Signal) {
					defer wg.Done()
					for s := range ch {
						o.handle(ctx, s)
					}
				}(ch)
			}
			// ponytail: latest-wins per Source — drain same-Source pending
			// before enqueue. O(buf) scan; fine at buf=64. Upgrade: ring
			// with per-source slot if enqueue rate becomes hot.
			drainSameSource(ch, s.Source)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- s:
			}
		}
	}
}

// drainSameSource drops pending signals with src from ch, re-queues the rest.
func drainSameSource(ch chan Signal, src Source) {
	var keep []Signal
	for {
		select {
		case old := <-ch:
			if old.Source != src {
				keep = append(keep, old)
			}
		default:
			for _, k := range keep {
				ch <- k
			}
			return
		}
	}
}

func (o *Orchestrator) handle(ctx context.Context, s Signal) {
	o.updateBreach(s)

	action := Decide(s)
	var suppress string
	if reason := o.suppressReason(&s, action); reason != "" {
		suppress = reason
		o.Log.Warn("fleet policy suppressed action",
			"repo", s.Repo, "would", action, "escalate", reason)
		if o.Suppressions != nil {
			o.Suppressions.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
		}
		if o.Fleet.NotifyWebhookURL != "" {
			if nerr := notifyEscalate(ctx, o.Fleet.NotifyWebhookURL, s.Repo, string(action), reason); nerr != nil {
				o.Log.Warn("fleet notify webhook failed", "error", nerr)
			}
		}
		action = ActionNoop
	}

	o.Log.Info("signal received", "source", s.Source, "repo", s.Repo, "kind", s.Kind, "action", action)

	started := time.Now()
	var err error
	switch action {
	case ActionFix:
		if skip := o.selfCausedOrStale(ctx, s); skip != "" {
			action = ActionNoop
			if s.Evidence == nil {
				s.Evidence = map[string]any{}
			}
			s.Evidence["skip_fix_sha"] = skip
			o.Log.Info("skipping Fix: signal is not a new failure",
				"repo", s.Repo, "sha", s.SHA, "reason", skip)
		} else if green, skipFix := o.tryCIRerun(ctx, &s); skipFix {
			action = ActionRerun
			if s.Evidence == nil {
				s.Evidence = map[string]any{}
			}
			s.Evidence["rerun"] = "success"
			err = nil
			_ = green
		} else if skip := o.claimFixSHA(s); skip != "" {
			action = ActionNoop
			if s.Evidence == nil {
				s.Evidence = map[string]any{}
			}
			s.Evidence["skip_fix_sha"] = skip
			o.Log.Info("skipping duplicate Fix for SHA",
				"repo", s.Repo, "sha", s.SHA, "reason", skip)
		} else {
			var res FixResult
			res, err = o.Dispatcher.Fix(ctx, s)
			o.finishFixSHA(s, res, err)
		}
	case ActionRevert:
		err = o.Dispatcher.Revert(ctx, s)
	case ActionPromote:
		err = o.Dispatcher.Promote(ctx, s)
	case ActionNoop:
		// nothing to do
	}
	if err != nil {
		o.Log.Error("dispatch failed", "action", action, "repo", s.Repo, "error", err)
		// The audit store gets the error as a column; BACKLOG.md only
		// gets evidence, so a failed Promote or Revert used to read there
		// exactly like one that worked.
		if s.Evidence == nil {
			s.Evidence = map[string]any{}
		}
		if _, has := s.Evidence["error"]; !has {
			s.Evidence["error"] = truncateErr(err.Error(), 300)
		}
	}

	if recErr := o.Backlog.Record(s.Repo, string(action), s.Evidence); recErr != nil {
		o.Log.Error("backlog write failed", "error", recErr)
	}

	if o.Audit != nil {
		if auditErr := o.Audit(s, action, err, started); auditErr != nil {
			o.Log.Error("audit write failed", "error", auditErr)
		}
	}

	if suppress == "root_cause" && o.Fleet.PatientZero {
		o.enqueuePatientZero(s)
	}
}

// enqueuePatientZero emits one Fix-triggering signal per upstream named
// in evidence (issue #4). Once per upstream while that upstream stays red.
func (o *Orchestrator) enqueuePatientZero(leaf Signal) {
	ups, _ := leaf.Evidence["upstream"].(string)
	for _, up := range strings.Split(ups, ",") {
		up = strings.TrimSpace(up)
		if up == "" {
			continue
		}
		if !o.markPatientZero(up) {
			continue
		}
		sig := Signal{
			Source: SourceCI,
			Repo:   up,
			Kind:   KindFail,
			Evidence: map[string]any{
				"patient_zero": true,
				"from_leaf":    leaf.Repo,
				"reason":       "upstream of " + leaf.Repo + " breaching (patient-zero)",
			},
			At: time.Now().UTC(),
		}
		select {
		case o.Signals <- sig:
			o.Log.Info("patient-zero: enqueued Fix for upstream", "upstream", up, "leaf", leaf.Repo)
		default:
			o.Log.Warn("patient-zero: signals full; dropped", "upstream", up)
			o.clearPatientZero(up)
		}
	}
}

func (o *Orchestrator) markPatientZero(repo string) bool {
	o.pzMu.Lock()
	defer o.pzMu.Unlock()
	if o.patientZeroFired == nil {
		o.patientZeroFired = map[string]struct{}{}
	}
	if _, ok := o.patientZeroFired[repo]; ok {
		return false
	}
	o.patientZeroFired[repo] = struct{}{}
	return true
}

func (o *Orchestrator) clearPatientZero(repo string) {
	o.pzMu.Lock()
	defer o.pzMu.Unlock()
	delete(o.patientZeroFired, repo)
}

// tryCIRerun runs the flake ladder once per run_url. Returns skipFix=true
// when the rerun went green (caller must not invoke Runner).
func (o *Orchestrator) tryCIRerun(ctx context.Context, s *Signal) (green bool, skipFix bool) {
	if o.RerunCI == nil || s.Source != SourceCI {
		return false, false
	}
	runURL, _ := s.Evidence["run_url"].(string)
	if runURL == "" {
		return false, false
	}
	o.reranMu.Lock()
	if o.reran == nil {
		o.reran = map[string]int{}
	}
	if _, seen := o.reran[runURL]; seen {
		o.reranMu.Unlock()
		return false, false
	}
	// The rerun we are about to request will complete as attempt+1.
	o.reran[runURL] = runAttempt(*s) + 1
	o.reranMu.Unlock()

	if s.Evidence != nil {
		s.Evidence["rerun_attempted"] = true
	}
	ok, err := o.RerunCI(ctx, *s)
	if err != nil {
		o.Log.Warn("ci rerun failed; falling through to Fix", "repo", s.Repo, "error", err)
		if s.Evidence != nil {
			s.Evidence["rerun_error"] = err.Error()
		}
		return false, false
	}
	if ok {
		o.Log.Info("ci rerun went green; skipping Fix", "repo", s.Repo, "run_url", runURL)
		return true, true
	}
	o.Log.Info("ci rerun still red; invoking Fix", "repo", s.Repo, "run_url", runURL)
	return false, false
}

func fixSHAKey(repo, sha string) string {
	return repo + "\x00" + sha
}

// claimFixSHA returns a skip reason when this repo+SHA already has a
// running or successful Fix. Empty SHA is never claimed (manual Fix).
func (o *Orchestrator) claimFixSHA(s Signal) string {
	sha := strings.TrimSpace(s.SHA)
	if sha == "" {
		return ""
	}
	key := fixSHAKey(s.Repo, sha)
	o.fixSHAMu.Lock()
	defer o.fixSHAMu.Unlock()
	if o.fixSHA == nil {
		o.fixSHA = map[string]fixSHAState{}
	}
	st := o.fixSHA[key]
	if st.inflight {
		return "inflight"
	}
	if st.ok {
		return "already_fixed"
	}
	st.inflight = true
	o.fixSHA[key] = st
	return ""
}

// finishFixSHA releases the claim and records whether this SHA is now
// fixed. st.ok means "a fix was delivered for this SHA", not "Fix
// returned no error": a run that committed nothing left the commit
// exactly as broken as it found it, so it must stay retryable (#34).
// A delivered Fix still latches ok, which is what keeps a flake-ladder
// workflow_run for the same commit from opening a second PR (d75b2c2).
func (o *Orchestrator) finishFixSHA(s Signal, res FixResult, err error) {
	sha := strings.TrimSpace(s.SHA)
	if sha == "" {
		return
	}
	key := fixSHAKey(s.Repo, sha)
	o.fixSHAMu.Lock()
	defer o.fixSHAMu.Unlock()
	if o.fixSHA == nil {
		return
	}
	st := o.fixSHA[key]
	st.inflight = false
	if err == nil && res.Delivered {
		st.ok = true
	}
	o.fixSHA[key] = st
	o.ownMu.Lock()
	if o.ownSHA == nil {
		o.ownSHA = map[string]struct{}{}
	}
	for _, pushed := range res.PushedSHAs {
		if pushed = strings.TrimSpace(pushed); pushed != "" {
			o.ownSHA[fixSHAKey(s.Repo, pushed)] = struct{}{}
		}
	}
	o.ownMu.Unlock()
}

// runAttempt is the workflow_run's run_attempt from the evidence, 1 when
// the payload did not say.
func runAttempt(s Signal) int {
	switch v := s.Evidence["run_attempt"].(type) {
	case int:
		if v > 0 {
			return v
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return 1
}

// selfCausedOrStale returns a skip reason when a CI fail signal is not
// news: it is the completion of a rerun this daemon requested, a run of
// a commit this daemon pushed, or a run of a commit that is no longer
// the branch tip. Each of those produced a duplicate Fix in the field —
// the last one pushing a commit onto a branch that was already green.
// Empty SHA (manual Fix) is never skipped.
func (o *Orchestrator) selfCausedOrStale(ctx context.Context, s Signal) string {
	if s.Source != SourceCI {
		return ""
	}
	sha := strings.TrimSpace(s.SHA)
	if sha == "" {
		return ""
	}
	o.ownMu.Lock()
	_, own := o.ownSHA[fixSHAKey(s.Repo, sha)]
	o.ownMu.Unlock()
	if own {
		return "own_push"
	}
	if runURL, _ := s.Evidence["run_url"].(string); runURL != "" {
		o.reranMu.Lock()
		expected, reran := o.reran[runURL]
		o.reranMu.Unlock()
		if reran && runAttempt(s) >= expected {
			return "rerun_echo"
		}
	}
	if o.TipSHA != nil {
		tip, err := o.TipSHA(ctx, s.Repo)
		if err != nil {
			o.Log.Warn("cannot read branch tip; treating signal as current", "repo", s.Repo, "error", err)
		} else if tip != "" && !strings.HasPrefix(tip, sha) && !strings.HasPrefix(sha, tip) {
			return "superseded"
		}
	}
	return ""
}

// truncateErr bounds an error message for an evidence field.
func truncateErr(msg string, n int) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) <= n {
		return msg
	}
	return msg[:n] + "…"
}
