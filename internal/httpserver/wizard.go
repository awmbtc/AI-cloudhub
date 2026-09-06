package httpserver

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed static/wizard.html
var wizardHTML []byte

// handleWizard serves the embedded Phase-0 ops wizard (Chinese UI, vanilla JS).
// Mounted at /wizard and /app. JSON API clients keep using /v1/* unchanged.
func (s *Server) handleWizard(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path != "/wizard" && path != "/app" && path != "/wizard/" && path != "/app/" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// Normalize trailing slash → canonical path without redirect body noise for SPA.
	if strings.HasSuffix(path, "/") && len(path) > 1 {
		http.Redirect(w, r, strings.TrimSuffix(path, "/"), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(wizardHTML)
}
