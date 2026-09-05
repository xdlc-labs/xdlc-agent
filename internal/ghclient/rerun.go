package ghclient

import (
	"context"
	"fmt"
	"time"
)

// RerunFailedJobs triggers GitHub Actions rerun-failed-jobs for the
// workflow run in runURL (issue #3).
func (c *Client) RerunFailedJobs(ctx context.Context, runURL string) error {
	owner, repo, runID, err := ParseRunURL(runURL)
	if err != nil {
		return err
	}
	_, err = c.gh.Actions.RerunFailedJobsByID(ctx, owner, repo, runID)
	if err != nil {
		return fmt.Errorf("ghclient: rerun failed jobs %s: %w", runURL, err)
	}
	return nil
}

// WaitRunConclusion polls the workflow run until it leaves queued/in_progress
// or ctx/timeout expires. Returns the final conclusion (success, failure, …).
func (c *Client) WaitRunConclusion(ctx context.Context, runURL string, interval, timeout time.Duration) (string, error) {
	owner, repo, runID, err := ParseRunURL(runURL)
	if err != nil {
		return "", err
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		run, _, err := c.gh.Actions.GetWorkflowRunByID(ctx, owner, repo, runID)
		if err != nil {
			return "", fmt.Errorf("ghclient: get workflow run %d: %w", runID, err)
		}
		status := run.GetStatus() // queued | in_progress | completed
		if status == "completed" || status == "" && run.GetConclusion() != "" {
			return run.GetConclusion(), nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("ghclient: wait run %d: timeout (status=%s)", runID, status)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

// RerunAndWait reruns failed jobs then waits for a terminal conclusion.
// green is true only when conclusion == "success".
func (c *Client) RerunAndWait(ctx context.Context, runURL string) (green bool, conclusion string, err error) {
	if err := c.RerunFailedJobs(ctx, runURL); err != nil {
		return false, "", err
	}
	// Brief pause so GitHub marks the run in_progress before we poll.
	select {
	case <-ctx.Done():
		return false, "", ctx.Err()
	case <-time.After(2 * time.Second):
	}
	conclusion, err = c.WaitRunConclusion(ctx, runURL, 5*time.Second, 10*time.Minute)
	if err != nil {
		return false, conclusion, err
	}
	return conclusion == "success", conclusion, nil
}
