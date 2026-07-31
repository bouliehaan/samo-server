package api

import (
	"github.com/bouliehaan/samo-server/internal/log"
	"html/template"
	"net/http"
)

// loginPage serves the sign-in screen. If the server is still in setup mode
// the page redirects to the wizard so users see one onboarding flow at a
// time.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeSetupStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status.NeedsSetup {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loginTemplate.Execute(w, nil); err != nil {
		log.Warnf("failed to render login page: %v", err)
	}
}

var loginTemplate = template.Must(template.New("login").Parse(pageSource("login")))
