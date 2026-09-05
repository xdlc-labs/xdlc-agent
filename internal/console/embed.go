// Package console embeds and serves the ops-console static build.
package console

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Mount registers the SPA at / when dist/index.html is present.
// No-op when the embed is empty (API-only daemon). Does not steal
// /api/* or /webhooks/* — those more-specific mux patterns win.
func Mount(mux *http.ServeMux) {
	if _, err := distFS.Open("dist/index.html"); err != nil {
		return
	}
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never claim API or webhook paths if somehow unmatched above.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if f, err := sub.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback → index.html
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}))
}
