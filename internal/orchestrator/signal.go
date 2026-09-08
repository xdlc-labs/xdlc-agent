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
	// KindBlocked is "the gate could not run", which is not a verdict
	// about the deployed software at all. A dead `argocd`, an expired
	// session or a typo'd argocd_app leaves the gate with nothing to
	// report, and before issue #45 that produced no Signal at all: the
	// webhook answered 204, the poller logged, and Promote simply never
	// fired again with no audit row and no BACKLOG.md line to say why.
	//
	// It is deliberately *not* KindFail. Decide maps a dev-gate fail to
	// ActionFix, so a typo'd app name would spend a coding-agent run
	// trying to fix a repo that is not broken. Every Source maps
	// KindBlocked to ActionNoop; the visibility comes from the record
	// the orchestrator writes on the way past — BACKLOG.md, the audit
	// store, and from there `xdlc history`, /api/events and the console
	// Activity feed, all tagged escalate=gate_unavailable.
	KindBlocked Kind = "blocked"
)

// EscalateGateUnavailable is the evidence["escalate"] value on a
// KindBlocked Signal, following the same convention as the fleet
// policy's structural / circuit / root_cause / deps_pin reasons: the
// one token an operator greps a BACKLOG.md or an Activity row for.
const EscalateGateUnavailable = "gate_unavailable"

// Blocked builds the Signal a gate runner emits when Gate.Check could
// not produce a verdict. gateName and err are recorded as evidence so
// the reason (not just the fact) reaches BACKLOG.md and the audit row;
// sha is the commit the missing verdict would have applied to, and may
// be empty.
func Blocked(source Source, repo, gateName, sha string, err error) Signal {
	reason := "gate check failed"
	if err != nil {
		reason = err.Error()
	}
	return Signal{
		Source: source,
		Repo:   repo,
		Kind:   KindBlocked,
		SHA:    sha,
		At:     time.Now().UTC(),
		Evidence: map[string]any{
			"escalate":   EscalateGateUnavailable,
			"gate":       gateName,
			"gate_error": reason,
		},
	}
}

// BlockedReason reports whether s is a KindBlocked Signal and, if so,
// why the gate could not run. Callers use it to record the signal as an
// error rather than a successful noop — see the daemon's Audit func,
// which sets store.StatusError so the row reads ok=false in
// `xdlc history` and the console instead of looking like a clean pass.
func BlockedReason(s Signal) (string, bool) {
	if s.Kind != KindBlocked {
		return "", false
	}
	if s.Evidence != nil {
		if reason, _ := s.Evidence["gate_error"].(string); reason != "" {
			return reason, true
		}
	}
	return "gate could not run", true
}

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

	// OperatorAgentProvider overrides config agent.provider for this Fix
	// only (console header). Empty = use dispatcher default / route.
	OperatorAgentProvider string
	// OperatorAgentKey is request-scoped API key for the coding-agent
	// subprocess (console header). Never copy into Evidence / audit /
	// backlog / logs — Dispatch consumes and clears it.
	OperatorAgentKey string
	// OperatorInstructions is optional free text an operator sends with
	// a manual Fix ("the flake is in the seed data, not the test") —
	// the thing you would type into a coding agent yourself. It joins
	// the prompt's *trusted* block, after the repo's rules, because it
	// comes from an authenticated operator rather than gate evidence.
	// Empty for every automatic Fix.
	OperatorInstructions string
}

// AuditSource is the source string written to the history store.
// Manual Actions keep SourceCI / SourceDevGate / SourceProdHealth so
// Decide() still maps to Fix / Promote / Revert, but operators must see
// them as daemon, not GitHub / Argo / Prometheus.
func AuditSource(s Signal) string {
	if s.Evidence != nil {
		if m, ok := s.Evidence["manual"].(bool); ok && m {
			return "daemon"
		}
		if v, _ := s.Evidence["via"].(string); v == "api" {
			return "daemon"
		}
	}
	return string(s.Source)
}
