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
	//
	// stderr is captured and carried into the error: kubectl says on
	// stderr whether the job does not exist, the namespace is wrong or
	// the kubeconfig is dead, and without it every one of those reads
	// as "exit status 1" — the same problem argocd had (issue #45).
	statusCmd := exec.CommandContext(ctx, bin, "-n", ns, "get", "job", job, //nolint:gosec
		"-o", "jsonpath={.status.succeeded}")
	var statusOut, statusErr bytes.Buffer
	statusCmd.Stdout = &statusOut
	statusCmd.Stderr = &statusErr
	if err := statusCmd.Run(); err != nil {
		return false, "", fmt.Errorf("k8sprobe: get job %s/%s: %w%s", ns, job, err, stderrDetail(statusErr.Bytes()))
	}
	succeeded := strings.TrimSpace(statusOut.String()) != "" && strings.TrimSpace(statusOut.String()) != "0"

	logsCmd := exec.CommandContext(ctx, bin, "-n", ns, "logs", "job/"+job, "--tail=200") //nolint:gosec // see above
	var logsOut bytes.Buffer
	logsCmd.Stdout = &logsOut
	_ = logsCmd.Run() // logs are best-effort evidence; don't fail the probe check on a logs error

	return succeeded, logsOut.String(), nil
}

// maxStderrBytes caps how much of kubectl's stderr is carried into an
// error. Enough for the one-line reason kubectl prints; not enough for a
// usage dump to swamp the log line.
const maxStderrBytes = 512

// stderrDetail renders captured stderr as a ": ..." suffix for an error
// message — collapsed to one line and truncated — or "" when the command
// printed nothing. Same shape as internal/gitops' helper; copied rather
// than imported so this package stays free of the gitops dependency.
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
