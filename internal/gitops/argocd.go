// Package gitops talks to ArgoCD to answer "is this Application
// Synced+Healthy" for the DEV smoke gate.
package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ArgoCDClient shells out to the `argocd` CLI (assumes an authenticated
// context, same as any GitOps operator's local setup). Swap for a direct
// REST client against the ArgoCD API server if you'd rather not depend
// on the CLI binary being present.
type ArgoCDClient struct {
	Binary string // defaults to "argocd"
}

// NewArgoCDClient returns an ArgoCDClient using the "argocd" binary on PATH.
func NewArgoCDClient() *ArgoCDClient {
	return &ArgoCDClient{Binary: "argocd"}
}

type appStatus struct {
	Status struct {
		Sync   struct{ Status string } `json:"sync"`
		Health struct{ Status string } `json:"health"`
	} `json:"status"`
}

// maxStderrBytes caps how much of the CLI's stderr is carried into an
// error. The message ends up in gate evidence, a BACKLOG.md line and an
// audit row, so it has to stay one readable line rather than however
// many kilobytes a failing CLI decides to print.
const maxStderrBytes = 512

// AppHealthy reports true if app is both Synced and Healthy.
//
// A failing `argocd` is the single most common way this gate stops
// working, and every distinct cause — a typo'd app name, an expired
// session, a missing kubeconfig, an RBAC denial — exits non-zero. The
// CLI says which one it was on *stderr*, so stderr is captured and
// carried into the error: without it every failure reads as
// "exit status 20" and an operator cannot tell a config typo from a
// dead cluster (issue #45).
func (c *ArgoCDClient) AppHealthy(ctx context.Context, app string) (bool, error) {
	bin := c.Binary
	if bin == "" {
		bin = "argocd"
	}
	// gosec G204: bin is operator config (ArgoCDClient.Binary), app is
	// this daemon's own gates.dev-smoke.argocd_app config value — not
	// external input.
	cmd := exec.CommandContext(ctx, bin, "app", "get", app, "-o", "json") //nolint:gosec
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("gitops: argocd app get %s: %w%s", app, err, stderrDetail(errOut.Bytes()))
	}

	var s appStatus
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		return false, fmt.Errorf("gitops: parse argocd status: %w%s", err, stderrDetail(errOut.Bytes()))
	}
	return s.Status.Sync.Status == "Synced" && s.Status.Health.Status == "Healthy", nil
}

// stderrDetail renders captured stderr as a ": ..." suffix for an error
// message — collapsed to one line and truncated — or "" when the command
// printed nothing.
func stderrDetail(b []byte) string {
	text := strings.Join(strings.Fields(string(b)), " ")
	if text == "" {
		return ""
	}
	if len(text) > maxStderrBytes {
		text = text[:maxStderrBytes] + "…"
	}
	return ": " + text
}
