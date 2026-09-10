package subagent

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRenderStreamLine(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain text passes through": {"working on it", "working on it"},
		"blank drops":               {"   \n", "   "},
		"init handshake drops": {
			`{"type":"system","subtype":"init","session_id":"abc","tools":["Bash"]}`, "",
		},
		"assistant prose": {
			`{"type":"assistant","message":{"content":[{"type":"text","text":"The test expects 3.\n"}]}}`,
			"The test expects 3.",
		},
		"tool call shows the command": {
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}]}}`,
			"▸ Bash go test ./...",
		},
		"tool call shows the path": {
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"internal/x.go","old_string":"a","new_string":"b"}}]}}`,
			"▸ Edit internal/x.go",
		},
		"tool result drops": {
			`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}`, "",
		},
		"result with cost": {
			`{"type":"result","subtype":"success","is_error":false,"duration_ms":93000,"num_turns":12,"total_cost_usd":1.6301,"result":"..."}`,
			"■ done, 12 turns, 93s, $1.63",
		},
		"result that hit a limit": {
			`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":50}`,
			"■ ended: error_max_turns, 50 turns",
		},
		"unknown json is still output": {
			`{"foo":"bar"}`, `{"foo":"bar"}`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := RenderStreamLine(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestRenderStreamLineClipsLongLines(t *testing.T) {
	long := strings.Repeat("é", maxRenderedLine)
	got := RenderStreamLine(long)
	if !strings.HasSuffix(got, "…") || len(got) > maxRenderedLine+len("…") {
		t.Fatalf("len=%d suffix=%q", len(got), got[len(got)-3:])
	}
	// Never split a multi-byte rune.
	if !strings.HasPrefix(got, "é") || strings.ContainsRune(got, '�') {
		t.Fatalf("clip broke a rune: %q", got[:8])
	}
}

func TestWithStreamingRewritesOnlyClaudeJSON(t *testing.T) {
	r := NewSubprocessRunner(ProviderClaude, "", nil, time.Minute, nil).WithStreaming()
	if !strings.Contains(strings.Join(r.Args, " "), "--output-format stream-json --verbose") {
		t.Fatalf("claude argv not streamed: %v", r.Args)
	}
	if r.StallTimeout != 0 {
		t.Fatal("streaming must not turn the watchdog on")
	}
	c := NewSubprocessRunner(ProviderCursor, "", nil, time.Minute, nil)
	if got := c.WithStreaming().Args; strings.Join(got, " ") != strings.Join(c.Args, " ") {
		t.Fatalf("cursor argv changed: %v", got)
	}
}

// The tap sees both streams as the process writes them, and Run's own
// return value is unchanged by tapping.
func TestRunCopiesOutputToTap(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	r := &SubprocessRunner{Binary: "sh", Args: []string{"-c", "echo out1; echo err1 >&2; echo out2"}, Timeout: 10 * time.Second}
	var tap bytes.Buffer
	ctx := WithOutputTap(context.Background(), &tap)
	out, err := r.Run(ctx, t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "out1\nout2\n" {
		t.Fatalf("stdout=%q", out)
	}
	got := tap.String()
	for _, want := range []string{"out1\n", "err1\n", "out2\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tap missing %q: %q", want, got)
		}
	}
}

func TestWithOutputTapNilIsSameContext(t *testing.T) {
	ctx := context.Background()
	if WithOutputTap(ctx, nil) != ctx {
		t.Fatal("nil writer should not wrap the context")
	}
	if outputTap(ctx) != nil {
		t.Fatal("bare context has no tap")
	}
}
