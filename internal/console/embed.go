// Package console embeds and serves the ops-console static build.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/xdlc-labs/xdlc-agent/internal/httpgzip"
)

//go:embed all:dist
var distFS embed.FS

// Mount registers the SPA at / when dist/index.html is present.
// No-op when the embed is empty (API-only daemon). Does not steal
// /api/* or /webhooks/* — those more-specific mux patterns win.
//
// Vite writes every file under assets/ with a content hash in its name,
// so those are served immutable for a year; index.html (which names the
// current hashes) and the other root files are always revalidated. An
// embed.FS has no modtimes, so without these headers the browser had no
// way to cache the ~450 KB of JavaScript between console loads.
func Mount(mux *http.ServeMux) {
	if _, err := distFS.Open("dist/index.html"); err != nil {
		return
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", httpgzip.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never claim API or webhook paths if somehow unmatched above.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := sub.Open(path); err == nil {
				_ = f.Close()
				if strings.HasPrefix(path, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback → index.html
		w.Header().Set("Cache-Control", "no-cache")
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})))
}
