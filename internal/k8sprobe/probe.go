// Package k8sprobe checks whether a smoke/e2e probe Job succeeded, by
// shelling out to kubectl (same dependency-on-CLI-not-client-go choice
// as internal/gitops, keeps this binary lighter and matches how a
// GitOps operator's box is usually already set up).
package k8sprobe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Client runs kubectl commands against one cluster (whatever the
// ambient kubeconfig points at).
type Client struct {
	Binary string // defaults to "kubectl"
}

// New returns a Client using the "kubectl" binary on PATH.
func New() *Client {
	return &Client{Binary: "kubectl"}
}

// JobSucceeded reports whether Job job in namespace ns has at least one
// succeeded pod, and returns its logs for evidence either way.
func (c *Client) JobSucceeded(ctx context.Context, ns, job string) (passed bool, logs string, err error) {
	bin := c.Binary
	if bin == "" {
		bin = "kubectl"
	}

	// gosec G204: bin is operator config (Client.Binary), ns/job come
	// from this daemon's own gates.dev-smoke config — not external
	// input.
	statusCmd := exec.CommandContext(ctx, bin, "-n", ns, "get", "job", job, //nolint:gosec
		"-o", "jsonpath={.status.succeeded}")
	var statusOut bytes.Buffer
	statusCmd.Stdout = &statusOut
	if err := statusCmd.Run(); err != nil {
		return false, "", fmt.Errorf("k8sprobe: get job %s/%s: %w", ns, job, err)
	}
	succeeded := strings.TrimSpace(statusOut.String()) != "" && strings.TrimSpace(statusOut.String()) != "0"

	logsCmd := exec.CommandContext(ctx, bin, "-n", ns, "logs", "job/"+job, "--tail=200") //nolint:gosec // see above
	var logsOut bytes.Buffer
	logsCmd.Stdout = &logsOut
	_ = logsCmd.Run() // logs are best-effort evidence; don't fail the probe check on a logs error

	return succeeded, logsOut.String(), nil
}
