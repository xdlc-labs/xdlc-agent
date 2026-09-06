// Package session records what a Fix subagent actually did — the exact
// prompt it was given, everything it printed, and the diff it produced —
// as plain files on disk, one directory per run.
//
// The audit store answers "what happened" (repo, action, status, cost).
// It cannot answer "why did the agent do that", because the subagent's
// reasoning only ever reached a truncated log line. A session directory
// is that missing half: the operator reviewing an automated Fix at 09:00
// can read the prompt, the output and the patch instead of guessing.
//
// Files are local and never uploaded. They are also unscrubbed: a Fix
// prompt embeds CI logs, and CI logs occasionally embed secrets. The
// directory is 0700 and entries are 0600; see docs/sessions.md and
// docs/SECURITY.md.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Defaults applied when Open gets a zero value.
const (
	DefaultRetain       = 30 * 24 * time.Hour
	DefaultMaxFileBytes = 2 << 20 // 2 MiB per artifact
	truncationMarker    = "\n...[truncated by xdlc: max_file_bytes]...\n"
)

// File names inside a session directory. Stable — `xdlc sessions show`
// and anything an operator greps depend on them.
const (
	FileMeta   = "meta.json"
	FilePrompt = "prompt.txt"
	FilePlan   = "plan.txt"
	FileOutput = "output.txt"
	FileDiff   = "diff.patch"
)

// AttemptFile returns the artifact name for one attempt of a Fix that
// went round the reverify ladder more than once. Attempt 1 keeps the
// bare name, so single-attempt sessions — still the common case — look
// exactly as they always have to `xdlc sessions show` and to anyone
// grepping the directory; attempt 2 writes prompt-2.txt / output-2.txt
// alongside, rather than overwriting the record of what was already
// tried.
func AttemptFile(name string, attempt int) string {
	if attempt <= 1 {
		return name
	}
	ext := filepath.Ext(name)
	return fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), attempt, ext)
}

