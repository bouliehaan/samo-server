package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/libraries"
)

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.librariesService().List(r.Context(), page.Limit, page.Offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getLibrary(w http.ResponseWriter, r *http.Request) {
	item, err := s.librariesService().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var input libraries.CreateLibraryInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.librariesService().Create(r.Context(), input)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateLibrary(w http.ResponseWriter, r *http.Request) {
	var input libraries.UpdateLibraryInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.librariesService().Update(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	if err := s.librariesService().Delete(r.Context(), r.PathValue("id")); err != nil {
		writeLibraryError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) scanAllLibraries(w http.ResponseWriter, r *http.Request) {
	result, err := s.librariesService().ScanAll(r.Context(), libraries.TriggerAPI)
	if err != nil {
		writeLibraryScanError(w, result, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) scanLibrary(w http.ResponseWriter, r *http.Request) {
	result, err := s.librariesService().ScanLibrary(r.Context(), r.PathValue("id"), libraries.TriggerAPI)
	if err != nil {
		writeLibraryScanError(w, result, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listScanJobs(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.librariesService().ListScanJobs(r.Context(), page.Limit, page.Offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getScanJob(w http.ResponseWriter, r *http.Request) {
	item, err := s.librariesService().GetScanJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) librariesService() *libraries.Service {
	if s.libraries == nil {
		panic("libraries service is not configured")
	}
	return s.libraries
}

func writeLibraryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, libraries.ErrNotFound), errors.Is(err, libraries.ErrScanJobNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, libraries.ErrProtectedLibrary):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, libraries.ErrInvalidLibrary), errors.Is(err, libraries.ErrPathNotDirectory), errors.Is(err, libraries.ErrDuplicatePath):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeLibraryScanError(w http.ResponseWriter, result libraries.ScanResult, err error) {
	if result.Job.ID != "" {
		writeJSON(w, http.StatusConflict, result)
		return
	}
	writeLibraryError(w, err)
}
