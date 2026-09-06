package subagent

import (
	"encoding/json"
	"strings"
)

// VerdictKey is the JSON field a Fix agent must emit on its last line so
// xdlc can tell "I fixed it" apart from "I gave up and wrote a BACKLOG
// note". Both exit 0, so the exit code cannot distinguish them and the
// retry ladder would otherwise re-run an agent that already said it was
// blocked. Keep the name distinctive — ParseVerdict finds the block by
// searching for this literal anywhere in the CLI's stdout, including
// nested inside a provider's own JSON envelope.
const VerdictKey = "xdlc_outcome"

// Outcome is the Fix agent's own report of how its run ended.
type Outcome string

// The Outcome values a Fix agent may report.
const (
	// OutcomeFixed: the agent believes it fixed the failure and pushed.
	OutcomeFixed Outcome = "fixed"
	// OutcomeGaveUp: not fixable from this repo alone; BACKLOG note left.
	OutcomeGaveUp Outcome = "gave_up"
	// OutcomeNeedsHuman: a fix exists but needs a human decision
	// (ambiguous intent, risky migration, credentials).
	OutcomeNeedsHuman Outcome = "needs_human"
	// OutcomeUnknown: no verdict line found. Older agent, truncated
	// output, or a provider that swallowed stdout. Treated as "maybe
	// fixed" — the gate re-check is what actually decides.
	OutcomeUnknown Outcome = ""
)

// Verdict is the parsed agent self-report.
type Verdict struct {
	Outcome Outcome `json:"xdlc_outcome"`
	Summary string  `json:"summary"`
}

// Found reports whether the agent actually emitted a verdict.
func (v Verdict) Found() bool { return v.Outcome != OutcomeUnknown }

// Retryable reports whether re-running the agent could plausibly help.
// An agent that said gave_up or needs_human has already concluded the
// failure is out of its reach; feeding it the same red gate again burns
// tokens to reach the same answer. Unknown stays retryable because the
// absence of a verdict says nothing about the fix.
func (v Verdict) Retryable() bool {
	switch v.Outcome {
	case OutcomeGaveUp, OutcomeNeedsHuman:
		return false
	default:
		return true
	}
}

// Escalation maps a terminal verdict to the evidence["escalate"] value
// an operator sees in the console. "" when the verdict is not terminal.
func (v Verdict) Escalation() string {
	switch v.Outcome {
	case OutcomeGaveUp:
		return "agent_gave_up"
	case OutcomeNeedsHuman:
		return "agent_needs_human"
	default:
		return ""
	}
}

// maxVerdictSummary caps the summary carried into evidence, LESSONS.md
// and session meta. Agents occasionally emit a paragraph.
const maxVerdictSummary = 400

// ParseVerdict extracts the agent's verdict from CLI stdout.
//
// The block is located by searching backwards for VerdictKey (the last
// one wins — an agent that reconsiders mid-run leaves both), then
// expanding to the enclosing JSON object. Two shapes are handled: the
// object emitted directly on stdout, and the same object embedded in a
// provider envelope's string field (Claude Code's `--output-format
// json` puts the agent's whole answer inside "result", escaping the
// quotes), which is retried after unescaping.
//
// A missing or malformed verdict yields OutcomeUnknown, never an error:
// the verdict is an optimization for the retry ladder, and a Fix must
// never fail because an agent skipped the last line.
func ParseVerdict(stdout string) Verdict {
	for end := len(stdout); end > 0; {
		i := strings.LastIndex(stdout[:end], VerdictKey)
		if i < 0 {
			return Verdict{}
		}
		end = i
		obj, ok := enclosingObject(stdout, i)
		if !ok {
			continue
		}
		if v, ok := decodeVerdict(obj); ok {
			return v
		}
		// Embedded in a provider envelope: \"xdlc_outcome\" inside a
		// JSON string. Unescape and retry.
		if v, ok := decodeVerdict(unescapeJSONString(obj)); ok {
			return v
		}
	}
	return Verdict{}
}

// decodeVerdict unmarshals one candidate object and normalizes it.
func decodeVerdict(obj string) (Verdict, bool) {
	var v Verdict
	if err := json.Unmarshal([]byte(obj), &v); err != nil {
		return Verdict{}, false
	}
	switch Outcome(strings.ToLower(strings.TrimSpace(string(v.Outcome)))) {
	case OutcomeFixed:
		v.Outcome = OutcomeFixed
	case OutcomeGaveUp:
		v.Outcome = OutcomeGaveUp
	case OutcomeNeedsHuman:
		v.Outcome = OutcomeNeedsHuman
	default:
		// A JSON object carrying the key with an unrecognized value is
		// not a verdict; keep scanning rather than reporting Unknown.
		return Verdict{}, false
	}
	v.Summary = strings.Join(strings.Fields(v.Summary), " ")
	if len(v.Summary) > maxVerdictSummary {
		v.Summary = v.Summary[:maxVerdictSummary] + "…"
	}
	return v, true
}

// enclosingObject returns the smallest brace-balanced JSON object in s
// that contains index at. Quote-aware so a brace inside a string value
// does not throw the depth count off.
func enclosingObject(s string, at int) (string, bool) {
	start := -1
	for i := at; i >= 0; i-- {
		if s[i] == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// brace inside a string literal: not structural
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// unescapeJSONString turns \" \\ \n \t back into their literal forms,
// enough to recover an object that was embedded in a JSON string field.
func unescapeJSONString(s string) string {
	r := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t", `\r`, "\r")
	return r.Replace(s)
}
