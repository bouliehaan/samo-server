package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
)

func (s *Server) listMetadataProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metadata.Providers())
}

func (s *Server) previewMetadataApply(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var request metadata.MetadataApplyRequest
	if !readJSONBody(w, r, &request) {
		return
	}
	preview, err := s.metadataApplyService().Preview(r.Context(), request)
	if err != nil {
		writeMetadataApplyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) applyMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var request metadata.MetadataApplyRequest
	if !readJSONBody(w, r, &request) {
		return
	}
	result, err := s.metadataApplyService().Apply(r.Context(), request)
	if err != nil {
		writeMetadataApplyError(w, err)
		return
	}
	if !request.DeferCatalogReload {
		if err := s.reloadCatalogProjection(r); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getMetadataOverride(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	view, err := s.metadataApplyService().GetOverride(r.Context(), r.PathValue("targetKind"), r.PathValue("targetId"))
	if err != nil {
		writeMetadataApplyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) deleteMetadataOverride(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.metadataApplyService().DeleteOverride(r.Context(), r.PathValue("targetKind"), r.PathValue("targetId")); err != nil {
		writeMetadataApplyError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearMetadataOverrideFields(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var request metadata.MetadataOverrideClearRequest
	if !readJSONBody(w, r, &request) {
		return
	}
	if err := s.metadataApplyService().ClearOverrideFields(r.Context(), r.PathValue("targetKind"), r.PathValue("targetId"), request.Fields); err != nil {
		writeMetadataApplyError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) searchMetadata(w http.ResponseWriter, r *http.Request) {
	request, err := readMetadataSearchRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	response, err := s.metadata.Search(r.Context(), request)
	if err != nil {
		writeMetadataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func readMetadataSearchRequest(r *http.Request) (metadata.SearchRequest, error) {
	query := r.URL.Query()
	request := metadata.SearchRequest{
		Kind:        metadata.Kind(query.Get("kind")),
		Query:       strings.TrimSpace(query.Get("q")),
		Provider:    strings.TrimSpace(query.Get("provider")),
		Title:       strings.TrimSpace(query.Get("title")),
		Author:      strings.TrimSpace(query.Get("author")),
		ISBN:        strings.TrimSpace(query.Get("isbn")),
		ASIN:        strings.TrimSpace(query.Get("asin")),
		AudibleASIN: strings.TrimSpace(query.Get("audibleAsin")),
		Artist:      strings.TrimSpace(query.Get("artist")),
		Album:       strings.TrimSpace(query.Get("album")),
		Track:       strings.TrimSpace(query.Get("track")),
		MusicType:   metadata.MusicSearchType(query.Get("musicType")),
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return metadata.SearchRequest{}, errors.New("limit must be a number")
		}
		request.Limit = limit
	}
	return request, nil
}

func (s *Server) metadataApplyService() *metadata.MetadataApplyService {
	if s.metadataApply == nil {
		panic("metadata apply service is not configured")
	}
	return s.metadataApply
}

func writeMetadataApplyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrMetadataApplyDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, metadata.ErrApplyNotFound):
		writeError(w, http.StatusNotFound, "metadata apply target not found")
	case errors.Is(err, catalog.ErrMetadataOverrideNotFound):
		writeError(w, http.StatusNotFound, "metadata override not found")
	case errors.Is(err, metadata.ErrInvalidApplyTarget),
		errors.Is(err, metadata.ErrInvalidApplyField),
		errors.Is(err, metadata.ErrEmptyApplyFields),
		errors.Is(err, metadata.ErrApplyCandidateKind),
		errors.Is(err, metadata.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeMetadataError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, "invalid metadata search request")
	case errors.Is(err, metadata.ErrProviderNotFound):
		writeError(w, http.StatusNotFound, "metadata provider not found")
	case errors.Is(err, metadata.ErrUnsupportedKind):
		writeError(w, http.StatusBadRequest, "metadata provider does not support requested kind")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
