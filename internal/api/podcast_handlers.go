package api

import (
	"net/http"

	"github.com/bouliehaan/samo-server/internal/search"
)

func (s *Server) listPodcasts(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListPodcasts(page))
}

func (s *Server) getPodcast(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Podcast(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listPodcastEpisodes(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items := s.catalog.ListPodcastEpisodes(page)
	items.Items = s.enrichEpisodeListCache(r.Context(), items.Items)
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listPodcastShowEpisodes(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.catalog.EpisodesForPodcast(r.PathValue("id"), page)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	items.Items = s.enrichEpisodeListCache(r.Context(), items.Items)
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getPodcastEpisode(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	item, err := s.podcastEpisodeWithUserPlayback(r.Context(), principal.User.ID, r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) searchPodcasts(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	overlay, err := s.loadSearchOverlay(r, principal.User.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	query := search.ParsePodcastQueryFromRequest(r, page)
	writeJSON(w, http.StatusOK, s.searchService().SearchPodcasts(query, overlay))
}
