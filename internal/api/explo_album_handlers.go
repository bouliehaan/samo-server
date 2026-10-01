package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/log"
)

// Search for new, for whole albums. Explo finds the album and its track list
// (one edition of it, see canonicalRelease in samo-explo) and downloads it a
// track at a time; samo records each track as its own request, placed on the
// album, so every track is identified, given the album's art and kept into
// the library as that album (explo/album_requests.go).

func (s *Server) searchExploAlbums(w http.ResponseWriter, r *http.Request) {
	remote := s.requireExploDiscovery(w, r)
	if remote == nil {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 || len(query) > 200 {
		writeError(w, 400, "enter between 2 and 200 characters")
		return
	}
	result, err := remote.SearchAlbums(r.Context(), query)
	if err != nil {
		writeExploRemoteError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

// exploAlbumTrackView is a track of an album as the browser sees it: the song,
// and the request for it once there is one.
type exploAlbumTrackView struct {
	explo.Song
	Job *explo.DownloadJob `json:"job,omitempty"`
}

type exploAlbumView struct {
	explo.Album
	Tracks []exploAlbumTrackView `json:"tracks"`
}

// getExploAlbum is an album's track list, for someone deciding whether to
// download it, with what samo already has of it.
func (s *Server) getExploAlbum(w http.ResponseWriter, r *http.Request) {
	remote := s.requireExploDiscovery(w, r)
	if remote == nil {
		return
	}
	id := r.PathValue("id")
	if !explo.ValidCatalogID(id) {
		writeError(w, 400, "invalid album ID")
		return
	}
	album, err := remote.Album(r.Context(), id)
	if err != nil {
		writeExploRemoteError(w, err)
		return
	}
	view := exploAlbumView{Album: album, Tracks: make([]exploAlbumTrackView, 0, len(album.Tracks))}
	for _, track := range album.Tracks {
		item := exploAlbumTrackView{Song: track}
		if request, ok, err := s.explo.Request(r.Context(), track.ID); err == nil && ok {
			job := exploJobFromRequest(track.ID, request)
			item.Job = &job
		}
		view.Tracks = append(view.Tracks, item)
	}
	writeJSON(w, 200, view)
}

// addExploAlbum asks Explo for every track of an album that samo does not
// already have, or is not already fetching. Asking again resumes: tracks in
// the library, staged or still downloading are left as they are, and the
// ones that failed are asked for again. Admin-only, like a song.
func (s *Server) addExploAlbum(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	remote := s.requireExploDiscovery(w, r)
	if remote == nil {
		return
	}
	id := r.PathValue("id")
	if !explo.ValidCatalogID(id) {
		writeError(w, 400, "invalid album ID")
		return
	}
	var input struct {
		Provider string `json:"provider"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid album download request")
		return
	}
	if input.Provider != "" && input.Provider != "auto" && input.Provider != "youtube" && input.Provider != "slskd" {
		writeError(w, 400, "invalid download provider")
		return
	}
	album, err := remote.Album(r.Context(), id)
	if err != nil {
		writeExploRemoteError(w, err)
		return
	}
	queued := 0
	var queueErr error
	for _, track := range album.Tracks {
		if track.AlbumTrack == nil || !strings.EqualFold(track.AlbumTrack.AlbumID, album.ID) || !explo.ValidCatalogID(track.ID) {
			continue
		}
		if request, ok, err := s.explo.Request(r.Context(), track.ID); err == nil && ok && request.State != explo.RequestFailed {
			// Already asked for. A request still under way finishes as this
			// album's track from here on; one already settled stays where
			// it is, a song kept on its own before included.
			if request.AlbumID != album.ID {
				if err := s.explo.AttachToAlbum(r.Context(), track.ID, *track.AlbumTrack); err != nil {
					log.Warnf("explo: attach song request %s to album %s: %v", track.ID, album.ID, err)
				}
			}
			continue
		}
		if libraryID := s.explo.AlbumTrackInLibrary(r.Context(), track, track.AlbumTrack.AlbumTitle); libraryID != "" {
			if err := s.explo.RecordAlbumTrackInLibrary(r.Context(), track, *track.AlbumTrack, libraryID, principal.User.ID); err != nil {
				log.Warnf("explo: record album track %s already in library: %v", track.ID, err)
			}
			continue
		}
		if queueErr != nil {
			continue // Explo refused one; the rest would be refused too
		}
		job, err := remote.AddAlbumTrack(r.Context(), track.ID, input.Provider, album.ID)
		if err != nil {
			queueErr = err
			continue
		}
		// Explo answers a recording it is already fetching with that job,
		// which may have been asked for on its own: it is this album's
		// track all the same.
		job.Song.AlbumTrack = track.AlbumTrack
		if job.Song.Title == "" {
			job.Song = track
		}
		if err := s.explo.RecordRequest(r.Context(), job, principal.User.ID); err != nil {
			log.Warnf("explo: record album track request %s: %v", job.ID, err)
		}
		queued++
	}
	view, err := s.exploAlbumDownload(r, remote, album.ID)
	if err != nil {
		writeError(w, 500, "unable to read the album request")
		return
	}
	if queueErr != nil {
		var remoteErr *explo.RemoteError
		if queued == 0 || !errors.As(queueErr, &remoteErr) {
			writeExploRemoteError(w, queueErr)
			return
		}
		view.Message = "Explo took " + strconv.Itoa(queued) + " track(s) and refused the rest: " + remoteErr.Message + " Add the album again later for the rest."
	}
	writeJSON(w, http.StatusAccepted, view)
}

// exploAlbumDownloadView is a requested album as the browser follows it: each
// track in the same shape as a song's download job.
type exploAlbumDownloadView struct {
	AlbumID string                `json:"albumId"`
	Title   string                `json:"title"`
	Artist  string                `json:"artist"`
	Message string                `json:"message,omitempty"`
	Tracks  []exploAlbumTrackView `json:"tracks"`
}

func (s *Server) getExploAlbumDownload(w http.ResponseWriter, r *http.Request) {
	if !s.exploDiscoveryConfigured(r) {
		writeError(w, 503, "Explo song search is not configured")
		return
	}
	id := r.PathValue("id")
	if !explo.ValidCatalogID(id) {
		writeError(w, 400, "invalid album ID")
		return
	}
	remote, err := s.exploRemoteClient(r)
	if err != nil {
		remote = nil
	}
	view, err := s.exploAlbumDownload(r, remote, id)
	switch {
	case errors.Is(err, errAlbumNotRequested):
		writeError(w, 404, "this album has not been requested")
	case err != nil:
		writeError(w, 500, "unable to read the album request")
	default:
		writeJSON(w, 200, view)
	}
}

var errAlbumNotRequested = errors.New("album not requested")

// exploAlbumDownload reads an album request from samo's record, asking Explo
// only about the tracks it is still downloading.
func (s *Server) exploAlbumDownload(r *http.Request, remote *explo.Remote, albumID string) (exploAlbumDownloadView, error) {
	album, ok, err := s.explo.AlbumRequest(r.Context(), albumID)
	if err != nil {
		return exploAlbumDownloadView{}, err
	}
	if !ok {
		return exploAlbumDownloadView{}, errAlbumNotRequested
	}
	view := exploAlbumDownloadView{AlbumID: album.AlbumID, Title: album.Title, Artist: album.Artist, Tracks: make([]exploAlbumTrackView, 0, len(album.Tracks))}
	for _, track := range album.Tracks {
		song := explo.Song{ID: track.RecordingID, Title: track.Title, Artist: track.Artist, Album: album.Title, DurationMS: track.DurationMS,
			AlbumID: album.AlbumID, AlbumTrack: &explo.AlbumTrack{AlbumID: album.AlbumID, AlbumTitle: album.Title,
				AlbumArtist: album.Artist, Number: track.Number, Disc: track.Disc}}
		job := exploJobFromRequest(track.RecordingID, track.SongRequest)
		if track.State == explo.RequestDownloading && remote != nil {
			if live, err := remote.Job(r.Context(), track.RecordingID); err == nil {
				if err := s.explo.SyncJob(r.Context(), live); err != nil {
					log.Warnf("explo: sync album track %s: %v", track.RecordingID, err)
				}
				job = s.exploJobView(r, live)
			}
		}
		job.Song = song
		view.Tracks = append(view.Tracks, exploAlbumTrackView{Song: song, Job: &job})
	}
	return view, nil
}

// exploJobFromRequest is a download job made from samo's record alone, for a
// track Explo is not asked about: settled, or Explo is out of reach.
func exploJobFromRequest(id string, request explo.SongRequest) explo.DownloadJob {
	if request.State == explo.RequestDownloading {
		return explo.DownloadJob{ID: id, State: "downloading", Message: request.Message}
	}
	library := request
	return explo.DownloadJob{ID: id, State: exploJobState(request.State), Message: request.Message, Library: &library}
}
