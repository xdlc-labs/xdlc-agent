package orchestrator

import "time"

// Source identifies which subsystem raised a Signal.
type Source string

// The Source values a Signal can carry.
const (
	SourceCI         Source = "ci"
	SourceDevGate    Source = "dev-gate"
	SourceProdHealth Source = "prod-health"
)

// Kind is the outcome the Source is reporting.
type Kind string

// The Kind values a Signal can carry.
const (
	KindPass   Kind = "pass"
	KindFail   Kind = "fail"
	KindBreach Kind = "breach" // continuous threshold breach (prod-health)
)

// Signal is the unit of information flowing from gates back to the
// orchestrator loop. Every gate check, webhook delivery, or poll tick
// that produces an outcome becomes one Signal.
type Signal struct {
	Source   Source
	Repo     string
	Kind     Kind
	Evidence map[string]any
	At       time.Time
	// SHA is the commit this Signal's verdict applies to — the verified
	// workflow_run head_sha for SourceCI, the dev-branch tip that was
	// gated for SourceDevGate. It is the *identity of the artifact*, not
	// decoration: a Promote must push this exact commit and refuse if the
	// dev branch has moved off it since the gate passed, so a commit that
	// landed after the smoke probe can never reach prod untested (see
	// promote.FastForward and dispatch.Dispatcher.Promote).
	//
	// Empty means "not pinned": manual actions enqueued by an operator
	// via /api/actions, and gates that cannot attribute a commit. Those
	// promote the current dev tip, as before.
	SHA string
}
