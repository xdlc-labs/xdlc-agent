// Package httpgzip is a small gzip response middleware for the daemon's
// JSON API and the embedded console assets. The console's main chunk is
// ~320 KB of JavaScript that compresses 3:1, and /api/overview carries
// the whole BACKLOG.md; neither is worth shipping uncompressed to a
// browser that asked for gzip.
package httpgzip

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// minSize is the smallest body worth compressing. Anything shorter is
// written through untouched once the decision point is reached.
const minSize = 1024

var pool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

// Handler wraps next so that responses to clients advertising
// Accept-Encoding: gzip are compressed. Streams (text/event-stream),
// responses that already set Content-Encoding, and images are passed
// through untouched; so are bodies shorter than 1 KiB.
func Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		gw := &responseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if enc == "gzip" {
			return true
		}
	}
	return false
}

// responseWriter defers the compress/pass-through decision until the
// first Write so the Content-Type and Content-Length set by the handler
// are known. Small bodies are buffered up to minSize and flushed plain.
type responseWriter struct {
	http.ResponseWriter
	status  int
	decided bool
	gz      *gzip.Writer
	buf     []byte
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if w.decided {
		if w.gz != nil {
			return w.gz.Write(p)
		}
		return w.ResponseWriter.Write(p)
	}
	if !w.compressible() {
		w.decide(false)
		return w.ResponseWriter.Write(p)
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) < minSize {
		return len(p), nil
	}
	w.decide(true)
	buf := w.buf
	w.buf = nil
	if _, err := w.gz.Write(buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

// compressible reports whether the response the handler has described
// so far is worth gzipping.
func (w *responseWriter) compressible() bool {
	h := w.Header()
	if h.Get("Content-Encoding") != "" {
		return false
	}
	ct := h.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"),
		strings.HasPrefix(ct, "image/"),
		strings.HasPrefix(ct, "video/"),
		strings.HasPrefix(ct, "audio/"):
		return false
	}
	return true
}

func (w *responseWriter) decide(compress bool) {
	w.decided = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if compress {
		h := w.Header()
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		gz := pool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	w.ResponseWriter.WriteHeader(w.status)
}

// Flush satisfies http.Flusher so a handler that streams keeps working
// behind the middleware (the SSE handler is passed through undecided
// until its first write, which decides pass-through).
func (w *responseWriter) Flush() {
	if !w.decided {
		// Headers only so far: the handler is streaming. Commit to
		// pass-through so the flushed headers (and every later chunk)
		// reach the client immediately instead of sitting in a gzip block.
		w.decide(false)
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish writes out whatever is pending: a short buffered body goes
// plain, a compressed one gets its gzip trailer.
func (w *responseWriter) finish() {
	if !w.decided {
		w.decide(false)
		if len(w.buf) > 0 {
			_, _ = w.ResponseWriter.Write(w.buf)
		}
		w.buf = nil
		return
	}
	if w.gz != nil {
		_ = w.gz.Close()
		w.gz.Reset(io.Discard)
		pool.Put(w.gz)
		w.gz = nil
	}
}

// Unwrap exposes the underlying writer to http.ResponseController so
// handlers can still adjust deadlines through the middleware.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