// Meta is the session summary written to meta.json.
type Meta struct {
	ID         string         `json:"id"`
	Repo       string         `json:"repo"`
	Source     string         `json:"source"`
	Kind       string         `json:"kind"`
	Provider   string         `json:"provider"`
	FixMode    string         `json:"fix_mode,omitempty"`
	Manual     bool           `json:"manual,omitempty"`
	StartedAt  time.Time      `json:"started_at"`
	EndedAt    time.Time      `json:"ended_at,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Status     string         `json:"status,omitempty"` // ok | error
	Error      string         `json:"error,omitempty"`
	BaseSHA    string         `json:"base_sha,omitempty"`
	HeadSHA    string         `json:"head_sha,omitempty"`
	Branch     string         `json:"branch,omitempty"`
	Changed    int            `json:"changed_files,omitempty"`
	PRURL      string         `json:"pr_url,omitempty"`
	Cost       map[string]any `json:"cost,omitempty"`
	// Attempts is how many Fix agent runs this session took. Absent (0)
	// or 1 means the single-shot path; >1 means the reverify ladder
	// re-ran the agent, and prompt-2.txt / output-2.txt exist.
	Attempts int `json:"attempts,omitempty"`
	// Outcome is the agent's own last verdict — "fixed", "gave_up" or
	// "needs_human". Empty when the agent emitted no verdict line.
	Outcome string `json:"outcome,omitempty"`
	// Summary is the agent's one-line description of what it did, from
	// that same verdict.
	Summary string `json:"summary,omitempty"`
}

// Store writes and reads session directories under Root.
type Store struct {
	Root         string
	Retain       time.Duration
	MaxFileBytes int64

	pruneMu   sync.Mutex
	lastPrune time.Time
}

// Open returns a Store rooted at dir, creating it if needed. dir "" →
// nil Store, which every method tolerates (recording disabled).
func Open(dir string, retain time.Duration, maxFileBytes int64) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	if retain <= 0 {
		retain = DefaultRetain
	}
	if maxFileBytes <= 0 {
		maxFileBytes = DefaultMaxFileBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create %s: %w", dir, err)
	}
	return &Store{Root: dir, Retain: retain, MaxFileBytes: maxFileBytes}, nil
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// NewID builds a sortable, filesystem-safe session id from the start
// time and repo name: 20260905T010514Z-example-service.
func NewID(at time.Time, repo string) string {
	slug := strings.Trim(unsafeName.ReplaceAllString(repo, "-"), "-")
	if slug == "" {
		slug = "repo"
	}
	if len(slug) > 48 {
		slug = slug[:48]
	}
	return at.UTC().Format("20060102T150405Z") + "-" + slug
}

// Session is one recording in progress. A nil *Session is valid and
// every method on it is a no-op, so callers never branch on whether
// recording is enabled.
type Session struct {
	store *Store
	dir   string
	meta  Meta
	mu    sync.Mutex
}

// Start creates the directory for a new session and returns a handle.
// A nil Store (recording disabled) returns a nil *Session and no error.
func (s *Store) Start(meta Meta) (*Session, error) {
	if s == nil {
		return nil, nil
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now().UTC()
	}
	if meta.ID == "" {
		meta.ID = NewID(meta.StartedAt, meta.Repo)
	}
	dir := filepath.Join(s.Root, meta.ID)
	// Two Fixes for one repo can start inside the same second.
	for i := 1; ; i++ {
		if err := os.Mkdir(dir, 0o700); err == nil {
			break
		} else if !os.IsExist(err) {
			return nil, fmt.Errorf("session: create %s: %w", dir, err)
		}
		meta.ID = fmt.Sprintf("%s-%d", NewID(meta.StartedAt, meta.Repo), i)
		dir = filepath.Join(s.Root, meta.ID)
		if i > 50 {
			return nil, fmt.Errorf("session: cannot allocate id under %s", s.Root)
		}
	}
	sess := &Session{store: s, dir: dir, meta: meta}
	if err := sess.writeMeta(); err != nil {
		return nil, err
	}
	go s.pruneAsync()
	return sess, nil
}

// ID returns the session id ("" when recording is disabled).
func (sess *Session) ID() string {
	if sess == nil {
		return ""
	}
	return sess.meta.ID
}

// Dir returns the session directory ("" when recording is disabled).
func (sess *Session) Dir() string {
	if sess == nil {
		return ""
	}
	return sess.dir
}

// Write stores one artifact (see the File* constants), truncating at
// the store's MaxFileBytes. Empty content is skipped. Errors are
// returned but are never fatal to a Fix — the caller logs and moves on.
func (sess *Session) Write(name, content string) error {
	if sess == nil || strings.TrimSpace(content) == "" {
		return nil
	}
	if max := sess.store.MaxFileBytes; max > 0 && int64(len(content)) > max {
		content = content[:max] + truncationMarker
	}
	path := filepath.Join(sess.dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("session: write %s: %w", path, err)
	}
	return nil
}

// SetResult records the outcome fields. Call before Finish.
func (sess *Session) SetResult(status, errMsg string, cost map[string]any) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Status = status
	sess.meta.Error = errMsg
	if len(cost) > 0 {
		sess.meta.Cost = cost
	}
}

// SetGit records the commit range the agent produced.
func (sess *Session) SetGit(base, head, branch string, changed int) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.BaseSHA, sess.meta.HeadSHA, sess.meta.Branch, sess.meta.Changed = base, head, branch, changed
}

// SetPR records the PR opened by fix_mode: pr.
func (sess *Session) SetPR(url string) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.PRURL = url
}

// SetVerdict records the agent's own last self-report and how many
// agent runs the Fix took. Call before Finish.
func (sess *Session) SetVerdict(outcome, summary string, attempts int) {
	if sess == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Outcome, sess.meta.Summary, sess.meta.Attempts = outcome, summary, attempts
}

// Finish stamps the end time and rewrites meta.json.
func (sess *Session) Finish() error {
	if sess == nil {
		return nil
	}
	sess.mu.Lock()
	sess.meta.EndedAt = time.Now().UTC()
	sess.meta.DurationMS = sess.meta.EndedAt.Sub(sess.meta.StartedAt).Milliseconds()
	sess.mu.Unlock()
	return sess.writeMeta()
}

func (sess *Session) writeMeta() error {
	sess.mu.Lock()
	raw, err := json.MarshalIndent(sess.meta, "", "  ")
	sess.mu.Unlock()
	if err != nil {
		return fmt.Errorf("session: marshal meta: %w", err)
	}
	path := filepath.Join(sess.dir, FileMeta)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("session: write %s: %w", path, err)
	}
	return nil
}

// List returns session metadata newest first. repo "" means all repos;
// limit <= 0 means no cap.
func (s *Store) List(repo string, limit int) ([]Meta, error) {
	if s == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, fmt.Errorf("session: list %s: %w", s.Root, err)
	}
	var out []Meta
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		m, err := s.Load(ent.Name())
		if err != nil {
			continue // half-written or hand-deleted; skip
		}
		if repo != "" && m.Repo != repo {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Load reads one session's meta.json.
func (s *Store) Load(id string) (Meta, error) {
	if s == nil {
		return Meta{}, fmt.Errorf("session: recording disabled")
	}
	dir, err := s.Path(id)
	if err != nil {
		return Meta{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileMeta)) //nolint:gosec // path validated by Path
	if err != nil {
		return Meta{}, fmt.Errorf("session: read meta %s: %w", id, err)
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Meta{}, fmt.Errorf("session: parse meta %s: %w", id, err)
	}
	return m, nil
}

// Path returns the directory for id, rejecting anything that would
// escape Root (an id reaches this from CLI args).
func (s *Store) Path(id string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("session: recording disabled")
	}
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return "", fmt.Errorf("session: invalid id %q", id)
	}
	dir := filepath.Join(s.Root, id)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("session: unknown id %q", id)
	}
	return dir, nil
}

// ReadFile returns one artifact from a session ("" when absent).
func (s *Store) ReadFile(id, name string) (string, error) {
	dir, err := s.Path(id)
	if err != nil {
		return "", err
	}
	if name != filepath.Base(name) {
		return "", fmt.Errorf("session: invalid file %q", name)
	}
	raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // both segments validated
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("session: read %s/%s: %w", id, name, err)
	}
	return string(raw), nil
}

// pruneAsync runs Prune at most once an hour, in the background.
func (s *Store) pruneAsync() {
	s.pruneMu.Lock()
	if time.Since(s.lastPrune) < time.Hour {
		s.pruneMu.Unlock()
		return
	}
	s.lastPrune = time.Now()
	s.pruneMu.Unlock()
	_, _ = s.Prune()
}

// Prune deletes sessions older than Retain and reports how many went.
func (s *Store) Prune() (int, error) {
	if s == nil {
		return 0, nil
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return 0, fmt.Errorf("session: prune %s: %w", s.Root, err)
	}
	cutoff := time.Now().Add(-s.Retain)
	removed := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.Root, ent.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}

// HeadSHA returns the current commit in repoDir, or "" if git fails.
func HeadSHA(ctx context.Context, repoDir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD").Output() //nolint:gosec // repoDir is an agent-owned clone
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Diff returns the patch the agent produced: committed changes since
// baseSHA plus anything still uncommitted in the worktree. Empty when
// git fails or nothing changed.
func Diff(ctx context.Context, repoDir, baseSHA string) (patch string, changed int) {
	var b strings.Builder
	if baseSHA != "" {
		if out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", baseSHA+"..HEAD").Output(); err == nil { //nolint:gosec // repoDir is an agent-owned clone, baseSHA is git's own output
			b.Write(out)
		}
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", "HEAD").Output(); err == nil && len(out) > 0 { //nolint:gosec // repoDir is an agent-owned clone
		if b.Len() > 0 {
			b.WriteString("\n--- uncommitted working-tree changes ---\n")
		}
		b.Write(out)
	}
	patch = b.String()
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			changed++
		}
	}
	return patch, changed
}
