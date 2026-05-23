package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/bouliehaan/samo-server/internal/sources"
)

type internetRadioStationResponse struct {
	sources.InternetRadioStation
	PublicStreamURL string `json:"publicStreamUrl"`
	PlaylistURL     string `json:"playlistUrl"`
}

func (s *Server) listPodcastFeeds(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feeds, err := s.sourcesService().ListPodcastFeeds(r.Context(), page)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) createPodcastFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input sources.AddPodcastFeedInput
	if !readJSONBody(w, r, &input) {
		return
	}
	feed, err := s.sourcesService().AddPodcastFeed(r.Context(), input)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

func (s *Server) getPodcastFeed(w http.ResponseWriter, r *http.Request) {
	feed, err := s.sourcesService().GetPodcastFeed(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *Server) updatePodcastFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input sources.UpdatePodcastFeedInput
	if !readJSONBody(w, r, &input) {
		return
	}
	feed, err := s.sourcesService().UpdatePodcastFeed(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *Server) runPodcastPollCycle(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	result, err := s.sourcesService().RunPodcastPollCycle(r.Context(), time.Now().UTC())
	if err != nil {
		writeSourceError(w, err)
		return
	}
	if result.Updated > 0 {
		if err := s.reloadCatalogProjection(r); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) refreshPodcastFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	feed, err := s.sourcesService().RefreshPodcastFeed(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *Server) deletePodcastFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.sourcesService().DeletePodcastFeed(r.Context(), r.PathValue("id")); err != nil {
		writeSourceError(w, err)
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInternetRadioStations(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stations, err := s.sourcesService().ListInternetRadioStations(r.Context(), page)
	if err != nil {
		writeSourceError(w, err)
		return
	}

	response := make([]internetRadioStationResponse, 0, len(stations.Items))
	for _, station := range stations.Items {
		response = append(response, s.internetRadioResponse(r, station))
	}
	writeJSON(w, http.StatusOK, struct {
		Items  []internetRadioStationResponse `json:"items"`
		Total  int                            `json:"total"`
		Limit  int                            `json:"limit"`
		Offset int                            `json:"offset"`
	}{
		Items:  response,
		Total:  stations.Total,
		Limit:  stations.Limit,
		Offset: stations.Offset,
	})
}

func (s *Server) createInternetRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input sources.AddInternetRadioStationInput
	if !readJSONBody(w, r, &input) {
		return
	}
	station, err := s.sourcesService().AddInternetRadioStation(r.Context(), input)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.internetRadioResponse(r, station))
}

func (s *Server) updateInternetRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var input sources.UpdateInternetRadioStationInput
	if !readJSONBody(w, r, &input) {
		return
	}
	station, err := s.sourcesService().UpdateInternetRadioStation(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.internetRadioResponse(r, station))
}

func (s *Server) probeInternetRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	station, err := s.sourcesService().ProbeInternetRadioStation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.internetRadioResponse(r, station))
}

func (s *Server) runInternetRadioProbeCycle(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	result, err := s.sourcesService().RunInternetRadioProbeCycle(r.Context(), time.Now().UTC())
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getInternetRadioStation(w http.ResponseWriter, r *http.Request) {
	station, err := s.sourcesService().GetInternetRadioStation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.internetRadioResponse(r, station))
}

func (s *Server) deleteInternetRadioStation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.sourcesService().DeleteInternetRadioStation(r.Context(), r.PathValue("id")); err != nil {
		writeSourceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) internetRadioPlaylist(w http.ResponseWriter, r *http.Request) {
	station, err := s.sourcesService().GetInternetRadioStation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s\n", station.Name, station.StreamURL)
}

func (s *Server) internetRadioStream(w http.ResponseWriter, r *http.Request) {
	station, err := s.sourcesService().GetInternetRadioStation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSourceError(w, err)
		return
	}
	http.Redirect(w, r, station.StreamURL, http.StatusTemporaryRedirect)
}

func (s *Server) internetRadioResponse(r *http.Request, station sources.InternetRadioStation) internetRadioStationResponse {
	id := url.PathEscape(station.ID)
	return internetRadioStationResponse{
		InternetRadioStation: station,
		PublicStreamURL:      publicURL(r, "/internet-radio/"+id+"/stream"),
		PlaylistURL:          publicURL(r, "/internet-radio/"+id+"/playlist.m3u"),
	}
}

func (s *Server) sourcesService() *sources.Service {
	if s.sources == nil {
		return nil
	}
	return s.sources
}

func (s *Server) reloadCatalogProjection(r *http.Request) error {
	if s.reloadCatalog == nil {
		return nil
	}
	return s.reloadCatalog(r.Context())
}

func readJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return false
	}
	return true
}

func writeSourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sources.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "source service is not configured")
	case errors.Is(err, sources.ErrNotFound):
		writeError(w, http.StatusNotFound, "source not found")
	case errors.Is(err, sources.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, "url must be absolute http or https")
	case errors.Is(err, sources.ErrInvalidPollInterval):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, sources.ErrInvalidProbeInterval):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
