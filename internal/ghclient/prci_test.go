package ghclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const testSHA = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// ciFixture serves the three endpoints GetPR touches for one PR: the PR
// itself, its check runs and its combined commit status. Empty JSON
// stands for "this system reports nothing", which is what GitHub
// actually returns (check-runs total_count 0; combined status "pending"
// with total_count 0 — the shape that used to fake a permanent
// pending).
type ciFixture struct {
	checkRuns string // JSON body for /commits/{sha}/check-runs
	statuses  string // JSON body for /commits/{sha}/status
	hits      map[string]int
}

const (
	noCheckRuns = `{"total_count":0,"check_runs":[]}`
	noStatuses  = `{"state":"pending","total_count":0,"statuses":[]}`
)

func (f *ciFixture) client(t *testing.T) *Client {
	t.Helper()
	f.hits = map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/svc/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		f.hits["pr"]++
		_, _ = fmt.Fprintf(w, `{"number":7,"state":"open","title":"fix: thing","head":{"sha":%q}}`, testSHA)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		f.hits["check-runs"]++
		_, _ = fmt.Fprint(w, f.checkRuns)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/status", func(w http.ResponseWriter, _ *http.Request) {
		f.hits["status"]++
		_, _ = fmt.Fprint(w, f.statuses)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
		http.NotFound(w, r)
	})
	c, _ := testClient(t, mux)
	return c
}

// TestGetPRCIState is the #35 regression: PR check state came only from
// GetCombinedStatus, the legacy commit-statuses API, which GitHub
// Actions never writes to — so an Actions-only repo read as "pending"
// forever, indistinguishable from a repo with no CI at all.
func TestGetPRCIState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		checkRuns string
		statuses  string
		want      string
	}{{
		// Only check runs (the GitHub Actions case): used to be "pending".
		name:      "check runs only, all successful",
		checkRuns: `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"skipped"}]}`,
		statuses:  noStatuses,
		want:      "success",
	}, {
		name:      "check runs only, one still running",
		checkRuns: `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"in_progress"}]}`,
		statuses:  noStatuses,
		want:      "pending",
	}, {
		name:      "check runs only, one queued",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"queued"}]}`,
		statuses:  noStatuses,
		want:      "pending",
	}, {
		name:      "check runs only, failure",
		checkRuns: `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"failure"}]}`,
		statuses:  noStatuses,
		want:      "failure",
	}, {
		name:      "check runs only, timed out counts as failure",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"timed_out"}]}`,
		statuses:  noStatuses,
		want:      "failure",
	}, {
		name:      "check runs only, neutral and stale are not failures",
		checkRuns: `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"neutral"},{"status":"completed","conclusion":"stale"}]}`,
		statuses:  noStatuses,
		want:      "success",
	}, {
		// Only commit statuses (external CI / older integrations).
		name:      "statuses only, success",
		checkRuns: noCheckRuns,
		statuses:  `{"state":"success","total_count":1,"statuses":[{"state":"success"}]}`,
		want:      "success",
	}, {
		name:      "statuses only, pending",
		checkRuns: noCheckRuns,
		statuses:  `{"state":"pending","total_count":1,"statuses":[{"state":"pending"}]}`,
		want:      "pending",
	}, {
		name:      "statuses only, failure",
		checkRuns: noCheckRuns,
		statuses:  `{"state":"failure","total_count":1,"statuses":[{"state":"failure"}]}`,
		want:      "failure",
	}, {
		name:      "statuses only, error",
		checkRuns: noCheckRuns,
		statuses:  `{"state":"error","total_count":1,"statuses":[{"state":"error"}]}`,
		want:      "failure",
	}, {
		// Both systems present and disagreeing: worst state wins.
		name:      "both present, statuses green but a check run failed",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"failure"}]}`,
		statuses:  `{"state":"success","total_count":1,"statuses":[{"state":"success"}]}`,
		want:      "failure",
	}, {
		name:      "both present, check runs green but a status failed",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"success"}]}`,
		statuses:  `{"state":"failure","total_count":2,"statuses":[{"state":"failure"}]}`,
		want:      "failure",
	}, {
		name:      "both present, check runs green but statuses pending",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"success"}]}`,
		statuses:  `{"state":"pending","total_count":1,"statuses":[{"state":"pending"}]}`,
		want:      "pending",
	}, {
		name:      "both present, check run running but a status already failed",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"in_progress"}]}`,
		statuses:  `{"state":"failure","total_count":1,"statuses":[{"state":"failure"}]}`,
		want:      "failure",
	}, {
		name:      "both present, all green",
		checkRuns: `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"success"}]}`,
		statuses:  `{"state":"success","total_count":1,"statuses":[{"state":"success"}]}`,
		want:      "success",
	}, {
		// Neither system reports: unknown, NOT pending. GitHub answers
		// the combined-status endpoint with state "pending" here, and
		// reporting that verbatim was half of #35.
		name:      "neither present is unknown, not pending",
		checkRuns: noCheckRuns,
		statuses:  noStatuses,
		want:      "",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &ciFixture{checkRuns: tc.checkRuns, statuses: tc.statuses}
			pr, err := f.client(t).GetPR(context.Background(), "acme/svc", 7)
			if err != nil {
				t.Fatal(err)
			}
			if pr.CI != tc.want {
				t.Errorf("CI = %q, want %q", pr.CI, tc.want)
			}
			// Bounded request count: one PR read plus at most one page
			// of check runs plus one combined status.
			if f.hits["check-runs"] > maxCheckRunPages || f.hits["status"] > 1 || f.hits["pr"] != 1 {
				t.Errorf("request counts = %v", f.hits)
			}
		})
	}
}

