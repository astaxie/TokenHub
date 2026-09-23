package server

import "net/http"

// NewStartupBlockedHandler keeps the process live for orchestrators while
// refusing readiness and application traffic until an operator supplies a
// configuration that can be loaded safely on restart.
func NewStartupBlockedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"status":  "configuration_required",
			"service": "tokenhub-backend",
		}
		probe := r.URL.Path == "/livez" || r.URL.Path == "/readyz" || r.URL.Path == "/healthz"
		if probe && r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSON(w, http.StatusMethodNotAllowed, payload)
			return
		}
		if r.URL.Path == "/livez" {
			writeJSON(w, http.StatusOK, payload)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, payload)
	})
}
