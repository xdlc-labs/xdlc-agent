package orchestrator

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		s    Signal
		want Action
	}{
		{"ci fail -> fix", Signal{Source: SourceCI, Kind: KindFail}, ActionFix},
		{"ci pass -> noop", Signal{Source: SourceCI, Kind: KindPass}, ActionNoop},
		{"dev-gate fail -> fix", Signal{Source: SourceDevGate, Kind: KindFail}, ActionFix},
		{"dev-gate pass -> promote", Signal{Source: SourceDevGate, Kind: KindPass}, ActionPromote},
		{"prod-health breach -> revert", Signal{Source: SourceProdHealth, Kind: KindBreach}, ActionRevert},
		{"prod-health pass -> noop", Signal{Source: SourceProdHealth, Kind: KindPass}, ActionNoop},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Decide(c.s); got != c.want {
				t.Errorf("Decide(%+v) = %v, want %v", c.s, got, c.want)
			}
		})
	}
}

func TestAuditSourceManualIsDaemon(t *testing.T) {
	ci := Signal{Source: SourceCI, Kind: KindFail, Evidence: map[string]any{"run_url": "https://x"}}
	if got := AuditSource(ci); got != "ci" {
		t.Fatalf("webhook CI = %q, want ci", got)
	}
	manual := Signal{
		Source:   SourceCI,
		Kind:     KindFail,
		Evidence: map[string]any{"manual": true, "via": "api", "action": "fix"},
	}
	if got := AuditSource(manual); got != "daemon" {
		t.Fatalf("manual Fix = %q, want daemon (Decide still uses SourceCI)", got)
	}
	if Decide(manual) != ActionFix {
		t.Fatal("Decide must still return Fix for a manual CI-shaped signal")
	}
}
