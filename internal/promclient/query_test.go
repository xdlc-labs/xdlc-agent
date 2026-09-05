package promclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestQueryEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(srv.Close)

	v, err := New(srv.URL).Query(context.Background(), "none")
	if err != nil {
		t.Fatal(err)
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
