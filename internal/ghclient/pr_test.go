package ghclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFindPRByBranchFound(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/svc/pulls" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("head"); got != "acme:xdlc-fix-1" {
			t.Errorf("head = %q, want acme:xdlc-fix-1", got)
		}
		_, _ = fmt.Fprint(w, `[{"number":7,"html_url":"https://github.com/acme/svc/pull/7","state":"open","merged":false}]`)
	}))
	pr, err := c.FindPRByBranch(context.Background(), "acme/svc", "xdlc-fix-1")
	if err != nil {
		t.Fatal(err)
	}
	if pr == nil || pr.Number != 7 || pr.State != "open" || pr.Merged {
		t.Fatalf("pr = %+v", pr)
	}
}

func TestFindPRByBranchNotFound(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `[]`)
	}))
	pr, err := c.FindPRByBranch(context.Background(), "acme/svc", "no-such-branch")
	if err != nil {
		t.Fatal(err)
	}
	if pr != nil {
		t.Fatalf("expected nil (no match, not an error), got %+v", pr)
	}
}

func TestFindPRByBranchBadRepo(t *testing.T) {
	c := New("")
	if _, err := c.FindPRByBranch(context.Background(), "noslash", "branch"); err == nil {
		t.Fatal("expected error for malformed repo")
	} else if !strings.Contains(err.Error(), "owner/name") {
		t.Errorf("error = %v", err)
	}
}
