package main

import (
	"encoding/hex"
	"net/http"
)

const desktopStartupProofHeader = "X-NewAPI-Desktop-Startup"

// Packaged Electron must not mistake an existing listener for its own child.
// This proof is emitted only for its loopback readiness probe.
func desktopStartupHandler(next http.Handler, bind, proof string) http.Handler {
	if bind != "127.0.0.1" || len(proof) != 48 {
		return next
	}
	if _, err := hex.DecodeString(proof); err != nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/readyz" {
			w.Header().Set(desktopStartupProofHeader, proof)
		}
		next.ServeHTTP(w, r)
	})
}
