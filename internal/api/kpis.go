package api

import (
	"math"
	"net/http"
	"sort"

	"github.com/xdlc-labs/xdlc-agent/internal/store"
)

// handleKPIs aggregates Fix cost / outcome metrics from the audit
// store. Pure read over Audit.All — no new persistence.
func (s *Server) handleKPIs(w http.ResponseWriter, r *http.Request) {
	records, err := s.Audit.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, buildKPIs(records))
}

type repoKPI struct {
	Repo           string   `json:"repo"`
	Fixes          int      `json:"fixes"`
	Reverts        int      `json:"reverts"`
	Promotes       int      `json:"promotes"`
	TotalCostUSD   float64  `json:"total_cost_usd"`
	FixSuccessRate *float64 `json:"fix_success_rate"` // nil if no fixes
	DurationP50MS  *float64 `json:"duration_p50_ms,omitempty"`
	DurationP95MS  *float64 `json:"duration_p95_ms,omitempty"`
}

type kpiResponse struct {
	Totals repoKPI   `json:"totals"`
	Repos  []repoKPI `json:"repos"`
}

// kpiAcc is per-repo accumulator for buildKPIs / finishKPI.
type kpiAcc struct {
	fixes, reverts, promotes int
	cost                     float64
	durs                     []float64
	actions                  []string // chronological for success heuristic
}

func buildKPIs(records []store.Record) kpiResponse {
	sort.Slice(records, func(i, j int) bool { return records[i].At.Before(records[j].At) })

	byRepo := map[string]*kpiAcc{}
	totals := &kpiAcc{}

	for _, rec := range records {
		a := byRepo[rec.Repo]
		if a == nil {
			a = &kpiAcc{}
			byRepo[rec.Repo] = a
		}
		switch rec.Action {
		case "fix":
			if !rec.Succeeded() {
				continue // crashed Fix is not a completed fix for KPIs
			}
			a.fixes++
			totals.fixes++
			a.actions = append(a.actions, "fix")
			totals.actions = append(totals.actions, "fix")
			if v, ok := floatEvidence(rec.Evidence, "total_cost_usd"); ok {
				a.cost += v
				totals.cost += v
			}
			dur := float64(rec.DurationMS)
			if dur <= 0 {
				if v, ok := floatEvidence(rec.Evidence, "duration_ms"); ok {
					dur = v
				}
			}
			if dur > 0 {
				a.durs = append(a.durs, dur)
				totals.durs = append(totals.durs, dur)
			}
		case "revert":
			if !rec.Succeeded() {
				continue
			}
			a.reverts++
			totals.reverts++
			a.actions = append(a.actions, "revert")
			totals.actions = append(totals.actions, "revert")
		case "promote":
			if !rec.Succeeded() {
				continue
			}
			a.promotes++
			totals.promotes++
		}
	}

	out := kpiResponse{Totals: finishKPI("", totals), Repos: make([]repoKPI, 0, len(byRepo))}
	names := make([]string, 0, len(byRepo))
	for name := range byRepo {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Repos = append(out.Repos, finishKPI(name, byRepo[name]))
	}
	return out
}

func finishKPI(repo string, a *kpiAcc) repoKPI {
	k := repoKPI{
		Repo:         repo,
		Fixes:        a.fixes,
		Reverts:      a.reverts,
		Promotes:     a.promotes,
		TotalCostUSD: round4(a.cost),
	}
	if a.fixes > 0 {
		ok := fixSuccessRate(a.actions)
		k.FixSuccessRate = &ok
	}
	if p50, p95, ok := percentiles(a.durs); ok {
		k.DurationP50MS = &p50
		k.DurationP95MS = &p95
	}
	return k
}

// fixSuccessRate: share of fixes not followed by a revert before the next fix
// (same chronological action stream). Pure heuristic for console KPIs.
func fixSuccessRate(actions []string) float64 {
	var fixes, good int
	for i, a := range actions {
		if a != "fix" {
			continue
		}
		fixes++
		reverted := false
		for j := i + 1; j < len(actions); j++ {
			if actions[j] == "fix" {
				break
			}
			if actions[j] == "revert" {
				reverted = true
				break
			}
		}
		if !reverted {
			good++
		}
	}
	if fixes == 0 {
		return 0
	}
	return round4(float64(good) / float64(fixes))
}

func floatEvidence(ev map[string]any, key string) (float64, bool) {
	if ev == nil {
		return 0, false
	}
	v, ok := ev[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func percentiles(vals []float64) (p50, p95 float64, ok bool) {
	if len(vals) == 0 {
		return 0, 0, false
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	p50 = cp[percentileIndex(len(cp), 0.50)]
	p95 = cp[percentileIndex(len(cp), 0.95)]
	return p50, p95, true
}

func percentileIndex(n int, p float64) int {
	if n == 1 {
		return 0
	}
	i := int(math.Ceil(p*float64(n))) - 1
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
