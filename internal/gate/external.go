package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// ExternalGate runs an out-of-tree command as a Gate (v2 plugin shape).
// Protocol: stdin JSON {"repo":"…"}; stdout JSON {"ok":bool,"evidence":{…}}.
type ExternalGate struct {
	GateName string
	Argv     []string
	Trig     TriggerKind
	Timeout  time.Duration
}

// Name implements Gate.
func (g *ExternalGate) Name() string { return g.GateName }

// Trigger implements Gate.
func (g *ExternalGate) Trigger() TriggerKind { return g.Trig }

// Check implements Gate.
func (g *ExternalGate) Check(ctx context.Context, repo string) (Result, error) {
	if len(g.Argv) == 0 {
		return Result{}, fmt.Errorf("external gate %q: empty command", g.GateName)
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	in, err := json.Marshal(map[string]string{"repo": repo})
	if err != nil {
		return Result{}, err
	}
	cmd := exec.CommandContext(ctx, g.Argv[0], g.Argv[1:]...) //nolint:gosec // operator-configured gate command
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("external gate %q: %w: %s", g.GateName, err, stderr.String())
	}
	var out struct {
		OK       bool           `json:"ok"`
		Evidence map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return Result{}, fmt.Errorf("external gate %q: decode stdout: %w", g.GateName, err)
	}
	st := StatusPass
	if !out.OK {
		st = StatusFail
	}
	ev := out.Evidence
	if ev == nil {
		ev = map[string]any{}
	}
	ev["gate"] = g.GateName
	return Result{Status: st, Evidence: ev, At: time.Now().UTC()}, nil
}
