package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// maxTeamInstructionsBytes caps the whole trusted rules block that
	// reaches the prompt.
	maxTeamInstructionsBytes = 16 * 1024
	// maxRuleFileBytes caps one rule file, so a single huge CLAUDE.md
	// cannot crowd out every other source (pre-C-rules the whole block
	// was tail-chopped once, which silently dropped skills).
	maxRuleFileBytes = 8 * 1024
	teamRulesBegin   = "---BEGIN TRUSTED TEAM RULES---"
	teamRulesEnd     = "---END TRUSTED TEAM RULES---"
)

// ruleFileNames are the per-repo instruction files read, in order. The
// first two are the de-facto cross-tool conventions (Codex/Cursor read
// AGENTS.md, Claude Code reads CLAUDE.md); .xdlc/rules.md is for rules
// that should apply to xdlc Fixes only.
var ruleFileNames = []string{"AGENTS.md", "CLAUDE.md", ".xdlc/rules.md"}

// RuleSource is one instruction file that fed the trusted rules block.
// Reported by RuleSources so `xdlc doctor` (and an operator reading it)
// can see exactly what the agent will be told before a Fix runs.
type RuleSource struct {
	// Path is repo-relative, or absolute for the daemon-global file.
	Path string
	// Bytes is the size actually used, after per-file truncation.
	Bytes int
	// Truncated reports whether the file hit maxRuleFileBytes.
	Truncated bool
	// Global marks the daemon-wide rules file (agent.rules_file).
	Global bool
}

// RuleSources lists the instruction files ReadTeamInstructions would
// use for repoDir, in prompt order. globalRulesFile is config.yaml's
// agent.rules_file ("" for none). Missing and empty files are skipped;
// a file whose body duplicates an earlier one is skipped too (a
// CLAUDE.md symlinked to AGENTS.md is common and would otherwise be
// injected twice).
func RuleSources(repoDir, globalRulesFile string) []RuleSource {
	var out []RuleSource
	seen := map[string]bool{}

	add := func(path, display string, global bool) {
		body, truncated := readRuleFile(path)
		if body == "" {
			return
		}
		sum := sha256.Sum256([]byte(body))
		key := hex.EncodeToString(sum[:])
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, RuleSource{Path: display, Bytes: len(body), Truncated: truncated, Global: global})
	}

	for _, name := range ruleFileNames {
		add(filepath.Join(repoDir, filepath.FromSlash(name)), name, false)
	}
	for _, name := range skillFiles(repoDir) {
		add(filepath.Join(repoDir, ".xdlc", "skills", name), ".xdlc/skills/"+name, false)
	}
	if globalRulesFile != "" {
		add(globalRulesFile, globalRulesFile, true)
	}
	return out
}

// ReadTeamInstructions loads the repo's instruction files (AGENTS.md,
// CLAUDE.md, .xdlc/rules.md, .xdlc/skills/*.md) plus the optional
// daemon-global globalRulesFile (config.yaml's agent.rules_file).
// Each file is capped at maxRuleFileBytes and the joined block at
// maxTeamInstructionsBytes. Missing files are a no-op.
//
// Content is trusted team rules — the caller must place it outside the
// untrusted evidence block (see FixPrompt).
func ReadTeamInstructions(repoDir, globalRulesFile string) string {
	var parts []string
	seen := map[string]bool{}

	add := func(path, label string) {
		body, truncated := readRuleFile(path)
		if body == "" {
			return
		}
		sum := sha256.Sum256([]byte(body))
		key := hex.EncodeToString(sum[:])
		if seen[key] {
			return
		}
		seen[key] = true
		if truncated {
			body += truncationMarker
		}
		parts = append(parts, label+":\n"+body)
	}

	for _, name := range ruleFileNames {
		add(filepath.Join(repoDir, filepath.FromSlash(name)), name)
	}
	for _, name := range skillFiles(repoDir) {
		add(filepath.Join(repoDir, ".xdlc", "skills", name), ".xdlc/skills/"+name)
	}
	if globalRulesFile != "" {
		add(globalRulesFile, "global rules ("+filepath.Base(globalRulesFile)+")")
	}

	out := strings.Join(parts, "\n\n")
	if len(out) > maxTeamInstructionsBytes {
		keep := maxTeamInstructionsBytes - len(truncationMarker)
		if keep < 0 {
			keep = 0
		}
		out = out[:keep] + truncationMarker
	}
	return out
}

const truncationMarker = "\n...[truncated]..."

// skillFiles returns the sorted *.md names under repoDir/.xdlc/skills.
func skillFiles(repoDir string) []string {
	entries, err := os.ReadDir(filepath.Join(repoDir, ".xdlc", "skills"))
	if err != nil {
		return nil
	}
	var names []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".md") {
			continue
		}
		names = append(names, ent.Name())
	}
	sort.Strings(names)
	return names
}

// readRuleFile reads one instruction file, trimmed and null-stripped,
// capped at maxRuleFileBytes. Missing/unreadable → "".
func readRuleFile(path string) (body string, truncated bool) {
	body = readFileTrim(path)
	if len(body) > maxRuleFileBytes {
		return body[:maxRuleFileBytes], true
	}
	return body, false
}

func readFileTrim(path string) string {
	// gosec G304: path under agent-owned clone (AGENTS.md / skills) or
	// the operator's own config.yaml agent.rules_file.
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", ""))
}

// FrameTeamRules wraps trusted team instructions for the Fix prompt.
// Empty input → empty string (omit from prompt).
func FrameTeamRules(rules string) string {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return ""
	}
	return teamRulesBegin + "\n" + rules + "\n" + teamRulesEnd
}
