package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Live output for the console.
//
// Run buffers everything the CLI prints and hands it back as one string
// when the process exits, which is what the session recording and the
// verdict parser want. A console watching a Fix wants the opposite: each
// line as it is printed. The tap below is the second reader. It rides on
// the context rather than on the Runner because it is per Fix — the
// writer is keyed by the Fix id the console tracks — while the Runner is
// built once per provider.

type tapKey struct{}

// WithOutputTap returns a context under which Run copies every stdout
// and stderr write to w as well as to its own buffer. w sees raw bytes
// in subprocess-sized chunks; line assembly is the writer's job.
func WithOutputTap(ctx context.Context, w io.Writer) context.Context {
	if w == nil {
		return ctx
	}
	return context.WithValue(ctx, tapKey{}, w)
}

// outputTap returns the context's tap wrapped so that stdout and stderr,
// which exec copies on two goroutines, never write to it concurrently.
func outputTap(ctx context.Context) io.Writer {
	w, _ := ctx.Value(tapKey{}).(io.Writer)
	if w == nil {
		return nil
	}
	return &lockedWriter{w: w}
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// WithStreaming returns a copy whose argv prints as it works, for a
// daemon that streams agent output to the console. Today that changes
// one shape only — Claude Code's `--output-format json`, which prints
// its whole result at exit, becomes `stream-json --verbose` — and the
// other three CLIs already narrate. Cost and verdict parsing accept both
// forms, so nothing downstream changes. WithStallTimeout does the same
// rewrite for its own reason; applying both is harmless.
func (r *SubprocessRunner) WithStreaming() *SubprocessRunner {
	clone := *r
	clone.Args = streamingArgsFor(r.Provider, r.Args)
	return &clone
}

// maxRenderedLine bounds one console line. A tool result that dumps a
// whole file is real output, but the console tail is for following
// along, and the session recording has the rest.
const maxRenderedLine = 400

// streamEvent is the subset of a stream-json event the console renders.
// Claude Code and Cursor emit this shape; Codex and Gemini print text.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	IsError    bool    `json:"is_error"`
	DurationMS int64   `json:"duration_ms"`
	NumTurns   int     `json:"num_turns"`
	CostUSD    float64 `json:"total_cost_usd"`
	// ToolCall is cursor-agent's shape: one key naming the tool
	// ("editToolCall", "shellToolCall", …) whose value carries args,
	// beside bookkeeping keys (toolCallId, hookAdditionalContexts, …)
	// of other shapes — hence raw, decoded per key below.
	ToolCall map[string]json.RawMessage `json:"tool_call"`
}

// RenderStreamLine turns one line of agent output into what a console
// tail should show for it, or "" to show nothing.
//
// A plain line is returned as is, bounded. A stream-json event is
// reduced to the part a person following along wants: the assistant's
// prose, the name and target of each tool call, and the final result
// with its cost. Tool results, the init handshake and anything else the
// protocol carries are dropped — they are in the session recording, and
// on a console they bury the two lines that matter.
func RenderStreamLine(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return ""
	}
	if !strings.HasPrefix(line, "{") {
		return clip(line, maxRenderedLine)
	}
	var ev streamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type == "" {
		// JSON-looking but not an event we know: still the agent's
		// output, so show it rather than hide it.
		return clip(line, maxRenderedLine)
	}
	switch ev.Type {
	case "assistant":
		var out []string
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					out = append(out, clip(t, maxRenderedLine))
				}
			case "tool_use":
				out = append(out, "▸ "+c.Name+" "+toolTarget(c.Input))
			}
		}
		return strings.Join(out, "\n")
	case "tool_call":
		// cursor-agent: one event when the call starts and one when it
		// completes; the start is the one worth a line.
		if ev.Subtype != "started" {
			return ""
		}
		for name, raw := range ev.ToolCall {
			if !strings.HasSuffix(name, "ToolCall") {
				continue
			}
			var call struct {
				Args map[string]any `json:"args"`
			}
			if json.Unmarshal(raw, &call) != nil {
				continue
			}
			target := ""
			for _, k := range []string{"command", "path", "file_path", "pattern", "query", "url"} {
				if v, ok := call.Args[k].(string); ok && v != "" {
					target = clip(strings.Join(strings.Fields(v), " "), 160)
					break
				}
			}
			return "▸ " + strings.TrimSuffix(name, "ToolCall") + " " + target
		}
		return ""
	case "result":
		status := "done"
		if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
			status = "ended: " + ev.Subtype
		}
		s := "■ " + status
		if ev.NumTurns > 0 {
			s += fmt.Sprintf(", %d turns", ev.NumTurns)
		}
		if ev.DurationMS > 0 {
			s += fmt.Sprintf(", %ds", ev.DurationMS/1000)
		}
		if ev.CostUSD > 0 {
			s += fmt.Sprintf(", $%.2f", ev.CostUSD)
		}
		return s
	default:
		// system/init, user (tool results), rate limit notices, and
		// whatever future events the CLI adds.
		return ""
	}
}

// toolTarget picks the one field of a tool call's input worth a glance:
// the command for a shell, the path for a file tool, else a short
// rendering of the whole input.
func toolTarget(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return clip(string(input), 120)
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "query", "url", "description"} {
		if v, ok := m[k].(string); ok && v != "" {
			return clip(strings.Join(strings.Fields(v), " "), 160)
		}
	}
	return clip(string(input), 120)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary so a multi-byte character is not split.
	cut := n
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
