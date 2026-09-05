package orchestrator

import (
	"fmt"
	"strings"
)

// structuralPatterns matched case-insensitively against evidence text
// before Fix invokes the Runner (issue #22 / C4).
//
// Keep short and boring. False positives: operator Fix still works —
// Evidence["manual"]=true (console/API) bypasses this check.
var structuralPatterns = []string{
	"cross-boundary",
	"cross-service",
	"across services",
	"complete rewrite",
	"rewrite from scratch",
	"architectural change",
	"architecture redesign",
	"api redesign",
	"breaking api change",
	"missing unpublished dependency",
	"dependency not published",
}

// structuralMatch returns the matched pattern, or "" if none / bypassed.
func structuralMatch(s Signal) string {
	if s.Evidence == nil {
		return ""
	}
	if m, _ := s.Evidence["manual"].(bool); m {
		return ""
	}
	blob := evidenceBlob(s.Evidence)
	if blob == "" {
		return ""
	}
	lower := strings.ToLower(blob)
	for _, p := range structuralPatterns {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}

func evidenceBlob(ev map[string]any) string {
	var b strings.Builder
	for _, v := range ev {
		switch x := v.(type) {
		case string:
			b.WriteString(x)
			b.WriteByte('\n')
		default:
			fmt.Fprintln(&b, x)
		}
	}
	return b.String()
}
