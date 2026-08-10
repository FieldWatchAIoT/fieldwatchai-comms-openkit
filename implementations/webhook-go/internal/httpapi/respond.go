package httpapi

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes v as a JSON response with the given status code. Encoding
// errors are swallowed deliberately: the header is already committed by the
// time Encode could fail, so there is nothing useful left to signal to the
// client.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
