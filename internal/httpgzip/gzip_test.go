package httpgzip

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.Handler, accept string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	rec := httptest.NewRecorder()
	Handler(h).ServeHTTP(rec, req)
	return rec
}

func TestLargeJSONIsGzipped(t *testing.T) {
	body := strings.Repeat(`{"k":"v"},`, 1000)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	rec := serve(t, h, "gzip, deflate, br")
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary = %q", rec.Header().Get("Vary"))
	}
	if rec.Body.Len() >= len(body) {
		t.Fatalf("compressed body %d not smaller than %d", rec.Body.Len(), len(body))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatal("round-trip mismatch")
	}
}

func TestSmallBodyPassesThroughPlain(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	rec := serve(t, h, "gzip")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("small body should not be compressed")
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestNoAcceptEncodingUntouched(t *testing.T) {
	body := strings.Repeat("x", 4096)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	rec := serve(t, h, "")
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != body {
		t.Fatal("request without Accept-Encoding must be passed through")
	}
}

func TestEventStreamNeverCompressed(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: "+strings.Repeat("y", 3000)+"\n\n")
		w.(http.Flusher).Flush()
	})
	rec := serve(t, h, "gzip")
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("SSE must not be gzipped")
	}
	if !rec.Flushed {
		t.Fatal("flush did not reach the underlying writer")
	}
	if !strings.HasPrefix(rec.Body.String(), "data: ") {
		t.Fatalf("body = %q", rec.Body.String()[:20])
	}
}

func TestImagePassesThrough(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, 5000))
	})
	rec := serve(t, h, "gzip")
	if rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != 5000 {
		t.Fatal("images must pass through")
	}
}

func TestEmptyBodyKeepsStatus(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := serve(t, h, "gzip")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status %d body %d", rec.Code, rec.Body.Len())
	}
}
