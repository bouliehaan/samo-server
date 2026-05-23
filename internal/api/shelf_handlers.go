package api

import (
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/search"
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
	if strings.TrimSpace(r.URL.Query().Get("include")) == "items" {
		page, err := readPage(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		detail, err := s.catalog.ShelfAuthorDetail(r.PathValue("id"), page)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
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
	if strings.TrimSpace(r.URL.Query().Get("include")) == "items" {
		page, err := readPage(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		detail, err := s.catalog.ShelfSeriesDetail(r.PathValue("id"), page)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
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
	query := search.ParseShelfQueryFromRequest(r, page)
	writeJSON(w, http.StatusOK, s.searchService().SearchShelf(query, overlay))
}
