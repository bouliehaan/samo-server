package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/sources"
)

type internetRadioStationResponse struct {
	sources.InternetRadioStation
	PublicStreamURL string `json:"publicStreamUrl"`
	PlaylistURL     string `json:"playlistUrl"`
	CoverURL        string `json:"coverUrl,omitempty"`
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

func (s *Server) attachPodcastShowFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	podcastID := strings.TrimSpace(r.PathValue("id"))
	if podcastID == "" {
		writeError(w, http.StatusBadRequest, "podcast id is required")
		return
	}
	var input sources.AddPodcastFeedInput
	if !readJSONBody(w, r, &input) {
		return
	}
	input.PodcastID = podcastID
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
	if !s.disableInitialInternetRadioProbe {
		// Fire a probe in the background so the first metadata (icy-name, codec,
		// bitrate, current track) lands without the user clicking PROBE. We
		// detach from the request context so the probe survives the response
		// returning. The probe records its own success/failure into probe
		// scheduling columns, so silent failure is acceptable here.
		go func(id string) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := s.sourcesService().ProbeInternetRadioStation(ctx, id); err != nil {
				log.Printf("initial probe for internet radio %s failed: %v", id, err)
			}
		}(station.ID)
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

func (s *Server) uploadInternetRadioCover(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "station id is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("cover")
	if err != nil {
		writeError(w, http.StatusBadRequest, "cover file is required")
		return
	}
	defer file.Close()

	contentType := ""
	if header != nil {
		contentType = header.Header.Get("Content-Type")
	}
	image, err := s.coversService().StoreFromUpload(r.Context(), "internet-radio:"+id, contentType, file)
	if err != nil {
		writeCoverUploadError(w, err)
		return
	}
	station, err := s.sourcesService().SetInternetRadioCover(r.Context(), id, image.ID)
	if err != nil {
		writeSourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.internetRadioResponse(r, station))
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
	streamURL := station.StreamURL
	if resolved, err := sources.ResolveInternetRadioStreamURL(r.Context(), &http.Client{Timeout: 15 * time.Second}, station.StreamURL); err == nil && resolved != "" {
		streamURL = resolved
	} else if err != nil {
		log.Printf("internet radio stream resolver failed for %s: %v", station.ID, err)
	}
	http.Redirect(w, r, streamURL, http.StatusTemporaryRedirect)
}

func (s *Server) internetRadioResponse(r *http.Request, station sources.InternetRadioStation) internetRadioStationResponse {
	id := url.PathEscape(station.ID)
	resp := internetRadioStationResponse{
		InternetRadioStation: station,
		PublicStreamURL:      publicURL(r, "/internet-radio/"+id+"/stream"),
		PlaylistURL:          publicURL(r, "/internet-radio/"+id+"/playlist.m3u"),
	}
	if strings.TrimSpace(station.CoverID) != "" {
		resp.CoverURL = publicURL(r, "/api/v1/media/covers/"+url.PathEscape(station.CoverID)+"/image")
	}
	return resp
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
	case errors.Is(err, sources.ErrPodcastNotFilesystem):
		writeError(w, http.StatusBadRequest, "podcast must be backed by a local library folder")
	case errors.Is(err, sources.ErrPodcastAlreadyHasFeed):
		writeError(w, http.StatusConflict, "podcast already has an rss feed attached")
	case errors.Is(err, sources.ErrFeedURLInUse):
		writeError(w, http.StatusConflict, "rss feed url is already subscribed on another podcast")
	case errors.Is(err, sources.ErrInvalidPollInterval):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, sources.ErrInvalidProbeInterval):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeCoverUploadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, covers.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "cover service is not configured")
	case errors.Is(err, covers.ErrUnsupportedType):
		writeError(w, http.StatusBadRequest, "cover must be an image file")
	case errors.Is(err, covers.ErrTooLarge):
		writeError(w, http.StatusBadRequest, "cover exceeds maximum size")
	case errors.Is(err, covers.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "invalid cover upload")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
