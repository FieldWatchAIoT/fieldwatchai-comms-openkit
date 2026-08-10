// Command fakechannels is a local stand-in for fieldwatchai-comms-channels. It
// logs every inbound POST (path, idempotency key, whether auth was present, and
// the body) and returns 201, so the comms-webhook drain worker can be exercised
// end-to-end without the real channels repo. Local dev only.
package main

import (
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}
	// Stub the account lookup so local end-to-end works without real channels.
	http.HandleFunc("/v1/accounts/lookup", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("lookup type=%q identifier=%q", r.URL.Query().Get("type"), r.URL.Query().Get("identifier"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"account_id":"acc_local","tenant_id":"t_local"}`))
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Note: we log whether auth was present, never its value.
		log.Printf("inbound %s %s idempotency-key=%q auth_present=%t body=%s",
			r.Method, r.URL.Path,
			r.Header.Get("Idempotency-Key"),
			r.Header.Get("Authorization") != "",
			string(body),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"created"}`))
	})
	log.Printf("fakechannels listening on :%s (POST /v1/ingest)", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
