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

// Dispatcher performs the side-effecting part of an Action: running a
// subagent, reverting, or promoting. Kept as an interface so loop.go
// stays testable without shelling out to git/claude for real.
type Dispatcher interface {
	Fix(ctx context.Context, s Signal) error
	Revert(ctx context.Context, s Signal) error
	Promote(ctx context.Context, s Signal) error
}

// RerunCIFunc tries GitHub rerun-failed-jobs for a CI fail signal.
// Returns green=true when the rerun concludes success (skip Fix).
// nil disables the ladder (issue #3).
type RerunCIFunc func(ctx context.Context, s Signal) (green bool, err error)

// AuditFunc persists a structured record of one signal+action, e.g. to
// the bbolt-backed internal/store for `xdlc-agent history`. Optional —
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
	// reran tracks run_urls already attempted this process lifetime.
	reranMu sync.Mutex
	reran   map[string]struct{}

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
		reran:      map[string]struct{}{},
	}
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
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ch <- s:
			}
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
		if green, skipFix := o.tryCIRerun(ctx, &s); skipFix {
			action = ActionRerun
			if s.Evidence == nil {
				s.Evidence = map[string]any{}
			}
			s.Evidence["rerun"] = "success"
			err = nil
			_ = green
		} else {
			err = o.Dispatcher.Fix(ctx, s)
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
		o.reran = map[string]struct{}{}
	}
	if _, seen := o.reran[runURL]; seen {
		o.reranMu.Unlock()
		return false, false
	}
	o.reran[runURL] = struct{}{}
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
