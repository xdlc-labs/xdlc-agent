// Package orchestrator is the single loop: read a Signal, Decide an
// Action, dispatch it. See loop.go for the loop itself and decide.go
// for the (pure, easily-tested) policy.
package orchestrator

import (
	"context"
	"log/slog"
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
	if reason := o.suppressReason(&s, action); reason != "" {
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
		err = o.Dispatcher.Fix(ctx, s)
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
}
