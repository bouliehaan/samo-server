package api

import (
	"github.com/bouliehaan/samo-server/internal/log"
	"html/template"
	"net/http"
)

// setupPage serves the first-run wizard. Once setup is complete, visitors are
// redirected back to the dashboard so the wizard doesn't become a permanent
// /setup entry point.
func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !status.NeedsSetup {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := setupTemplate.Execute(w, nil); err != nil {
		log.Warnf("failed to render setup page: %v", err)
	}
}

var setupTemplate = template.Must(template.New("setup").Parse(pageSource("setup")))
