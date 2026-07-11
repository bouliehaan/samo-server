package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/explo"
)

func (s *Server) exploService() *explo.Service {
	return s.explo
}

func (s *Server) requireExploService(w http.ResponseWriter) (*explo.Service, bool) {
	service := s.exploService()
	if service == nil {
		writeError(w, http.StatusServiceUnavailable, "explo integration is not available")
		return nil, false
	}
	return service, true
}

func (s *Server) getExploConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	service, ok := s.requireExploService(w)
	if !ok {
		return
	}
	config, err := service.Config(r.Context())
	if err != nil {
		writeExploError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) updateExploConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	service, ok := s.requireExploService(w)
	if !ok {
		return
	}
	var input explo.AppConfigInput
	if !readJSONBody(w, r, &input) {
		return
	}
	if folder := strings.TrimSpace(input.Folder); folder != "" {
		if err := validateExploFolder(folder); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	config, err := service.SaveConfig(r.Context(), input)
	if err != nil {
		writeExploError(w, err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		// First re-sync hidden flags / ledger / playlist to the new folder
		// (fast, path-based) so a narrowed folder recovers Recently Added
		// immediately. Then run the slow, rate-limited identification pass.
		if err := service.ReconcileRecentlyAdded(ctx); err != nil {
			log.Printf("explo: reconcile after config save failed: %v", err)
		}
		if service.Enabled() {
			if _, err := service.ProcessNewTracks(ctx); err != nil {
				log.Printf("explo: process after config save failed: %v", err)
			}
		}
		if err := service.BackfillCovers(ctx); err != nil {
			log.Printf("explo: cover backfill after config save failed: %v", err)
		}
	}()
	writeJSON(w, http.StatusOK, config)
}

// browseExploDirectories lets the admin folder-picker walk the server
// filesystem. It reuses the setup wizard's browseDirectories logic but is
// registered via handleAPI (so requireUser injects the principal into the
// context that requireAdmin reads) - the setup route itself is a raw
// mux.HandleFunc with no auth middleware, so calling it post-setup returns 401
// and the web UI treats that as a dead session and logs the admin out.
func (s *Server) browseExploDirectories(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	entries, err := browseDirectories(requested)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) clearExploConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	service, ok := s.requireExploService(w)
	if !ok {
		return
	}
	config, err := service.ClearConfig(r.Context())
	if err != nil {
		writeExploError(w, err)
		return
	}
	// Disabling clears the effective folder set, so this un-hides every album,
	// empties the ledger, and clears the Explo playlist - a full recovery/undo.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := service.ReconcileRecentlyAdded(ctx); err != nil {
			log.Printf("explo: reconcile after config clear failed: %v", err)
		}
	}()
	writeJSON(w, http.StatusOK, config)
}

// validateExploFolder rejects a chosen folder before it's persisted so the
// admin gets an immediate, specific error rather than a silently-inert config
// (the pipeline would just never find candidates under a bad path).
func validateExploFolder(folder string) error {
	if !filepath.IsAbs(folder) {
		return errors.New("folder must be an absolute path")
	}
	info, err := os.Stat(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("folder does not exist on the server")
		}
		return err
	}
	if !info.IsDir() {
		return errors.New("path is not a directory")
	}
	return nil
}

func writeExploError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, explo.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, explo.ErrInvalidConfig):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
