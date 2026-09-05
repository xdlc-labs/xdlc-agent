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

// MergeCost writes ParseCost fields into dst. No-op if dst is nil or
// stdout has no cost JSON.
func MergeCost(dst map[string]any, stdout string) {
	if dst == nil {
		return
	}
	for k, v := range ParseCost(stdout) {
		dst[k] = v
	}
}
