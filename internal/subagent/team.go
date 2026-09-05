package subagent

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	maxTeamInstructionsBytes = 16 * 1024
	teamRulesBegin           = "---BEGIN TRUSTED TEAM RULES---"
	teamRulesEnd             = "---END TRUSTED TEAM RULES---"
)

// ReadTeamInstructions loads AGENTS.md and .xdlc/skills/*.md from
// repoDir (size-capped). Missing files are a no-op. Content is trusted
// team rules — caller must place it outside the untrusted evidence block.
func ReadTeamInstructions(repoDir string) string {
	var parts []string
	if s := readFileTrim(filepath.Join(repoDir, "AGENTS.md")); s != "" {
		parts = append(parts, "AGENTS.md:\n"+s)
	}
	skillsDir := filepath.Join(repoDir, ".xdlc", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err == nil {
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".md") {
				continue
			}
			s := readFileTrim(filepath.Join(skillsDir, ent.Name()))
			if s == "" {
				continue
			}
			parts = append(parts, ".xdlc/skills/"+ent.Name()+":\n"+s)
		}
	}
	out := strings.Join(parts, "\n\n")
	if len(out) > maxTeamInstructionsBytes {
		keep := maxTeamInstructionsBytes - len("\n...[truncated]...")
		if keep < 0 {
			keep = 0
		}
		out = out[:keep] + "\n...[truncated]..."
	}
	return out
}

func readFileTrim(path string) string {
	// gosec G304: path under agent-owned clone (AGENTS.md / skills).
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
