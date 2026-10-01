package api

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/log"
)

// A YouTube Music playlist imported with Explo: samo creates the playlist
// under its YouTube Music name with the songs the library has, and asks Explo
// for the rest, each a Search for new request that joins the playlist once it
// is in the library (explo/playlist_imports.go). The downloads end up in the
// library, so importing this way is admin-only, like adding a song.

var youtubePlaylistIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,64}$`)

// youtubeMusicPlaylistID is the playlist a YouTube or YouTube Music link
// names: its list= parameter (…/playlist?list=…, …/watch?v=…&list=…), or a
// …/browse/VL… page.
func youtubeMusicPlaylistID(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	switch host {
	case "music.youtube.com", "youtube.com", "m.youtube.com", "youtu.be":
	default:
		return ""
	}
	id := parsed.Query().Get("list")
	if id == "" {
		if browse, ok := strings.CutPrefix(parsed.Path, "/browse/VL"); ok {
			id = browse
		}
	}
	if !youtubePlaylistIDPattern.MatchString(id) {
		return ""
	}
	return id
}

type exploPlaylistImportResponse struct {
	Playlist catalog.MusicPlaylist `json:"playlist"`
	Import   explo.PlaylistImport  `json:"import"`
}

func (s *Server) importExploPlaylist(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	state, remote := s.exploDiscoveryState(r)
	if !state.Available {
		message := state.Reason
		if message == "" {
			message = "Explo song search is not configured"
		}
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	if !state.Playlists {
		writeError(w, http.StatusServiceUnavailable, "The connected Explo cannot read YouTube Music playlists yet. Update samo-explo.")
		return
	}
	var input struct {
		URL    string `json:"url"`
		Name   string `json:"name"`
		Public bool   `json:"public"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !readJSONBody(w, r, &input) {
		return
	}
	id := youtubeMusicPlaylistID(input.URL)
	if id == "" {
		writeError(w, http.StatusBadRequest, "paste a YouTube Music playlist link (music.youtube.com/playlist?list=…)")
		return
	}
	if len(strings.TrimSpace(input.Name)) > 200 {
		writeError(w, http.StatusBadRequest, "playlist name is too long")
		return
	}
	listing, err := remote.Playlist(r.Context(), id)
	var remoteErr *explo.RemoteError
	switch {
	case errors.As(err, &remoteErr) && remoteErr.Status == http.StatusNotFound:
		writeError(w, http.StatusNotFound, "YouTube Music could not open this playlist. It has to be public or unlisted.")
		return
	case err != nil:
		writeExploRemoteError(w, err)
		return
	case len(listing.Tracks) == 0:
		writeError(w, http.StatusBadRequest, "This playlist has no tracks YouTube Music can play.")
		return
	}
	playlist, err := s.explo.ImportPlaylist(r.Context(), principal.User.ID, input.Name, input.URL, input.Public, listing)
	if err != nil {
		writePlaylistError(w, err)
		return
	}
	if !s.commitPlaylist(w, r, playlist) {
		return
	}
	// Hand the missing songs to Explo now rather than on the next pass; the
	// page follows them through the import view.
	go s.explo.AdvancePlaylistImports(s.baseCtx, remote)
	view, _, err := s.explo.PlaylistImport(r.Context(), playlist.ID)
	if err != nil {
		log.Warnf("explo: read playlist import %s: %v", playlist.ID, err)
	}
	writeJSON(w, http.StatusAccepted, exploPlaylistImportResponse{Playlist: playlist, Import: view})
}

func (s *Server) getExploPlaylistImport(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	if _, err := s.catalog.MusicPlaylistForUser(principal.User.ID, id); err != nil {
		writeCatalogError(w, err)
		return
	}
	view, ok, err := s.explo.PlaylistImport(r.Context(), id)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, "unable to read the playlist import")
		return
	case !ok:
		writeError(w, http.StatusNotFound, "this playlist was not imported with Explo")
		return
	}
	s.followImportDownloads(r, &view)
	writeJSON(w, http.StatusOK, view)
}

// followImportDownloads asks Explo how the playlist's downloads are going,
// as the album view does: one waiting for a slot says so, and one Explo has
// staged moves on to identification without waiting for samo's next pass.
func (s *Server) followImportDownloads(r *http.Request, view *explo.PlaylistImport) {
	remote, err := s.exploRemoteClient(r)
	if err != nil || remote == nil {
		return
	}
	asked := 0
	for i := range view.Tracks {
		track := &view.Tracks[i]
		if track.State != explo.RequestDownloading || asked >= 100 {
			continue
		}
		asked++
		job, err := remote.Job(r.Context(), track.RecordingID)
		if err != nil {
			continue
		}
		if err := s.explo.SyncJob(r.Context(), job); err != nil {
			log.Warnf("explo: sync playlist track %s: %v", track.RecordingID, err)
		}
		switch job.State {
		case "failed":
			track.State, track.Message = explo.RequestFailed, job.Message
		case "staged":
			track.State, track.Message = explo.RequestIdentifying, "Downloaded. Waiting for samo to identify it."
		default:
			if job.Message != "" {
				track.Message = job.Message
			}
		}
	}
}

func (s *Server) retryExploPlaylistImport(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	remote := s.requireExploDiscovery(w, r)
	if remote == nil {
		return
	}
	id := r.PathValue("id")
	if _, ok, err := s.explo.PlaylistImport(r.Context(), id); err != nil || !ok {
		writeError(w, http.StatusNotFound, "this playlist was not imported with Explo")
		return
	}
	if _, err := s.explo.RetryPlaylistImport(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "unable to retry the playlist import")
		return
	}
	// Tracks already in the library by now join the playlist here.
	s.explo.AdvancePlaylistImports(r.Context(), remote)
	playlist, err := s.playlistsService().Get(r.Context(), id)
	if err != nil {
		writePlaylistError(w, err)
		return
	}
	if !s.commitPlaylist(w, r, playlist) {
		return
	}
	view, _, err := s.explo.PlaylistImport(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to read the playlist import")
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}
