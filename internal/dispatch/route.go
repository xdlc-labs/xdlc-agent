package dispatch

import (
	"strings"

	"github.com/xdlc-labs/xdlc-agent/internal/store"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// Relative cost weights for route: cheapest (lower = preferred).
var providerCostWeight = map[string]float64{
	string(subagent.ProviderClaude): 1.0,
	string(subagent.ProviderCodex):  0.7,
	string(subagent.ProviderCursor): 0.8,
}

// ProviderStats is recent Fix outcome for one provider (from audit).
type ProviderStats struct {
	Fixes   int
	Success int // completed fixes not followed by revert before next fix
}

// PickProvider chooses which coding-agent provider to use for Fix.
// route "cheapest" among candidates with success rate >= minSuccess (or
// no history); otherwise returns fallback.
func PickProvider(route, fallback string, candidates []string, minSuccess float64, stats map[string]ProviderStats) string {
	if route != "cheapest" || len(candidates) == 0 {
		if fallback != "" {
			return fallback
		}
		if len(candidates) > 0 {
			return candidates[0]
		}
		return string(subagent.ProviderClaude)
	}
	if minSuccess <= 0 {
		minSuccess = 0.5
	}
	best := ""
	bestScore := 1e99
	for _, p := range candidates {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		st := stats[p]
		if st.Fixes > 0 {
			rate := float64(st.Success) / float64(st.Fixes)
			if rate < minSuccess {
				continue
			}
		}
		w := providerCostWeight[p]
		if w == 0 {
			w = 1.0
		}
		// Prefer cheaper; break ties by listing order (first wins when equal).
		if w < bestScore {
			bestScore = w
			best = p
		}
	}
	if best != "" {
		return best
	}
	if fallback != "" {
		return fallback
	}
	return candidates[0]
}

// StatsFromRecords builds per-provider Fix success from audit records.
// Only StatusOK (or empty legacy) Fix rows count; failed dispatches are
// ignored so route: cheapest does not credit a crashed subagent.
func StatsFromRecords(records []store.Record, providers []string, fallbackProvider string) map[string]ProviderStats {
	out := map[string]ProviderStats{}
	for _, p := range providers {
		out[p] = ProviderStats{}
	}
	if fallbackProvider != "" {
		if _, ok := out[fallbackProvider]; !ok {
			out[fallbackProvider] = ProviderStats{}
		}
	}

	type fixEvt struct {
		provider string
		i        int
	}
	var fixes []fixEvt
	actions := make([]string, len(records))
	for i, r := range records {
		actions[i] = r.Action
		if r.Action != "fix" || !r.Succeeded() {
			continue
		}
		p := r.AgentProvider
		if p == "" {
			if r.Evidence != nil {
				if v, ok := r.Evidence["agent_provider"].(string); ok {
					p = v
				}
			}
		}
		if p == "" {
			p = fallbackProvider
		}
		if p == "" && len(providers) > 0 {
			p = providers[0]
		}
		fixes = append(fixes, fixEvt{provider: p, i: i})
	}

	for _, f := range fixes {
		st := out[f.provider]
		st.Fixes++
		reverted := false
		for j := f.i + 1; j < len(actions); j++ {
			if actions[j] == "fix" {
				break
			}
			if actions[j] == "revert" {
				reverted = true
				break
			}
		}
		if !reverted {
			st.Success++
		}
		out[f.provider] = st
	}
	return out
}

// StatsFromActions is a thin test helper: all fixes credited to fallbackProvider.
func StatsFromActions(actions []string, providers []string, fallbackProvider string) map[string]ProviderStats {
	recs := make([]store.Record, len(actions))
	for i, a := range actions {
		recs[i] = store.Record{Action: a, Status: store.StatusOK, AgentProvider: fallbackProvider}
	}
	return StatsFromRecords(recs, providers, fallbackProvider)
}
