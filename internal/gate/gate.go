// Package gate defines the pluggable Gate abstraction — the "3 gates" in
// the xdlc loop (CI, DEV smoke/e2e, PROD health). New gates are
// added by implementing this interface, not by editing the orchestrator.
package gate

import (
	"context"
	"time"
)

// TriggerKind says when a Gate's Check should run.
type TriggerKind string

// The TriggerKind values a Gate's Trigger() can return.
const (
	OnPush     TriggerKind = "on_push"    // fires on repo push (e.g. CI gate)
	OnSync     TriggerKind = "on_sync"    // fires after a GitOps sync (e.g. DEV smoke gate)
	Continuous TriggerKind = "continuous" // polled on an interval (e.g. PROD health gate)
)

// Status is the pass/fail outcome of a single Check call.
type Status string

// The Status values a Gate's Check() can return.
const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
)

// Result is what a Gate reports back after Check.
type Result struct {
	Status   Status
	Evidence map[string]any
	At       time.Time
}

// Gate is implemented by every check the orchestrator can run: CI status,
// DEV smoke/e2e, PROD health (p95/error-rate), or anything a fork adds.
type Gate interface {
	// Name is the config key this gate is referenced by (e.g. "ci", "dev-smoke").
	Name() string
	Trigger() TriggerKind
	Check(ctx context.Context, repo string) (Result, error)
}
