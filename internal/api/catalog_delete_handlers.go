package api

import (
	"errors"
	"net/http"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
)

type deleteCatalogItemRequest struct {
	DeleteFiles *bool `json:"deleteFiles"`
}

func deleteFilesFromRequest(input deleteCatalogItemRequest) bool {
	if input.DeleteFiles == nil {
		return true
	}
	return *input.DeleteFiles
}

func (s *Server) deleteMusicAlbum(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not configured")
		return
	}
	var input deleteCatalogItemRequest
	_ = decodeJSONOptional(r, &input)
	result, err := catalogstore.DeleteMusicAlbum(r.Context(), s.db, r.PathValue("id"), catalogstore.DeleteOptions{
		DeleteFiles: deleteFilesFromRequest(input),
	})
	if err != nil {
		writeCatalogDeleteError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deleteAudiobook(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not configured")
		return
	}
	var input deleteCatalogItemRequest
	_ = decodeJSONOptional(r, &input)
	result, err := catalogstore.DeleteAudiobook(r.Context(), s.db, r.PathValue("id"), catalogstore.DeleteOptions{
		DeleteFiles: deleteFilesFromRequest(input),
	})
	if err != nil {
		writeCatalogDeleteError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deletePodcastShow(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not configured")
		return
	}
	var input deleteCatalogItemRequest
	_ = decodeJSONOptional(r, &input)
	result, err := catalogstore.DeletePodcastShow(r.Context(), s.db, r.PathValue("id"), catalogstore.DeleteOptions{
		DeleteFiles: deleteFilesFromRequest(input),
	})
	if err != nil {
		writeCatalogDeleteError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeCatalogDeleteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, catalogstore.ErrRemoteItem):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
