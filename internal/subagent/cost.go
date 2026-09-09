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
		// Cache tokens are billed and are usually the bulk of a coding
		// agent's input: one real Fix reported 514 uncached input tokens
		// beside 49,801 cache writes and 843,579 cache reads. Recording
		// only input_tokens put 0.06% of the billed input in the audit
		// row, so the tokens could not be reconciled with the dollar
		// figure next to them.
		CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// ParseCost extracts Claude-style cost/token fields from CLI stdout.
// Malformed or non-JSON stdout → nil (no-op). Best-effort only.
//
// Three stdout shapes reach here. One JSON object (`--output-format
// json`); that object with git or wrapper chatter around it; and a
// stream of events, one JSON object per line (`--output-format
// stream-json`, which the stall watchdog switches on), where the
// billing totals live on the final `result` event and every earlier
// line has no cost fields at all. Whole-document parsing fails outright
// on the third — several objects are not one document — so a stream
// would have recorded a Fix as costing nothing.
func ParseCost(stdout string) map[string]any {
	data := []byte(strings.TrimSpace(stdout))
	if len(data) == 0 {
		return nil
	}
	var parsed cliCostJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		if fields := parseCostLines(data); fields != nil {
			return fields
		}
		// ponytail: strip prefix/suffix noise (git chatter) then retry once
		i, j := bytes.IndexByte(data, '{'), bytes.LastIndexByte(data, '}')
		if i < 0 || j <= i {
			return nil
		}
		if err := json.Unmarshal(data[i:j+1], &parsed); err != nil {
			return nil
		}
	}
	return costFieldsOf(parsed)
}

// parseCostLines reads a JSON-lines stream backwards for the last event
// that actually carries billing fields. Backwards because the totals
// are emitted last, and because a stream's early lines parse fine but
// hold nothing — taking the first parseable line would report zero.
func parseCostLines(data []byte) map[string]any {
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var parsed cliCostJSON
		if err := json.Unmarshal(line, &parsed); err != nil {
			continue
		}
		if fields := costFieldsOf(parsed); fields != nil {
			return fields
		}
	}
	return nil
}

// costFieldsOf renders one parsed envelope as the audit-row keys. Nil
// when the envelope carried no cost or usage at all.
func costFieldsOf(parsed cliCostJSON) map[string]any {
	out := make(map[string]any, 4)
	if parsed.TotalCostUSD != nil {
		out["total_cost_usd"] = *parsed.TotalCostUSD
	}
	if parsed.DurationMS != nil {
		out["duration_ms"] = *parsed.DurationMS
	}
	if u := parsed.Usage; u != nil {
		if u.InputTokens != nil {
			out["input_tokens"] = *u.InputTokens
		}
		if u.OutputTokens != nil {
			out["output_tokens"] = *u.OutputTokens
		}
		if u.CacheCreationInputTokens != nil {
			out["cache_write_tokens"] = *u.CacheCreationInputTokens
		}
		if u.CacheReadInputTokens != nil {
			out["cache_read_tokens"] = *u.CacheReadInputTokens
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
