package api

import (
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/search"
)

func (s *Server) listAudiobooks(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListAudiobooks(page))
}

func (s *Server) getAudiobook(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.Audiobook(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listContributors(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListContributors(page))
}

func (s *Server) getContributor(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("include")) == "audiobooks" {
		page, err := readPage(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		detail, err := s.catalog.ContributorDetail(r.PathValue("id"), page)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
	item, err := s.catalog.Contributor(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listContributorAudiobooks(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.catalog.AudiobooksForContributor(r.PathValue("id"), page)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListSeries(page))
}

func (s *Server) getSeries(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("include")) == "audiobooks" {
		page, err := readPage(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		detail, err := s.catalog.SeriesDetail(r.PathValue("id"), page)
		if err != nil {
			writeCatalogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
	item, err := s.catalog.Series(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listSeriesAudiobooks(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.catalog.AudiobooksForSeries(r.PathValue("id"), page)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) searchAudiobooks(w http.ResponseWriter, r *http.Request) {
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
	query := search.ParseAudiobookQueryFromRequest(r, page)
	writeJSON(w, http.StatusOK, s.searchService().SearchAudiobooks(query, overlay))
}
