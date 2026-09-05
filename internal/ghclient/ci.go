// Package ghclient wraps go-github for the one thing the CI gate needs:
// the latest workflow_run conclusion for a repo's branch.
package ghclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v90/github"
)

// defaultBranch is the trunk assumed for a repo that configures none.
// Keep it equal to repos.DefaultBranch — this package can't import
// repos (repos imports it, for TokenProvider), so the constant is
// duplicated here rather than the literal being sprinkled around.
const defaultBranch = "develop"

// Client wraps go-github for GetStatus.
type Client struct {
	gh *github.Client
	// Branch is the fallback branch GetStatus reads workflow runs for
	// when BranchFor has no answer. Defaults to defaultBranch.
	Branch string
	// BranchFor resolves "owner/name" to that repo's configured dev
	// branch — set it (repos.Manager.BranchByGitHub) so a repo on
	// `branch: main` is actually watched on main instead of silently
	// never producing a CI signal. nil → Branch for every repo.
	BranchFor func(ownerRepo string) string
}

// New builds a client authenticated with token (typically GITHUB_TOKEN).
// An empty token still works for public repos, just rate-limited harder.
func New(token string) *Client {
	var opts []github.ClientOptionsFunc
	if token != "" {
		opts = append(opts, github.WithAuthToken(token))
	}
	return &Client{gh: mustClient(opts...), Branch: defaultBranch}
}

// mustClient wraps github.NewClient; only fails on bad option values.
func mustClient(opts ...github.ClientOptionsFunc) *github.Client {
	c, err := github.NewClient(opts...)
	if err != nil {
		panic("ghclient: NewClient: " + err.Error()) // ponytail: options are static; surface misconfig loudly
	}
	return c
}

// GetStatus fetches the most recent workflow_run conclusion for
// "owner/repo" on that repo's branch (BranchFor, else Branch). Matches
// the signature gate.CIGate.GetStatus expects.
func (c *Client) GetStatus(ctx context.Context, repo string) (conclusion string, runURL string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return "", "", fmt.Errorf("ghclient: repo %q must be \"owner/name\"", repo)
	}

	branch := c.branchFor(repo)

	runs, _, err := c.gh.Actions.ListRepositoryWorkflowRuns(ctx, owner, name, &github.ListWorkflowRunsOptions{
		Branch:      branch,
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return "", "", fmt.Errorf("ghclient: list workflow runs for %s: %w", repo, err)
	}
	if len(runs.WorkflowRuns) == 0 {
		return "", "", fmt.Errorf("ghclient: no workflow runs found for %s@%s", repo, branch)
	}

	run := runs.WorkflowRuns[0]
	return run.GetConclusion(), run.GetHTMLURL(), nil
}

// branchFor resolves which branch's workflow runs count for ownerRepo.
func (c *Client) branchFor(ownerRepo string) string {
	if c.BranchFor != nil {
		if b := c.BranchFor(ownerRepo); b != "" {
			return b
		}
	}
	if c.Branch != "" {
		return c.Branch
	}
	return defaultBranch
}

var runURLRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/actions/runs/(\d+)`)

// ParseRunURL extracts owner, repo, runID from a workflow run HTML URL.
func ParseRunURL(runURL string) (owner, repo string, runID int64, err error) {
	m := runURLRe.FindStringSubmatch(runURL)
	if m == nil {
		return "", "", 0, fmt.Errorf("ghclient: cannot parse run URL %q", runURL)
	}
	id, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		return "", "", 0, err
	}
	return m[1], m[2], id, nil
}

const maxLogBytes = 32 << 10 // 32 KiB — enough for Fix prompt, not a dump

// FetchFailedJobLogs downloads log text for the first failed job in a
// workflow run, truncated to maxLogBytes. Returns empty string if no
// failed job or logs unavailable.
func (c *Client) FetchFailedJobLogs(ctx context.Context, runURL string) (string, error) {
	owner, repo, runID, err := ParseRunURL(runURL)
	if err != nil {
		return "", err
	}
	jobs, _, err := c.gh.Actions.ListWorkflowJobs(ctx, owner, repo, runID, &github.ListWorkflowJobsOptions{
		Filter:      "latest",
		ListOptions: github.ListOptions{PerPage: 50},
	})
	if err != nil {
		return "", fmt.Errorf("ghclient: list jobs for run %d: %w", runID, err)
	}
	var jobID int64
	for _, j := range jobs.Jobs {
		if j.GetConclusion() == "failure" || j.GetConclusion() == "timed_out" {
			jobID = j.GetID()
			break
		}
	}
	if jobID == 0 {
		return "", nil
	}
	// DownloadWorkflowJobLogs follows redirect to the zip/log URL.
	url, _, err := c.gh.Actions.GetWorkflowJobLogs(ctx, owner, repo, jobID, 3)
	if err != nil {
		return "", fmt.Errorf("ghclient: job logs URL: %w", err)
	}
	if url == nil {
		return "", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ghclient: download job logs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ghclient: download job logs: status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxLogBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(body) > maxLogBytes {
		body = append(body[:maxLogBytes], []byte("\n...(truncated)\n")...)
	}
	return string(body), nil
}
