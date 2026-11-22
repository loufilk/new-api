package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopStartupProofOnlyForLoopbackReadiness(t *testing.T) {
	proof := strings.Repeat("12", 24)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	for _, tt := range []struct {
		name, method, path, host, proof, want string
	}{
		{"loopback", "GET", "/api/readyz", "127.0.0.1", proof, proof},
		{"status", "GET", "/api/status", "127.0.0.1", proof, ""},
		{"head", "HEAD", "/api/readyz", "127.0.0.1", proof, ""},
		{"public bind", "GET", "/api/readyz", "0.0.0.0", proof, ""},
		{"empty proof", "GET", "/api/readyz", "127.0.0.1", "", ""},
		{"invalid proof", "GET", "/api/readyz", "127.0.0.1", strings.Repeat("~", 48), ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			desktopStartupHandler(next, tt.host, tt.proof).ServeHTTP(res, req)
			if got := res.Header().Get(desktopStartupProofHeader); got != tt.want {
				t.Fatalf("unexpected proof: %q", got)
			}
			if res.Code != http.StatusServiceUnavailable {
				t.Fatalf("startup proof must not override readiness, status %d", res.Code)
			}
		})
	}
}
