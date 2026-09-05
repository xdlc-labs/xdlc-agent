package orchestrator

// Action is what the orchestrator does in response to a Signal.
type Action string

// The Action values Decide can return.
const (
	ActionNoop    Action = "noop"    // pass signal, nothing to do
	ActionFix     Action = "fix"     // dispatch subagent to fix-forward
	ActionRevert  Action = "revert"  // git revert last change, push
	ActionPromote Action = "promote" // fast-forward develop -> main
)

// Decide maps a Signal to the Action the orchestrator should take.
// This is intentionally a pure function — easy to unit test, easy for
// forks to swap out for their own policy.
func Decide(s Signal) Action {
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
