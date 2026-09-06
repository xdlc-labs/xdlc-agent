package subagent

import "testing"

func TestParseVerdictPlainLine(t *testing.T) {
	out := "did some work\nmore work\n" +
		`{"xdlc_outcome": "fixed", "summary": "bumped the pinned go-github version"}`
	v := ParseVerdict(out)
	if v.Outcome != OutcomeFixed {
		t.Fatalf("outcome = %q, want fixed", v.Outcome)
	}
	if v.Summary != "bumped the pinned go-github version" {
		t.Fatalf("summary = %q", v.Summary)
	}
	if !v.Found() || !v.Retryable() {
		t.Fatal("fixed verdict should be found and retryable")
	}
}

// Claude Code's --output-format json wraps the agent's whole answer in a
// "result" string, escaping every quote inside it.
func TestParseVerdictInsideProviderEnvelope(t *testing.T) {
	out := `{"type":"result","total_cost_usd":0.31,"result":"I looked at the logs.\n` +
		`{\"xdlc_outcome\": \"gave_up\", \"summary\": \"failure is in the upstream image, not this repo\"}"}`
	v := ParseVerdict(out)
	if v.Outcome != OutcomeGaveUp {
		t.Fatalf("outcome = %q, want gave_up", v.Outcome)
	}
	if v.Summary != "failure is in the upstream image, not this repo" {
		t.Fatalf("summary = %q", v.Summary)
	}
	if v.Retryable() {
		t.Fatal("gave_up must not be retryable")
	}
	if v.Escalation() != "agent_gave_up" {
		t.Fatalf("escalation = %q", v.Escalation())
	}
}

func TestParseVerdictLastOneWins(t *testing.T) {
	out := `{"xdlc_outcome":"gave_up","summary":"first thought"}` + "\n" +
		"actually, I found it\n" +
		`{"xdlc_outcome":"fixed","summary":"second thought"}`
	if v := ParseVerdict(out); v.Summary != "second thought" {
		t.Fatalf("summary = %q, want the last verdict", v.Summary)
	}
}

// A malformed or unrecognized block must not stop an earlier valid one
// from being found — the scan keeps walking backwards.
func TestParseVerdictSkipsUnusableBlocks(t *testing.T) {
	out := `{"xdlc_outcome":"needs_human","summary":"migration needs a DBA"}` + "\n" +
		`{"xdlc_outcome": "maybe"}` + "\n" +
		`{"xdlc_outcome": not-json}`
	v := ParseVerdict(out)
	if v.Outcome != OutcomeNeedsHuman {
		t.Fatalf("outcome = %q, want needs_human", v.Outcome)
	}
	if v.Escalation() != "agent_needs_human" {
		t.Fatalf("escalation = %q", v.Escalation())
	}
}

func TestParseVerdictAbsent(t *testing.T) {
	v := ParseVerdict("I edited three files and pushed.\n")
	if v.Found() {
		t.Fatal("no verdict line should mean not found")
	}
	if v.Outcome != OutcomeUnknown {
		t.Fatalf("outcome = %q, want empty", v.Outcome)
	}
	// Unknown must stay retryable so agents that ignore the contract
	// behave exactly as they did before the verdict existed.
	if !v.Retryable() || v.Escalation() != "" {
		t.Fatal("unknown verdict must be retryable and non-escalating")
	}
}

func TestParseVerdictBraceInsideSummary(t *testing.T) {
	out := `{"xdlc_outcome":"fixed","summary":"replaced map[string]any{} literal"}`
	if v := ParseVerdict(out); v.Summary != "replaced map[string]any{} literal" {
		t.Fatalf("summary = %q", v.Summary)
	}
}

func TestParseVerdictTruncatesLongSummary(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "word "
	}
	out := `{"xdlc_outcome":"fixed","summary":"` + long + `"}`
	v := ParseVerdict(out)
	if len(v.Summary) > maxVerdictSummary+4 {
		t.Fatalf("summary not capped: %d bytes", len(v.Summary))
	}
}
