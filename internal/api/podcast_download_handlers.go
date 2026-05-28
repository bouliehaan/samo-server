package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
)

func (s *Server) cachePodcastEpisode(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
		return
	}

	episode, err := s.catalog.PodcastEpisode(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	if len(episode.AudioFiles) > 0 {
		writeError(w, http.StatusConflict, "episode is already stored locally")
		return
	}
	if episode.EnclosureURL == "" {
		writeError(w, http.StatusBadRequest, "episode has no enclosure url")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	if err := s.podcastCache.EnsureCached(ctx, episode); err != nil {
		writePodcastCacheError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.podcastCache.EpisodeCacheStatus(ctx, episode))
}

func (s *Server) getPodcastEpisodeCache(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeJSON(w, http.StatusOK, catalog.EpisodeCache{})
		return
	}

	episode, err := s.podcastEpisodeWithUserPlayback(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.podcastCache.EpisodeCacheStatus(r.Context(), episode))
}

func (s *Server) deletePodcastEpisodeCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
		return
	}
	if err := s.podcastCache.Evict(r.Context(), r.PathValue("id")); err != nil {
		writePodcastCacheError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enrichEpisodeCache(ctx context.Context, episode *catalog.PodcastEpisode) {
	if episode == nil || s.podcastCache == nil || !s.podcastCache.Enabled() {
		return
	}
	status := s.podcastCache.EpisodeCacheStatus(ctx, *episode)
	if status.Cached || status.Local {
		episode.Cache = &status
	}
}

func (s *Server) enrichEpisodeListCache(ctx context.Context, episodes []catalog.PodcastEpisode) []catalog.PodcastEpisode {
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		return episodes
	}
	for i := range episodes {
		status := s.podcastCache.EpisodeCacheStatus(ctx, episodes[i])
		if status.Cached || status.Local {
			episodes[i].Cache = &status
		}
	}
	return episodes
}

func writePodcastCacheError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, podcastcache.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
	case errors.Is(err, podcastcache.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid podcast cache request")
	case errors.Is(err, podcastcache.ErrNotCached):
		writeError(w, http.StatusNotFound, "episode is not cached")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
