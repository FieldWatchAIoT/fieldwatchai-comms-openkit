package httpapi

import "net/http"

// WithBodyCap is middleware that bounds the size of an inbound request body to
// maxBytes. A cap of 0 (or negative) disables enforcement.
//
// Enforcement is two-layered: a declared Content-Length over the cap is
// rejected with 413 before the downstream handler runs (no bytes read), and
// the body is additionally wrapped in http.MaxBytesReader so a body that lies
// about or omits its length is still bounded when the handler reads it.
func WithBodyCap(maxBytes int64, next http.Handler) http.Handler {
	if maxBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"status": "payload_too_large"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}
