package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/users"
)

// issueStreamToken mints a short-lived credential the dashboard can put in
// stream/cover URLs so HTML5 audio/img elements don't need to send the
// bearer header (which they can't). The caller must already be
// authenticated via the standard requireUser path.
func (s *Server) issueStreamToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	service := s.usersService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	token, expiresAt, err := service.IssueStreamToken(principal.User.ID)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": expiresAt,
	})
}

func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {
	service := s.usersService()
	if service == nil || !service.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "user accounts are not configured")
		return
	}
	var input users.LoginInput
	if !readJSONBody(w, r, &input) {
		return
	}
	response, err := service.Login(r.Context(), input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getCurrentUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, principal.User)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	items, err := s.usersService().List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var input users.CreateUserInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.usersService().Create(r.Context(), principal, input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateCurrentUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input users.UpdateUserInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.usersService().Update(r.Context(), principal, principal.User.ID, input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listUserTokens(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	items, err := s.usersService().ListTokens(r.Context(), principal)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createUserToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var input users.CreateTokenInput
	if !readJSONBody(w, r, &input) {
		return
	}
	issued, err := s.usersService().IssueToken(r.Context(), principal, input)
	if err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, issued)
}

func (s *Server) revokeUserToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.usersService().RevokeToken(r.Context(), principal, r.PathValue("id")); err != nil {
		writeUserError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

func writeUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, users.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, users.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, users.ErrInvalidUsername), errors.Is(err, users.ErrInvalidPassword), errors.Is(err, users.ErrInvalidToken):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, users.ErrUsernameTaken):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
