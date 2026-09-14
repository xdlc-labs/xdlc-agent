package console

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embedded dist is the committed console build; these assertions
// only need index.html plus one hashed asset to exist.
func TestMountCacheHeadersAndGzip(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	idx := get("/")
	if idx.Code != http.StatusOK {
		t.Skip("no console build embedded")
	}
	if cc := idx.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index Cache-Control = %q", cc)
	}
	// Deep link falls back to index.html with the same policy.
	if deep := get("/repos/some-id"); deep.Code != http.StatusOK || deep.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("SPA fallback: %d %q", deep.Code, deep.Header().Get("Cache-Control"))
	}
	// Pull a hashed asset path out of index.html.
	body := idx.Body.String()
	if idx.Header().Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(idx.Body)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		body = string(raw)
	}
	i := strings.Index(body, "/assets/")
	if i < 0 {
		t.Fatal("index.html references no /assets/")
	}
	end := strings.IndexAny(body[i:], `"'`)
	asset := body[i : i+end]

	res := get(asset)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: %d", asset, res.Code)
	}
	if cc := res.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset Cache-Control = %q", cc)
	}
	if res.Body.Len() >= 1024 && res.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("asset %s (%d bytes) not gzipped", asset, res.Body.Len())
	}
	if api := get("/api/nope"); api.Code != http.StatusNotFound {
		t.Fatalf("/api/* through console mount = %d", api.Code)
	}
}
