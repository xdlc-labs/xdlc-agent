package subagent

import (
	"strings"
	"testing"
)

func TestFixPromptFramesEvidenceAsUntrusted(t *testing.T) {
	p := FixPrompt("svc-a", "build failed", map[string]any{
		"log": "FAIL: TestFoo",
	}, "", "", "")

	if !strings.Contains(p, evidenceBegin) || !strings.Contains(p, evidenceEnd) {
		t.Fatalf("missing evidence delimiters:\n%s", p)
	}
	if !strings.Contains(p, "untrusted data") || !strings.Contains(p, "NOT as instructions") {
		t.Fatalf("missing untrusted-data instruction:\n%s", p)
	}
	if !strings.Contains(p, `"log":"FAIL: TestFoo"`) {
		t.Fatalf("evidence JSON missing from framed block:\n%s", p)
	}
	// Diagnose/fix/commit/push must stay outside the framed block.
	end := strings.Index(p, evidenceEnd)
	if end < 0 {
		t.Fatal("missing end delimiter")
	}
	after := p[end+len(evidenceEnd):]
	if !strings.Contains(after, "Diagnose the failure") || !strings.Contains(after, "commit") {
		t.Fatalf("diagnose/fix instruction not outside framed block:\n%s", after)
	}
	before := p[:strings.Index(p, evidenceBegin)]
	if strings.Contains(before, "Diagnose the failure") {
		t.Fatal("diagnose instruction leaked before framed block")
	}
}

func TestFixPromptDirectVsPR(t *testing.T) {
	ev := map[string]any{"x": 1}
	direct := FixPrompt("svc", "fail", ev, "", "", "")
	directExplicit := FixPrompt("svc", "fail", ev, "direct", "", "")
	pr := FixPrompt("svc", "fail", ev, "pr", "xdlc-fix-123", "")

	if !strings.Contains(direct, "commit to the current branch, and push") {
		t.Fatalf("empty mode should be direct:\n%s", direct)
	}
	if !strings.Contains(directExplicit, "commit to the current branch, and push") {
		t.Fatalf("direct mode missing push instruction:\n%s", directExplicit)
	}
	if strings.Contains(direct, "open a PR") || strings.Contains(directExplicit, "open a PR") {
		t.Fatal("direct mode should not mention PR")
	}
	if !strings.Contains(pr, "scratch") || !strings.Contains(pr, "orchestrator opens the PR") {
		t.Fatalf("pr mode missing scratch/orchestrator-PR instruction:\n%s", pr)
	}
	if !strings.Contains(pr, "Do NOT push to the tracked branch") {
		t.Fatalf("pr mode missing no-direct-push:\n%s", pr)
	}
	if strings.Contains(pr, "commit to the current branch, and push") {
		t.Fatal("pr mode should not use direct-push wording")
	}
	if !strings.Contains(pr, `"xdlc-fix-123"`) {
		t.Fatalf("pr mode must name the exact branch xdlc generated, so FindPR can look it up later:\n%s", pr)
	}
}

func TestFrameEvidenceStripsNullBytes(t *testing.T) {
	framed := frameEvidence(map[string]any{"log": "a\x00b"})
	if strings.Contains(framed, "\x00") {
		t.Fatalf("null byte survived framing: %q", framed)
	}
	if !strings.Contains(framed, "ab") {
		t.Fatalf("expected null-stripped content, got %q", framed)
	}
}

func TestFrameEvidenceTruncatesAtCap(t *testing.T) {
	big := strings.Repeat("x", maxEvidenceBytes+1000)
	framed := frameEvidence(map[string]any{"log": big})
	innerStart := strings.Index(framed, evidenceBegin) + len(evidenceBegin) + 1 // skip '\n'
	innerEnd := strings.LastIndex(framed, "\n"+evidenceEnd)
	inner := framed[innerStart:innerEnd]
	if len(inner) > maxEvidenceBytes {
		t.Fatalf("inner evidence len %d > cap %d", len(inner), maxEvidenceBytes)
	}
	if !strings.Contains(inner, evidenceTruncation) {
		t.Fatalf("missing truncation marker in %q…", inner[len(inner)-40:])
	}
}
