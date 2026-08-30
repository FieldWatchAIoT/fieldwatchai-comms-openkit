// Package console serves a single read-only HTML page for operators.
//
// It exists because the pipeline was previously only observable through psql
// and container logs. An operator could follow the setup correctly and still
// have no way to answer "is anything arriving, and did the system understand
// it?" — which is the question that decides whether a deployment is trusted.
//
// Scope is deliberately narrow. It renders what /v1/diagnostics and
// /v1/messages already return and writes nothing. A full operator UI is a
// product each agency will want to shape itself; this is the minimum needed to
// see the system working.
package console

import (
	_ "embed"
	"net/http"
)

// page is compiled into the binary so the container stays a single static file
// with no asset directory to mount or serve.
//
//go:embed console.html
var page []byte

// RegisterRoutes mounts the console.
//
// The page itself is served unauthenticated on purpose: it contains no data,
// only markup. It asks for the internal token in the browser and calls the
// authenticated JSON endpoints with it, so nothing is readable without the same
// credential the API already requires. Gating the shell would mean either a
// second auth scheme or basic-auth, and neither buys anything given the HTML is
// public in this repository anyway.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /console", serve)
}

func serve(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page loads no external resources; say so, so a stray future <script
	// src> or webfont fails loudly here rather than silently degrading in the
	// field. 'unsafe-inline' covers the inline style and script blocks.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(page)
}
