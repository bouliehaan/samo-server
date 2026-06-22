package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
)

type prewarmCountInput struct {
	Count int `json:"count"`
}

// getPodcastPrewarm reports the global default prewarm count (how many newest
// episodes the server keeps warm per show by default).
func (s *Server) getPodcastPrewarm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.podcastCache == nil {
		writeJSON(w, http.StatusOK, map[string]any{"count": 0, "default": 0})
		return
	}
	count, explicit := s.podcastCache.GetPrewarmCount(r.Context(), "")
	if !explicit {
		count = s.podcastCache.DefaultPrewarmCount()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   count,
		"default": s.podcastCache.DefaultPrewarmCount(),
	})
}

// setPodcastPrewarm sets the global default prewarm count.
func (s *Server) setPodcastPrewarm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
		return
	}
	var input prewarmCountInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.podcastCache.SetPrewarmCount(r.Context(), "", input.Count); err != nil {
		writePodcastCacheError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": maxInt(input.Count, 0)})
}

// getPodcastShowPrewarm reports the effective prewarm count for one show plus
// whether it has an explicit override (so the UI can show "using default").
func (s *Server) getPodcastShowPrewarm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	showID := r.PathValue("id")
	if s.podcastCache == nil {
		writeJSON(w, http.StatusOK, map[string]any{"count": 0, "override": false})
		return
	}
	stored, override := s.podcastCache.GetPrewarmCount(r.Context(), showID)
	writeJSON(w, http.StatusOK, map[string]any{
		"count":    s.podcastCache.PrewarmCount(r.Context(), showID),
		"override": override,
		"stored":   stored,
		"default":  s.podcastCache.DefaultPrewarmCount(),
	})
}

// setPodcastShowPrewarm sets a per-show override and immediately warms the
// newest N episodes so the change takes effect without waiting for a feed poll.
func (s *Server) setPodcastShowPrewarm(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
		return
	}
	showID := r.PathValue("id")
	if _, err := s.catalog.Podcast(showID); err != nil {
		writeCatalogError(w, err)
		return
	}
	var input prewarmCountInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.podcastCache.SetPrewarmCount(r.Context(), showID, input.Count); err != nil {
		writePodcastCacheError(w, err)
		return
	}
	if episodes, err := s.catalog.EpisodesForPodcast(showID, catalog.PageRequest{Limit: 500}); err == nil {
		s.podcastCache.PrewarmNewest(showID, episodes.Items, maxInt(input.Count, 0))
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": maxInt(input.Count, 0)})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type cacheLimitInput struct {
	MaxBytes int64 `json:"maxBytes"`
}

// getPodcastCacheLimit reports the effective cache size cap (bytes).
func (s *Server) getPodcastCacheLimit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.podcastCache == nil {
		writeJSON(w, http.StatusOK, map[string]any{"maxBytes": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"maxBytes": s.podcastCache.CacheMaxBytes(r.Context())})
}

// setPodcastCacheLimit sets the cache size cap and prunes to it immediately.
func (s *Server) setPodcastCacheLimit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
		return
	}
	var input cacheLimitInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.podcastCache.SetCacheMaxBytes(r.Context(), input.MaxBytes); err != nil {
		writePodcastCacheError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"maxBytes": s.podcastCache.CacheMaxBytes(r.Context())})
}

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

func (s *Server) getPodcastCacheSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeJSON(w, http.StatusOK, podcastcache.Summary{Enabled: false})
		return
	}
	summary, err := s.podcastCache.Summary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) clearPodcastCache(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.podcastCache == nil || !s.podcastCache.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "podcast cache is not enabled")
		return
	}
	result, err := s.podcastCache.ClearAll(r.Context())
	if err != nil {
		writePodcastCacheError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
