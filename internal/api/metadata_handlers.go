package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/metadata"
)

func (s *Server) listMetadataProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metadata.Providers())
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
		Kind:      metadata.Kind(query.Get("kind")),
		Query:     strings.TrimSpace(query.Get("q")),
		Provider:  strings.TrimSpace(query.Get("provider")),
		Title:     strings.TrimSpace(query.Get("title")),
		Author:    strings.TrimSpace(query.Get("author")),
		ISBN:      strings.TrimSpace(query.Get("isbn")),
		Artist:    strings.TrimSpace(query.Get("artist")),
		Album:     strings.TrimSpace(query.Get("album")),
		Track:     strings.TrimSpace(query.Get("track")),
		MusicType: metadata.MusicSearchType(query.Get("musicType")),
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
