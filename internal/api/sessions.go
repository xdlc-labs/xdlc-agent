package api

import (
	"errors"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/xdlc-labs/xdlc-agent/internal/session"
	"github.com/xdlc-labs/xdlc-agent/internal/subagent"
)

// Session endpoints serve the Fix recordings under agent.sessions.dir.
//
// Every route here is operator-only. A recording is unscrubbed: the
// prompt embeds CI logs and CI logs occasionally embed secrets, so
// putting one on the wire is a real step up from a 0600 file on the
// daemon host. The viewer role, which exists so a dashboard on a wall
// can show the audit feed, never reaches these. SECURITY.md says so.

// outputTailLines is how much of the agent's output the summary carries.
// Enough to see how a run ended without fetching the whole transcript.
const outputTailLines = 60

// defaultSessionsLimit bounds GET /api/sessions when ?limit is absent.
const defaultSessionsLimit = 50

// handleSessions lists recordings, newest first. ?repo filters, ?limit
// caps (default 50). With recording disabled it answers an empty list
// and enabled=false rather than 404, so the console can say "recording
// is off" instead of "not found".
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.Sessions == nil {
		writeJSON(w, map[string]any{"sessions": []session.Meta{}, "enabled": false})
		return
	}
	limit := defaultSessionsLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	metas, err := s.Sessions.List(r.URL.Query().Get("repo"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if metas == nil {
		metas = []session.Meta{}
	}
	writeJSON(w, map[string]any{"sessions": metas, "enabled": true})
}

// handleSession returns one recording's meta.json, the files it holds,
// and the tail of the agent's output from the last attempt.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id, meta, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	dir, err := s.Sessions.Path(id)
	if err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	files := []string{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() {
				files = append(files, e.Name())
			}
		}
	}
	sort.Strings(files)
	attempt := max(meta.Attempts, 1)
	out, _ := s.Sessions.ReadFile(id, session.AttemptFile(session.FileOutput, attempt))
	writeJSON(w, map[string]any{
		"session":     meta,
		"files":       files,
		"output_tail": tailLines(renderOutput(out), outputTailLines),
	})
}

// renderOutput reduces a recorded transcript to what the live console
// shows for it — prose, tool calls, the result — so the summary panel
// and the /fixes grid read the same way. The raw stream-json is still
// one click away under /output.
func renderOutput(out string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(out, "\n") {
		if r := subagent.RenderStreamLine(line); r != "" {
			b.WriteString(r)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// handleSessionDiff serves diff.patch as text. 404 when the run
// delivered no patch, which is a real outcome and not a missing file.
func (s *Server) handleSessionDiff(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	s.writeSessionFile(w, id, session.FileDiff, "session has no diff")
}

// handleSessionPrompt serves the prompt the agent was given, for
// ?attempt=N (default 1). plan.txt from agent.fix_plan is a different
// file; the console reads it from the file list.
func (s *Server) handleSessionPrompt(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	s.writeSessionFile(w, id, session.AttemptFile(session.FilePrompt, attemptParam(r)), "session has no prompt for that attempt")
}

// handleSessionOutput serves everything the agent printed, for
// ?attempt=N (default 1).
func (s *Server) handleSessionOutput(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	s.writeSessionFile(w, id, session.AttemptFile(session.FileOutput, attemptParam(r)), "session has no output for that attempt")
}

// loadSession resolves {id} against the store. It writes the response
// itself on failure: 404 for a disabled store, an unknown id, or an id
// that is not a plain directory name (the store rejects traversal, but
// the message here is deliberately the same as for a missing one).
func (s *Server) loadSession(w http.ResponseWriter, r *http.Request) (string, session.Meta, bool) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return "", session.Meta{}, false
	}
	if s.Sessions == nil {
		http.Error(w, "session recording disabled", http.StatusNotFound)
		return "", session.Meta{}, false
	}
	if _, err := s.Sessions.Path(id); err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return "", session.Meta{}, false
	}
	meta, err := s.Sessions.Load(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "unknown session", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return "", session.Meta{}, false
	}
	return id, meta, true
}

func (s *Server) writeSessionFile(w http.ResponseWriter, id, name, missing string) {
	body, err := s.Sessions.ReadFile(id, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if body == "" {
		http.Error(w, missing, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	// Served as text/plain with nosniff: a browser never interprets it as
	// markup, so agent output that happens to contain "<script>" is inert.
	_, _ = w.Write([]byte(body)) //nolint:gosec // G705: text/plain + nosniff, not HTML
}

func attemptParam(r *http.Request) int {
	if v := r.URL.Query().Get("attempt"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

// tailLines returns the last n lines of s, without a trailing newline.
func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" || n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
