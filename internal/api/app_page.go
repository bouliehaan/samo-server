package api

import (
	"github.com/bouliehaan/samo-server/internal/log"
	"html/template"
	"net/http"
)

// appPage serves the authenticated dashboard shell. Setup-pending visitors
// get redirected to the wizard; everyone else gets the same HTML and the JS
// decides whether to render content or punt to /login.
func (s *Server) appPage(w http.ResponseWriter, r *http.Request) {
	if status, err := s.computeSetupStatus(r.Context()); err == nil && status.NeedsSetup {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := appTemplate.Execute(w, nil); err != nil {
		log.Warnf("failed to render app page: %v", err)
	}
}

var appTemplate = template.Must(template.New("app").Parse(pageSource("app")))
