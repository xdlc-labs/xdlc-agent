package promclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "up" {
			t.Errorf("query = %q", r.URL.Query().Get("query"))
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"42.5"]}]}}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL)
	v, err := c.Query(context.Background(), "up")
	if err != nil {
		t.Fatal(err)
	}
	if v != 42.5 {
		t.Fatalf("v = %v, want 42.5", v)
	}
}

// TestQueryEmptyResult: a query that matched no series must report
// ErrNoData, not 0. Returning 0 made the prod-health gate — whose only
// failure condition is value > threshold — read a renamed metric, a
// dead exporter or a typo'd query as a perfectly healthy service, and
// silently stop detecting breaches (issue #48).
func TestQueryEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(srv.Close)

	v, err := New(srv.URL).Query(context.Background(), "absent_metric")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("err = %v, want ErrNoData", err)
	}
	if v != 0 {
		t.Fatalf("v = %v, want the zero value alongside the error", v)
	}
	// The text lands in a BACKLOG.md line and an audit row, so it has to
	// name the query that came back empty.
	if !strings.Contains(err.Error(), "absent_metric") {
		t.Fatalf("error does not name the query: %v", err)
	}
}

// TestQueryGenuineZero: a series that exists and holds 0 is data, not
// missing data. An error-rate query with zero errors in the window is
// the everyday case, and it must stay a plain 0, nil — otherwise the
// #48 fix would block the gate on every healthy service.
func TestQueryGenuineZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"0"]}]}}`))
	}))
	t.Cleanup(srv.Close)

	v, err := New(srv.URL).Query(context.Background(), "err_rate")
	if err != nil {
		t.Fatalf("genuine zero returned an error: %v", err)
	}
	if v != 0 {
		t.Fatalf("v = %v, want 0", v)
	}
}

func TestQueryErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	if _, err := New(srv.URL).Query(context.Background(), "up"); err == nil {
		t.Fatal("expected error")
	}
}

// TestNewHasTimeout: http.DefaultClient has no Timeout, so a Prometheus
// that accepts the connection and never answers used to park the caller
// forever — which, for the prod-health poller, silently disabled the
// gate instead of failing it.
func TestNewHasTimeout(t *testing.T) {
	if got := New("http://prom.local").HTTP.Timeout; got != DefaultTimeout {
		t.Fatalf("New(...).HTTP.Timeout = %v, want %v", got, DefaultTimeout)
	}
	if New("http://prom.local").HTTP == http.DefaultClient {
		t.Fatal("New returned http.DefaultClient, which has no timeout")
	}
}

// TestQueryZeroValueClientHasTimeout: a Client built as a struct literal
// must not fall back to a client without a deadline either.
func TestQueryZeroValueClientHasTimeout(t *testing.T) {
	c := &Client{BaseURL: "http://prom.local"}
	if c.httpClient().Timeout <= 0 {
		t.Fatalf("zero-value Client queries with no timeout (%v)", c.httpClient().Timeout)
	}
}

// TestQueryHungServer: against a listener that accepts the connection
// and never answers, Query must return an error rather than block. The
// bound here comes from the caller's context — the poller's per-tick
// deadline — which is tighter than DefaultTimeout.
func TestQueryHungServer(t *testing.T) {
	hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(hung.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := New(hung.URL).Query(ctx, "up"); err == nil {
		t.Fatal("hung server returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Query was not bounded: took %v", elapsed)
	}
}
