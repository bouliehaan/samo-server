package api

import (
	"net/http"
	"strings"
)

func (s *Server) listShelfLibraries(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListShelfLibraries(page))
}

func (s *Server) getShelfLibrary(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.ShelfLibrary(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listShelfItems(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListShelfItems(page))
}

func (s *Server) getShelfItem(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.ShelfItem(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listAudiobooks(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListAudiobooks(page))
}

func (s *Server) listShelfAuthors(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListShelfAuthors(page))
}

func (s *Server) getShelfAuthor(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.ShelfAuthor(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listShelfSeries(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListShelfSeries(page))
}

func (s *Server) getShelfSeries(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.ShelfSeries(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listPodcasts(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListPodcasts(page))
}

func (s *Server) listPodcastEpisodes(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListPodcastEpisodes(page))
}

func (s *Server) getPodcastEpisode(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.PodcastEpisode(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) searchShelf(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.SearchShelf(strings.TrimSpace(r.URL.Query().Get("q")), page))
}
