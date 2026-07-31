package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/log"
	"github.com/bouliehaan/samo-server/internal/safego"
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
	safego.Go("explo pass after config change", func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Minute)
		defer cancel()
		// First re-sync hidden flags / ledger / playlist to the new folder
		// (fast, path-based) so a narrowed folder recovers Recently Added
		// immediately. Then run the slow, rate-limited identification pass.
		if err := service.ReconcileRecentlyAdded(ctx); err != nil {
			log.Warnf("explo: reconcile after config save failed: %v", err)
		}
		if service.Enabled() {
			if _, err := service.ProcessNewTracks(ctx); err != nil {
				log.Warnf("explo: process after config save failed: %v", err)
			}
		}
		if err := service.BackfillCovers(ctx); err != nil {
			log.Warnf("explo: cover backfill after config save failed: %v", err)
		}
	})
	writeJSON(w, http.StatusOK, config)
}

// postExploReprocess is the admin "re-scan / retry" action: it resets stranded
// identifications (so tracks retired by the attempt ceiling — e.g. during the
// AcoustID outage — re-run) and every track's cover state (so the per-track
// cover engine re-resolves art), then kicks the slow identify + cover passes in
// the background. The synchronous reset counts are returned immediately.
func (s *Server) postExploReprocess(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	service, ok := s.requireExploService(w)
	if !ok {
		return
	}
	result, err := service.Reprocess(r.Context())
	if err != nil {
		writeExploError(w, err)
		return
	}
	safego.Go("explo pass after reprocess", func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Minute)
		defer cancel()
		if service.Enabled() {
			if _, err := service.ProcessNewTracks(ctx); err != nil {
				log.Warnf("explo: process after reprocess failed: %v", err)
			}
		}
		if err := service.BackfillCovers(ctx); err != nil {
			log.Warnf("explo: cover backfill after reprocess failed: %v", err)
		}
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"identificationReset": result.IdentificationReset,
		"coversReset":         result.CoversReset,
	})
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
	safego.Go("explo reconcile after config clear", func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 5*time.Minute)
		defer cancel()
		if err := service.ReconcileRecentlyAdded(ctx); err != nil {
			log.Warnf("explo: reconcile after config clear failed: %v", err)
		}
	})
	writeJSON(w, http.StatusOK, config)
}

// getExploStatus is the user-level gate for the web UI's Explo tab: unlike
// every other explo endpoint it is NOT admin-only (any signed-in user may see
// their explo queue), and unlike GET /explo/config it exposes only the
// feature-visibility facts — no folder path, no key presence.
func (s *Server) getExploStatus(w http.ResponseWriter, r *http.Request) {
	service := s.exploService()
	if service == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "enabled": false})
		return
	}
	config, err := service.Config(r.Context())
	if err != nil {
		writeExploError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":     config.Configured,
		"enabled":        config.Enabled,
		"disabledReason": config.DisabledReason,
	})
}

// exploLedgerTrack is a ledger row decorated with current display fields from
// the catalog projection (override-aware, so matched titles show as applied).
type exploLedgerTrack struct {
	explo.LedgerRow
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	AlbumTitle string `json:"albumTitle,omitempty"`
}

// getExploTracks lists the explo pipeline ledger for the Explo tab. User-
// level on purpose: the Explore playlist already exposes this membership to
// every user; this adds the pipeline status columns.
func (s *Server) getExploTracks(w http.ResponseWriter, r *http.Request) {
	service, ok := s.requireExploService(w)
	if !ok {
		return
	}
	config, err := service.Config(r.Context())
	if err != nil {
		writeExploError(w, err)
		return
	}
	snapshot, err := service.Ledger(r.Context(), 1000)
	if err != nil {
		writeExploError(w, err)
		return
	}
	tracks := make([]exploLedgerTrack, 0, len(snapshot.Tracks))
	for _, row := range snapshot.Tracks {
		decorated := exploLedgerTrack{LedgerRow: row, Title: row.MatchedTitle, Artist: row.MatchedArtist}
		if item, err := s.catalog.MusicTrack(row.TrackID); err == nil {
			decorated.Title = item.Title
			decorated.Artist = item.DisplayArtist
			decorated.AlbumTitle = item.AlbumTitle
			if item.AlbumID != "" {
				decorated.AlbumID = item.AlbumID
			}
		}
		if decorated.AlbumTitle == "" && decorated.AlbumID != "" {
			if album, err := s.catalog.MusicAlbum(decorated.AlbumID); err == nil {
				decorated.AlbumTitle = album.Title
			}
		}
		tracks = append(tracks, decorated)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": config.Configured,
		"enabled":    config.Enabled,
		"summary":    snapshot.Summary,
		"tracks":     tracks,
	})
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
