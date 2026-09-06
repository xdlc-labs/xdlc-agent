package subagent

import (
	"bytes"
	"encoding/json"
	"strings"
)

// cliCostJSON is the Claude Code `--output-format json` cost/usage shape.
// Other providers may emit a subset or nothing — ParseCost is best-effort.
type cliCostJSON struct {
	TotalCostUSD *float64 `json:"total_cost_usd"`
	DurationMS   *int64   `json:"duration_ms"`
	Usage        *struct {
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
	} `json:"usage"`
}

// ParseCost extracts Claude-style cost/token fields from CLI stdout JSON.
// Malformed or non-JSON stdout → nil (no-op). Best-effort only.
func ParseCost(stdout string) map[string]any {
	data := []byte(strings.TrimSpace(stdout))
	if len(data) == 0 {
		return nil
	}
	var parsed cliCostJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		// ponytail: strip prefix/suffix noise (git chatter) then retry once
		i, j := bytes.IndexByte(data, '{'), bytes.LastIndexByte(data, '}')
		if i < 0 || j <= i {
			return nil
		}
		if err := json.Unmarshal(data[i:j+1], &parsed); err != nil {
			return nil
		}
	}
	out := make(map[string]any, 4)
	if parsed.TotalCostUSD != nil {
		out["total_cost_usd"] = *parsed.TotalCostUSD
	}
	if parsed.DurationMS != nil {
		out["duration_ms"] = *parsed.DurationMS
	}
	if parsed.Usage != nil {
		if parsed.Usage.InputTokens != nil {
			out["input_tokens"] = *parsed.Usage.InputTokens
		}
		if parsed.Usage.OutputTokens != nil {
			out["output_tokens"] = *parsed.Usage.OutputTokens
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MergeCost writes ParseCost fields into dst, replacing any already
// there. No-op if dst is nil or stdout has no cost JSON.
//
// Use AddCost instead when more than one agent run feeds one audit row —
// a diagnose pass followed by a patch pass, or a retried Fix — otherwise
// the last run's numbers stand in for the whole thing.
func MergeCost(dst map[string]any, stdout string) {
	if dst == nil {
		return
	}
	for k, v := range ParseCost(stdout) {
		dst[k] = v
	}
}

// AddCost folds one agent run's cost and usage into a running total on
// dst. A single Fix can spend several runs (agent.fix_plan adds a
// diagnose pass; agent.fix_attempts can send the agent back in), and all
// of them are billed. Overwriting would report a three-attempt Fix at the
// price of its last attempt, which is exactly the number an operator
// deciding whether the ladder is worth it must not be shown.
//
// Unknown keys and non-numeric values are replaced rather than summed.
func AddCost(dst map[string]any, stdout string) {
	if dst == nil {
		return
	}
	for k, v := range ParseCost(stdout) {
		prev, ok := dst[k]
		if !ok {
			dst[k] = v
			continue
		}
		dst[k] = addNumeric(prev, v)
	}
}

// addNumeric sums two cost values, preserving int64 where both sides are
// integral so token counts and durations do not turn into floats in the
// audit record. Falls back to the newer value when either side is not a
// number ParseCost could have produced.
func addNumeric(a, b any) any {
	ai, aok := a.(int64)
	bi, bok := b.(int64)
	if aok && bok {
		return ai + bi
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af + bf
	}
	return b
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}
