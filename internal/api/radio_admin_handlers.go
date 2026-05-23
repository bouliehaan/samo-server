package api

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/bouliehaan/samo-server/internal/radio"
)

type radioStationResponse struct {
	radio.StationRecord
	StreamURL   string `json:"streamUrl"`
	PlaylistURL string `json:"playlistUrl"`
}

func (s *Server) listRadioStationRecords(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	records, err := s.radio.ListStationRecords(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := make([]radioStationResponse, 0, len(records))
	for _, record := range records {
		response = append(response, s.radioRecordResponse(r, record))
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getRadioStationRecord(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	record, err := s.radio.GetStationRecord(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.radioRecordResponse(r, record))
}

func (s *Server) createRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input radio.CreateStationInput
	if !readJSONBody(w, r, &input) {
		return
	}
	record, err := s.radio.CreateStation(r.Context(), input)
	if err != nil {
		writeRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.radioRecordResponse(r, record))
}

func (s *Server) updateRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input radio.UpdateStationInput
	if !readJSONBody(w, r, &input) {
		return
	}
	record, err := s.radio.UpdateStation(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.radioRecordResponse(r, record))
}

func (s *Server) deleteRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.radio.DeleteStation(r.Context(), r.PathValue("id")); err != nil {
		writeRadioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addRadioStationItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input radio.CreateStationItemInput
	if !readJSONBody(w, r, &input) {
		return
	}
	item, err := s.radio.AddStationItem(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeRadioError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) deleteRadioStationItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.radio.RemoveStationItem(r.Context(), r.PathValue("itemId")); err != nil {
		writeRadioError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) radioRecordResponse(r *http.Request, record radio.StationRecord) radioStationResponse {
	return radioStationResponse{
		StationRecord: record,
		StreamURL:     publicURL(r, "/radio/"+url.PathEscape(record.ID)+"/stream"),
		PlaylistURL:   publicURL(r, "/radio/"+url.PathEscape(record.ID)+"/playlist.m3u"),
	}
}

func writeRadioError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, radio.ErrStationNotFound):
		writeError(w, http.StatusNotFound, "station not found")
	case errors.Is(err, radio.ErrItemNotFound):
		writeError(w, http.StatusNotFound, "station item not found")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
