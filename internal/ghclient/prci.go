package ghclient

import (
	"context"

	"github.com/google/go-github/v90/github"
)

// CI state vocabulary for PRRef.CI. These are the strings GitHub's
// combined-status API already produced, so api.PRLiveStatus, the
// /api/prs row and the console keep rendering what they render today.
const (
	ciUnknown = ""        // no check run and no commit status exists — CI is not configured for this SHA
	ciPending = "pending" // at least one check/status is queued or still running
	ciSuccess = "success" // every check/status finished without failing
	ciFailure = "failure" // at least one check/status failed
)

// failingConclusions are the check-run conclusions that block a merge.
// neutral, skipped and stale are deliberately absent: they finished
// without failing, so they must not turn a green PR red.
var failingConclusions = map[string]bool{
	"failure":         true,
	"timed_out":       true,
	"cancelled":       true,
	"action_required": true,
}

// checkRunPageSize and maxCheckRunPages bound the check-run read. GetPR
// runs once per row of the Fix-PR work queue under a shared few-second
// budget, so this has to stay a fixed, small number of requests: with
// filter=latest (one run per check name) 200 runs covers any realistic
// PR, and a failure short-circuits the paging anyway.
const (
	checkRunPageSize = 100
	maxCheckRunPages = 2
)

// ciState reports the aggregate CI state for sha, merging GitHub's two
// independent — and coexisting — check systems:
//
//   - check runs (Checks API), where GitHub Actions and modern apps
//     report. These never appear in combined status, which is why an
//     Actions-only repo used to read as a permanent "pending" (#35).
//   - commit statuses (legacy statuses API), where external CI and
//     older integrations report.
//
// A repo can legitimately have both at once, so both are read and
// aggregated worst-state-wins:
//
//  1. any failing signal           → "failure" (check-run conclusion in
//     failingConclusions, or combined status state failure/error)
//  2. else any unfinished signal   → "pending" (check run whose status
//     is not yet "completed", or combined status state "pending")
//  3. else at least one signal     → "success"
//  4. no signal from either source → "" (unknown)
//
// Rule 4 is the other half of #35: "no CI configured" and "CI is still
// running" both arrived as "pending", so the console could not tell
// them apart. Unknown renders as "—" in the PR queue. Lookup errors
// also yield "" — CI here is best-effort and must never fail a queue
// row.
func (c *Client) ciState(ctx context.Context, owner, name, sha string) string {
	var sawSignal, sawPending bool

	opts := &github.ListCheckRunsOptions{
		Filter:      github.Ptr("latest"),
		ListOptions: github.ListOptions{PerPage: checkRunPageSize},
	}
	for page := 0; page < maxCheckRunPages; page++ {
		runs, resp, err := c.gh.Checks.ListCheckRunsForRef(ctx, owner, name, sha, opts)
		if err != nil {
			break // best-effort: still try commit statuses
		}
		for _, run := range runs.GetCheckRuns() {
			if run == nil {
				continue
			}
			sawSignal = true
			switch {
			case run.GetStatus() != "completed":
				sawPending = true
			case failingConclusions[run.GetConclusion()]:
				return ciFailure // rule 1: a failure dominates, stop paging
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	// The combined endpoint aggregates every commit status on the SHA
	// server-side, so one request with PerPage: 1 is enough. TotalCount
	// is what separates "no statuses" from "statuses pending": the
	// endpoint reports state "pending" for a SHA with zero statuses.
	if st, _, err := c.gh.Repositories.GetCombinedStatus(ctx, owner, name, sha, &github.ListOptions{PerPage: 1}); err == nil && st.GetTotalCount() > 0 {
		sawSignal = true
		switch st.GetState() {
		case ciFailure, "error":
			return ciFailure
		case ciPending:
			sawPending = true
		}
	}

	switch {
	case sawPending:
		return ciPending
	case sawSignal:
		return ciSuccess
	default:
		return ciUnknown
	}
}
