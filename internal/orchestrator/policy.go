package orchestrator

import (
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// FleetPolicy is the anti-thrash / dependency-aware suppression policy.
// Zero FlapMaxCycles and CircuitBreachRatio disable those guards.
type FleetPolicy struct {
	FlapMaxCycles      int
	FlapWindow         time.Duration
	CircuitBreachRatio float64
	RepoCount          int // configured repos; used for circuit ratio
	NotifyWebhookURL   string
	PatientZero        bool // issue #4: Fix upstream on root_cause suppress
}

// PromotePin is a min_tag requirement on a dependency repo (v2).
type PromotePin struct {
	Repo   string
	MinTag string
}

// RecentActionsFunc returns chronological action names (fix/revert/…)
// for repo since the given time. Nil → flap detection skipped.
type RecentActionsFunc func(repo string, since time.Time) ([]string, error)

// flapTransitions counts adjacent fix↔revert alternations in a
// chronological action list (other actions ignored).
func flapTransitions(actions []string) int {
	var seq []string
	for _, a := range actions {
		if a == string(ActionFix) || a == string(ActionRevert) {
			seq = append(seq, a)
		}
	}
	n := 0
	for i := 1; i < len(seq); i++ {
		if seq[i] != seq[i-1] {
			n++
		}
	}
	return n
}

// tagAtLeast reports whether have >= want using SemVer when both parse,
// otherwise exact string match.
func tagAtLeast(have, want string) bool {
	if want == "" {
		return true
	}
	if have == "" {
		return false
	}
	h, w := have, want
	if !strings.HasPrefix(h, "v") {
		h = "v" + h
	}
	if !strings.HasPrefix(w, "v") {
		w = "v" + w
	}
	if semver.IsValid(h) && semver.IsValid(w) {
		return semver.Compare(h, w) >= 0
	}
	return have == want
}

// suppressReason returns non-empty escalate reason when action should
// become Noop. Mutates evidence via ensureEvidence when suppressing.
func (o *Orchestrator) suppressReason(s *Signal, action Action) string {
	if action == ActionNoop {
		return ""
	}

	// C4: structural smell → backlog, never burn a Runner call.
	if action == ActionFix {
		if hit := structuralMatch(*s); hit != "" {
			ensureEvidence(s)
			s.Evidence["escalate"] = "structural"
			s.Evidence["structural_match"] = hit
			return "structural"
		}
	}

	if o.Fleet.CircuitBreachRatio > 0 && o.Fleet.RepoCount > 0 &&
		(action == ActionFix || action == ActionRevert || action == ActionPromote) {
		breaching := o.breachCount()
		ratio := float64(breaching) / float64(o.Fleet.RepoCount)
		if ratio >= o.Fleet.CircuitBreachRatio {
			ensureEvidence(s)
			s.Evidence["escalate"] = "circuit"
			s.Evidence["breach_ratio"] = ratio
			s.Evidence["breaching"] = breaching
			return "circuit"
		}
	}

	if action == ActionRevert {
		if ups := o.breachingDeps(s.Repo); len(ups) > 0 {
			ensureEvidence(s)
			s.Evidence["escalate"] = "root_cause"
			s.Evidence["upstream"] = strings.Join(ups, ",")
			return "root_cause"
		}
	}

	if action == ActionPromote {
		if ups := o.breachingDeps(s.Repo); len(ups) > 0 {
			ensureEvidence(s)
			s.Evidence["escalate"] = "deps_unhealthy"
			s.Evidence["upstream"] = strings.Join(ups, ",")
			return "deps_unhealthy"
		}
		if bad := o.failingPins(s.Repo); len(bad) > 0 {
			ensureEvidence(s)
			s.Evidence["escalate"] = "deps_pin"
			s.Evidence["pin_fail"] = strings.Join(bad, ",")
			return "deps_pin"
		}
	}

	if o.Fleet.FlapMaxCycles > 0 && o.RecentActions != nil &&
		(action == ActionFix || action == ActionRevert) {
		window := o.Fleet.FlapWindow
		if window <= 0 {
			window = 2 * time.Hour
		}
		actions, err := o.RecentActions(s.Repo, time.Now().Add(-window))
		if err == nil && flapTransitions(actions) >= o.Fleet.FlapMaxCycles {
			ensureEvidence(s)
			s.Evidence["escalate"] = "flap"
			s.Evidence["flap_transitions"] = flapTransitions(actions)
			return "flap"
		}
	}

	return ""
}

func (o *Orchestrator) failingPins(repo string) []string {
	pins := o.PromotePins[repo]
	if len(pins) == 0 || o.ProdTag == nil {
		return nil
	}
	var bad []string
	for _, p := range pins {
		tag, err := o.ProdTag(p.Repo)
		if err != nil || !tagAtLeast(tag, p.MinTag) {
			bad = append(bad, p.Repo+"@"+p.MinTag)
		}
	}
	return bad
}

func ensureEvidence(s *Signal) {
	if s.Evidence == nil {
		s.Evidence = map[string]any{}
	}
}

func (o *Orchestrator) updateBreach(s Signal) {
	if s.Source != SourceProdHealth {
		return
	}
	o.breachMu.Lock()
	defer o.breachMu.Unlock()
	if o.breach == nil {
		o.breach = map[string]bool{}
	}
	switch s.Kind {
	case KindBreach:
		o.breach[s.Repo] = true
	case KindPass:
		o.breach[s.Repo] = false
		o.clearPatientZero(s.Repo)
	}
}

func (o *Orchestrator) breachCount() int {
	o.breachMu.Lock()
	defer o.breachMu.Unlock()
	n := 0
	for _, b := range o.breach {
		if b {
			n++
		}
	}
	return n
}

func (o *Orchestrator) breachingDeps(repo string) []string {
	deps := o.RepoDeps[repo]
	if len(deps) == 0 {
		return nil
	}
	o.breachMu.Lock()
	defer o.breachMu.Unlock()
	var ups []string
	for _, d := range deps {
		if o.breach[d] {
			ups = append(ups, d)
		}
	}
	return ups
}