// TestGetPRCIFailureShortCircuitsPaging: a failure on the first page
// must not keep paging — the PR queue reads one PR per row under a
// shared timeout.
func TestGetPRCIFailureShortCircuitsPaging(t *testing.T) {
	var checkRunPages, statusHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/svc/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"number":7,"state":"open","head":{"sha":%q}}`, testSHA)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/check-runs", func(w http.ResponseWriter, r *http.Request) {
		checkRunPages++
		if got := r.URL.Query().Get("filter"); got != "latest" {
			t.Errorf("filter = %q, want latest", got)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100", got)
		}
		w.Header().Set("Link", `<`+r.URL.String()+`&page=2>; rel="next"`)
		_, _ = fmt.Fprint(w, `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"failure"}]}`)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/status", func(w http.ResponseWriter, _ *http.Request) {
		statusHits++
		_, _ = fmt.Fprint(w, noStatuses)
	})
	c, _ := testClient(t, mux)

	pr, err := c.GetPR(context.Background(), "acme/svc", 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.CI != "failure" {
		t.Errorf("CI = %q, want failure", pr.CI)
	}
	if checkRunPages != 1 {
		t.Errorf("check-run pages = %d, want 1 (failure short-circuits)", checkRunPages)
	}
	if statusHits != 0 {
		t.Errorf("combined status requests = %d, want 0 (failure short-circuits)", statusHits)
	}
}

// TestGetPRCIPagesOnceMore: a full first page with a next link is read
// on, but never past maxCheckRunPages.
func TestGetPRCIPagesBounded(t *testing.T) {
	var pages int
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/svc/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"number":7,"state":"open","head":{"sha":%q}}`, testSHA)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/check-runs", func(w http.ResponseWriter, r *http.Request) {
		pages++
		// Always advertise another page: the loop must stop itself.
		w.Header().Set("Link", `<`+strings.Split(r.URL.String(), "&page=")[0]+`&page=`+fmt.Sprint(pages+1)+`>; rel="next"`)
		conclusion := "success"
		if pages == 2 {
			conclusion = "neutral"
		}
		_, _ = fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"status":"completed","conclusion":%q}]}`, conclusion)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, noStatuses)
	})
	c, _ := testClient(t, mux)

	pr, err := c.GetPR(context.Background(), "acme/svc", 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.CI != "success" {
		t.Errorf("CI = %q, want success", pr.CI)
	}
	if pages != maxCheckRunPages {
		t.Errorf("check-run pages = %d, want %d", pages, maxCheckRunPages)
	}
}

// TestGetPRCILookupErrorsAreUnknown: CI is best-effort decoration on a
// work-queue row, so a 500 from either system must leave CI unknown
// rather than failing GetPR.
func TestGetPRCILookupErrorsAreUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/svc/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"number":7,"state":"open","head":{"sha":%q}}`, testSHA)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/check-runs", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	})
	mux.HandleFunc("/repos/acme/svc/commits/"+testSHA+"/status", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	})
	c, _ := testClient(t, mux)

	pr, err := c.GetPR(context.Background(), "acme/svc", 7)
	if err != nil {
		t.Fatalf("GetPR must not fail on a CI lookup error: %v", err)
	}
	if pr.CI != "" {
		t.Errorf("CI = %q, want %q", pr.CI, "")
	}
}

// TestGetPRNoHeadSHASkipsCILookup: no head SHA, no CI requests.
func TestGetPRNoHeadSHASkipsCILookup(t *testing.T) {
	var ciHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/svc/pulls/7", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"number":7,"state":"open"}`)
	})
	mux.HandleFunc("/repos/acme/svc/commits/", func(w http.ResponseWriter, _ *http.Request) {
		ciHits++
		_, _ = fmt.Fprint(w, `{}`)
	})
	c, _ := testClient(t, mux)

	pr, err := c.GetPR(context.Background(), "acme/svc", 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.CI != "" || ciHits != 0 {
		t.Errorf("CI = %q, ciHits = %d", pr.CI, ciHits)
	}
}
