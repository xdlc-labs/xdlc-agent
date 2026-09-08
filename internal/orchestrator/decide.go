package orchestrator

// Action is what the orchestrator does in response to a Signal.
type Action string

// The Action values Decide can return.
const (
	ActionNoop    Action = "noop"    // pass signal, nothing to do
	ActionFix     Action = "fix"     // dispatch subagent to fix-forward
	ActionRevert  Action = "revert"  // git revert last change, push
	ActionPromote Action = "promote" // fast-forward develop -> main
	ActionRerun   Action = "rerun"   // GitHub rerun-failed-jobs (flake ladder)
)

// Decide maps a Signal to the Action the orchestrator should take.
// This is intentionally a pure function — easy to unit test, easy for
// forks to swap out for their own policy.
func Decide(s Signal) Action {
	// A gate that could not run reported no verdict, so there is
	// nothing to act on. This is checked before the Source switch, not
	// inside each arm, so a new Source can never accidentally route
	// "ArgoCD was unreachable" to Fix and pay for a coding-agent run
	// against a repo that is not broken (issue #45). The record an
	// operator sees is written by handle(), not by an Action.
	if s.Kind == KindBlocked {
		return ActionNoop
	}

	switch s.Source {
	case SourceCI:
		if s.Kind == KindFail {
			return ActionFix
		}
		return ActionNoop

	case SourceDevGate:
		if s.Kind == KindFail {
			return ActionFix
		}
		if s.Kind == KindPass {
			return ActionPromote
		}
		return ActionNoop

	case SourceProdHealth:
		if s.Kind == KindBreach {
			return ActionRevert
		}
		return ActionNoop

	default:
		return ActionNoop
	}
}
