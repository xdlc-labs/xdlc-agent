package ghclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v90/github"
)

func testClient(t *testing.T, h http.Handler) (*Client, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	base := srv.URL + "/"
	gh, err := github.NewClient(github.WithURLs(&base, &base))
	if err != nil {
		t.Fatal(err)
	}
	return &Client{gh: gh, Branch: "develop"}, srv.URL
}

func TestNew(t *testing.T) {
	c := New("")
	if c.Branch != "develop" || c.gh == nil {
		t.Fatalf("%+v", c)
	}
	c2 := New("tok")
	if c2.Branch != "develop" || c2.gh == nil {
		t.Fatalf("%+v", c2)
	}
}

func TestGetPR(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/svc/pulls/7" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"number":7,"html_url":"https://github.com/acme/svc/pull/7","state":"closed","merged":true}`)
	}))
	pr, err := c.GetPR(context.Background(), "acme/svc", 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || pr.State != "closed" || !pr.Merged {
		t.Fatalf("%+v", pr)
	}
}

func TestGetPRBadInput(t *testing.T) {
	c := New("")
	if _, err := c.GetPR(context.Background(), "noslash", 1); err == nil {
		t.Fatal("expected error")
	}
	if _, err := c.GetPR(context.Background(), "acme/svc", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetStatus(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/svc/actions/runs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("branch") != "develop" {
			t.Errorf("branch = %q", r.URL.Query().Get("branch"))
		}
		_, _ = fmt.Fprint(w, `{"total_count":1,"workflow_runs":[{"id":9,"conclusion":"success","html_url":"https://github.com/acme/svc/actions/runs/9"}]}`)
	}))
	conclusion, runURL, err := c.GetStatus(context.Background(), "acme/svc")
	if err != nil {
		t.Fatal(err)
	}
	if conclusion != "success" || !strings.Contains(runURL, "/runs/9") {
		t.Fatalf("%q %q", conclusion, runURL)
	}
}

// TestGetStatusPerRepoBranch is the C2 regression at the gate: the
// branch the CI gate reads was one global value, so a repo configured
// with `branch: main` was polled for workflow runs on a develop branch
// it doesn't have — no runs, no CI signal, ever.
func TestGetStatusPerRepoBranch(t *testing.T) {
	var queried []string
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queried = append(queried, r.URL.Path+"?branch="+r.URL.Query().Get("branch"))
		_, _ = fmt.Fprint(w, `{"total_count":1,"workflow_runs":[{"id":9,"conclusion":"success","html_url":"https://github.com/acme/svc/actions/runs/9"}]}`)
	}))
	c.BranchFor = func(ownerRepo string) string {
		if ownerRepo == "acme/trunk" {
			return "main"
		}
		return "" // unset → fall back to Client.Branch
	}

	for _, repo := range []string{"acme/trunk", "acme/svc"} {
		if _, _, err := c.GetStatus(context.Background(), repo); err != nil {
			t.Fatalf("GetStatus(%s): %v", repo, err)
		}
	}

	want := []string{
		"/repos/acme/trunk/actions/runs?branch=main",
		"/repos/acme/svc/actions/runs?branch=develop",
	}
	if len(queried) != len(want) {
		t.Fatalf("queried %v, want %v", queried, want)
	}
	for i := range want {
		if queried[i] != want[i] {
			t.Errorf("query[%d] = %q, want %q", i, queried[i], want[i])
		}
	}
}

func TestGetStatusBadRepo(t *testing.T) {
	c := New("")
	if _, _, err := c.GetStatus(context.Background(), "noslash"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetStatusNoRuns(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"total_count":0,"workflow_runs":[]}`)
	}))
	if _, _, err := c.GetStatus(context.Background(), "acme/svc"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchFailedJobLogs(t *testing.T) {
	var logHits int
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/svc/actions/runs/99/jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"total_count":2,"jobs":[
			{"id":1,"conclusion":"success"},
			{"id":2,"conclusion":"failure"}
		]}`)
	})
	mux.HandleFunc("/repos/acme/svc/actions/jobs/2/logs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/download-logs", http.StatusFound)
	})
	mux.HandleFunc("/download-logs", func(w http.ResponseWriter, _ *http.Request) {
		logHits++
		_, _ = fmt.Fprint(w, "line1\nFAIL here\n")
	})
	c, base := testClient(t, mux)

	logs, err := c.FetchFailedJobLogs(context.Background(), "https://github.com/acme/svc/actions/runs/99")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "FAIL here") || logHits != 1 {
		t.Fatalf("logs=%q hits=%d", logs, logHits)
	}
}

func TestFetchFailedJobLogsNoFailure(t *testing.T) {
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/jobs") && !strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = fmt.Fprint(w, `{"total_count":1,"jobs":[{"id":1,"conclusion":"success"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	logs, err := c.FetchFailedJobLogs(context.Background(), "https://github.com/acme/svc/actions/runs/1")
	if err != nil {
		t.Fatal(err)
	}
	if logs != "" {
		t.Fatalf("logs = %q", logs)
	}
}

// rewriteHost sends api.github.com traffic to an httptest server.
type rewriteHost struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	req.Host = r.target.Host
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func TestAppToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/42/access_tokens" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		hits++
		_, _ = fmt.Fprint(w, `{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	a := &AppToken{
		AppID:          7,
		InstallationID: 42,
		PrivateKey:     key,
		HTTP:           &http.Client{Transport: rewriteHost{target: target}},
	}
	tok, err := a.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_test" || hits != 1 {
		t.Fatalf("tok=%q hits=%d", tok, hits)
	}
	// cached — no second mint
	tok2, err := a.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok2 != "ghs_test" || hits != 1 {
		t.Fatalf("cache miss: tok=%q hits=%d", tok2, hits)
	}
	// force refresh
	a.mu.Lock()
	a.expiry = time.Now().Add(-time.Hour)
	a.mu.Unlock()
	if _, err := a.Token(); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d after expiry", hits)
	}
}
