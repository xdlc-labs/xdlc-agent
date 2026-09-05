// Package gitops talks to ArgoCD to answer "is this Application
// Synced+Healthy" for the DEV smoke gate.
package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
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

// AppHealthy reports true if app is both Synced and Healthy.
func (c *ArgoCDClient) AppHealthy(ctx context.Context, app string) (bool, error) {
	bin := c.Binary
	if bin == "" {
		bin = "argocd"
	}
	// gosec G204: bin is operator config (ArgoCDClient.Binary), app is
	// this daemon's own gates.dev-smoke.argocd_app config value — not
	// external input.
	cmd := exec.CommandContext(ctx, bin, "app", "get", app, "-o", "json") //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("gitops: argocd app get %s: %w", app, err)
	}

	var s appStatus
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		return false, fmt.Errorf("gitops: parse argocd status: %w", err)
	}
	return s.Status.Sync.Status == "Synced" && s.Status.Health.Status == "Healthy", nil
}
