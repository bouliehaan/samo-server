package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
)

func (s *Server) getMediaFile(w http.ResponseWriter, r *http.Request) {
	item, err := s.filesService().GetMediaFile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFilesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) streamMediaFile(w http.ResponseWriter, r *http.Request) {
	if err := s.filesService().ServeMediaFile(r.Context(), r.PathValue("id"), w, r); err != nil {
		writeFilesError(w, err)
	}
}

func (s *Server) streamMusicTrack(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	track, err := s.musicTrackWithUserPlayback(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	resume := streamResumeSeconds(r, track.Playback.ProgressSeconds)
	s.notifyMusicTrackLastFM(r.Context(), principal.User.ID, track.ID, catalog.PlaybackState{}, catalog.PlaybackState{ProgressSeconds: resume}, nil, "stream", resume)
	s.streamCatalogAudioFiles(w, r, track.AudioFiles, track.Playback, track.DiscNumber)
}

func (s *Server) streamPodcastEpisode(w http.ResponseWriter, r *http.Request) {
	episode, err := s.catalog.PodcastEpisode(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if len(episode.AudioFiles) == 0 {
		s.streamPodcastEnclosure(w, r, episode)
		return
	}
	s.streamCatalogAudioFiles(w, r, episode.AudioFiles, episode.Progress, 0)
}

func (s *Server) streamAudiobook(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Audiobook(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.streamCatalogAudioFiles(w, r, item.AudioFiles, item.Progress, 0)
}

func (s *Server) streamCatalogAudioFiles(w http.ResponseWriter, r *http.Request, audioFiles []catalog.AudioFile, playback catalog.PlaybackState, defaultDisc int) {
	target, err := catalog.SelectStreamTarget(audioFiles, playback, catalog.StreamSelectQueryFromRequest(r), defaultDisc)
	if err != nil {
		writeStreamSelectError(w, err)
		return
	}
	w.Header().Set("X-Samo-Media-File-Id", target.FileID)
	if target.GlobalSeconds > 0 {
		w.Header().Set("X-Samo-Stream-Global-Seconds", strconv.Itoa(target.GlobalSeconds))
	}
	if err := s.filesService().ServeMediaFileAt(r.Context(), target.FileID, target.OffsetSeconds, w, r); err != nil {
		writeFilesError(w, err)
	}
}

func (s *Server) streamPodcastEnclosure(w http.ResponseWriter, r *http.Request, episode catalog.PodcastEpisode) {
	if strings.TrimSpace(episode.EnclosureURL) == "" {
		writeError(w, http.StatusNotFound, "no audio files available")
		return
	}
	resume := streamResumeSeconds(r, episode.Progress.ProgressSeconds)
	if s.podcastCache != nil && s.podcastCache.Enabled() {
		if cached, ok, err := s.podcastCache.Lookup(r.Context(), episode.ID, episode.EnclosureURL); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		} else if ok {
			w.Header().Set("X-Samo-Stream-Source", "cache")
			if resume > 0 {
				w.Header().Set("X-Samo-Stream-Offset-Seconds", strconv.Itoa(resume))
			}
			if err := s.filesService().ServeReadablePathAt(r.Context(), cached.Path, cached.ContentType, episode.DurationSeconds, resume, w, r); err != nil {
				writeFilesError(w, err)
			}
			return
		}
	}
	if err := s.podcastStreamService().ServeEnclosure(r.Context(), podcaststream.Enclosure{
		URL:             episode.EnclosureURL,
		ContentType:     episode.EnclosureType,
		SizeBytes:       episode.EnclosureBytes,
		DurationSeconds: episode.DurationSeconds,
		OffsetSeconds:   resume,
	}, w, r); err != nil {
		writePodcastStreamError(w, err)
		return
	}
	if s.podcastCache != nil && s.podcastCache.Enabled() {
		episodeCopy := episode
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_ = s.podcastCache.EnsureCached(ctx, episodeCopy)
		}()
	}
}

func (s *Server) podcastStreamService() *podcaststream.Service {
	if s.podcastStream == nil {
		panic("podcast stream service is not configured")
	}
	return s.podcastStream
}

func writePodcastStreamError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, podcaststream.ErrInvalidEnclosure), errors.Is(err, podcaststream.ErrForbiddenEnclosure):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, podcaststream.ErrUpstream):
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, podcaststream.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeStreamSelectError(w http.ResponseWriter, err error) {
	message := strings.TrimSpace(err.Error())
	switch message {
	case "no audio files available":
		writeError(w, http.StatusNotFound, message)
	case "mediaFileId does not belong to this item":
		writeError(w, http.StatusBadRequest, message)
	default:
		writeError(w, http.StatusBadRequest, message)
	}
}

func (s *Server) serveMusicAlbumCover(w http.ResponseWriter, r *http.Request) {
	album, err := s.catalog.MusicAlbum(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.serveCatalogImage(w, r, album.Images)
}

func (s *Server) serveAudiobookCover(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Audiobook(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	var images []catalog.Image
	if item.Cover != nil {
		images = []catalog.Image{*item.Cover}
	}
	s.serveCatalogImage(w, r, images)
}

func (s *Server) servePodcastCover(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Podcast(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	var images []catalog.Image
	if item.Cover != nil {
		images = []catalog.Image{*item.Cover}
	}
	s.serveCatalogImage(w, r, images)
}

func (s *Server) serveCatalogImage(w http.ResponseWriter, r *http.Request, images []catalog.Image) {
	if path := firstImagePath(images); path != "" {
		if err := s.filesService().ServeLocalPath(r.Context(), path, w, r); err != nil {
			writeFilesError(w, err)
		}
		return
	}
	// No local file — fall back to a remote URL if the metadata layer
	// supplied one (e.g. cover from a metadata-apply that hasn't been
	// downloaded yet, or an RSS feed image). Redirect rather than proxy
	// so the bytes don't round-trip through Samo.
	if url := firstImageURL(images); url != "" {
		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
		return
	}
	writeError(w, http.StatusNotFound, "cover not found")
}

func firstImagePath(images []catalog.Image) string {
	for _, image := range images {
		if path := strings.TrimSpace(image.Path); path != "" {
			return path
		}
	}
	return ""
}

func firstImageURL(images []catalog.Image) string {
	for _, image := range images {
		if url := strings.TrimSpace(image.URL); url != "" {
			return url
		}
	}
	return ""
}

func (s *Server) filesService() *files.Service {
	if s.files == nil {
		panic("files service is not configured")
	}
	return s.files
}

func writeFilesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrNotFound):
		writeError(w, http.StatusNotFound, "media file not found")
	case errors.Is(err, files.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, files.ErrMissing):
		writeError(w, http.StatusNotFound, "media file is missing on disk")
	case errors.Is(err, files.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func streamResumeSeconds(r *http.Request, savedProgress int) int {
	query := catalog.StreamSelectQueryFromRequest(r)
	if query.ProgressSeconds > 0 {
		return query.ProgressSeconds
	}
	if savedProgress > 0 {
		return savedProgress
	}
	return 0
}
