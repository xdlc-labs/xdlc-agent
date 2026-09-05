package ghclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v90/github"
)

// PRRef is the subset of a pull request the Fix-PR work queue needs.
type PRRef struct {
	Number int
	URL    string
	State  string // "open" or "closed" (go-github's two PR states)
	Merged bool
}

// GetPR fetches a pull request by number on "owner/repo".
func (c *Client) GetPR(ctx context.Context, repo string, number int) (*PRRef, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("ghclient: repo %q must be \"owner/name\"", repo)
	}
	if number <= 0 {
		return nil, fmt.Errorf("ghclient: invalid PR number %d", number)
	}
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, name, number)
	if err != nil {
		return nil, fmt.Errorf("ghclient: get PR %s#%d: %w", repo, number, err)
	}
	return &PRRef{
		Number: pr.GetNumber(),
		URL:    pr.GetHTMLURL(),
		State:  pr.GetState(),
		Merged: pr.GetMerged(),
	}, nil
}

// FindPRByBranch looks up a PR whose head is branch on "owner/repo".
// Returns nil, nil (not an error) when no matching PR exists — a Fix
// run in "pr" mode may have failed to actually open one (e.g. left a
// BACKLOG.md note instead), which isn't a lookup failure.
func (c *Client) FindPRByBranch(ctx context.Context, repo, branch string) (*PRRef, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("ghclient: repo %q must be \"owner/name\"", repo)
	}
	prs, _, err := c.gh.PullRequests.List(ctx, owner, name, &github.PullRequestListOptions{
		State:       "all", // catch a PR that merged/closed between Fix finishing and this lookup
		Head:        owner + ":" + branch,
		ListOptions: github.ListOptions{PerPage: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("ghclient: list PRs for %s head %s: %w", repo, branch, err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	pr := prs[0]
	return &PRRef{
		Number: pr.GetNumber(),
		URL:    pr.GetHTMLURL(),
		State:  pr.GetState(),
		Merged: pr.GetMerged(),
	}, nil
}

// CreatePR opens a pull request from head → base on "owner/repo".
func (c *Client) CreatePR(ctx context.Context, repo, head, base, title, body string) (*PRRef, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return nil, fmt.Errorf("ghclient: repo %q must be \"owner/name\"", repo)
	}
	if head == "" || base == "" {
		return nil, fmt.Errorf("ghclient: head and base required")
	}
	if title == "" {
		title = "xdlc fix"
	}
	pr, _, err := c.gh.PullRequests.Create(ctx, owner, name, github.CreatePullRequest{
		Title: github.Ptr(title),
		Head:  head,
		Base:  base,
		Body:  github.Ptr(body),
	})
	if err != nil {
		return nil, fmt.Errorf("ghclient: create PR %s %s→%s: %w", repo, head, base, err)
	}
	return &PRRef{
		Number: pr.GetNumber(),
		URL:    pr.GetHTMLURL(),
		State:  pr.GetState(),
		Merged: pr.GetMerged(),
	}, nil
}
