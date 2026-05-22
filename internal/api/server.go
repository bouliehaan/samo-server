package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/files"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/sources"
)

type ServerOptions struct {
	APIToken      string
	Catalog       *catalog.Service
	Libraries     *libraries.Service
	Playback      *playback.Service
	Covers        *covers.Service
	Files         *files.Service
	Metadata      *metadata.Service
	Radio         *radio.Service
	Sources       *sources.Service
	ReloadCatalog func(context.Context) error
}

type Server struct {
	apiToken      string
	catalog       *catalog.Service
	libraries     *libraries.Service
	playback      *playback.Service
	covers        *covers.Service
	files         *files.Service
	metadata      *metadata.Service
	mux           *http.ServeMux
	radio         *radio.Service
	sources       *sources.Service
	reloadCatalog func(context.Context) error
}

func NewServer(options ServerOptions) http.Handler {
	catalogService := options.Catalog
	if catalogService == nil {
		catalogService = catalog.NewService(catalog.Seed{})
	}
	metadataService := options.Metadata
	if metadataService == nil {
		metadataService = metadata.NewService(metadata.ServiceOptions{})
	}
	radioService := options.Radio
	if radioService == nil {
		radioService, _ = radio.NewService(radio.Config{})
	}

	server := &Server{
		apiToken:      strings.TrimSpace(options.APIToken),
		catalog:       catalogService,
		libraries:     options.Libraries,
		playback:      options.Playback,
		covers:        options.Covers,
		files:         options.Files,
		metadata:      metadataService,
		mux:           http.NewServeMux(),
		radio:         radioService,
		sources:       options.Sources,
		reloadCatalog: options.ReloadCatalog,
	}
	server.routes()
	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.home)
	s.mux.HandleFunc("GET /health", s.health)

	s.handleAPI("GET /api/v1/radio/stations", s.listStations)
	s.handleAPI("GET /api/v1/radio/stations/{id}", s.getStation)
	s.handleAPI("GET /api/v1/radio/stations/{id}/now", s.getNow)
	s.handleAPI("GET /api/v1/radio/stations/{id}/schedule", s.getSchedule)

	s.handleAPI("GET /api/v1/catalog/overview", s.catalogOverview)
	s.handleAPI("GET /api/v1/catalog/manifest", s.catalogManifest)

	s.handleAPI("GET /api/v1/libraries", s.listLibraries)
	s.handleAPI("GET /api/v1/libraries/{id}", s.getLibrary)
	s.handleAPI("POST /api/v1/libraries", s.createLibrary)
	s.handleAPI("PATCH /api/v1/libraries/{id}", s.updateLibrary)
	s.handleAPI("DELETE /api/v1/libraries/{id}", s.deleteLibrary)
	s.handleAPI("POST /api/v1/libraries/{id}/scan", s.scanLibrary)
	s.handleAPI("POST /api/v1/scan", s.scanAllLibraries)
	s.handleAPI("GET /api/v1/scan/jobs", s.listScanJobs)
	s.handleAPI("GET /api/v1/scan/jobs/{id}", s.getScanJob)

	s.handleAPI("GET /api/v1/playback/{kind}/{id}", s.getPlayback)
	s.handleAPI("PUT /api/v1/playback/{kind}/{id}", s.putPlayback)
	s.handleAPI("PATCH /api/v1/playback/{kind}/{id}", s.patchPlayback)

	s.handleAPI("GET /api/v1/media/covers/{id}", s.getExtractedCover)
	s.handleAPI("GET /api/v1/media/covers/{id}/image", s.serveExtractedCover)

	s.handleAPI("GET /api/v1/media/files/{id}", s.getMediaFile)
	s.handleAPI("GET /api/v1/media/files/{id}/stream", s.streamMediaFile)
	s.handleAPI("GET /api/v1/music/tracks/{id}/stream", s.streamMusicTrack)
	s.handleAPI("GET /api/v1/music/albums/{id}/cover", s.serveMusicAlbumCover)
	s.handleAPI("GET /api/v1/shelf/items/{id}/stream", s.streamShelfItem)
	s.handleAPI("GET /api/v1/shelf/items/{id}/cover", s.serveShelfItemCover)
	s.handleAPI("GET /api/v1/shelf/episodes/{id}/stream", s.streamShelfEpisode)

	s.handleAPI("GET /api/v1/metadata/providers", s.listMetadataProviders)
	s.handleAPI("GET /api/v1/metadata/search", s.searchMetadata)

	s.handleAPI("GET /api/v1/music/artists", s.listMusicArtists)
	s.handleAPI("GET /api/v1/music/artists/{id}", s.getMusicArtist)
	s.handleAPI("GET /api/v1/music/albums", s.listMusicAlbums)
	s.handleAPI("GET /api/v1/music/albums/{id}", s.getMusicAlbum)
	s.handleAPI("GET /api/v1/music/tracks", s.listMusicTracks)
	s.handleAPI("GET /api/v1/music/tracks/{id}", s.getMusicTrack)
	s.handleAPI("GET /api/v1/music/genres", s.listMusicGenres)
	s.handleAPI("GET /api/v1/music/playlists", s.listMusicPlaylists)
	s.handleAPI("GET /api/v1/music/playlists/{id}", s.getMusicPlaylist)
	s.handleAPI("GET /api/v1/music/search", s.searchMusic)

	s.handleAPI("GET /api/v1/shelf/libraries", s.listShelfLibraries)
	s.handleAPI("GET /api/v1/shelf/libraries/{id}", s.getShelfLibrary)
	s.handleAPI("GET /api/v1/shelf/items", s.listShelfItems)
	s.handleAPI("GET /api/v1/shelf/items/{id}", s.getShelfItem)
	s.handleAPI("GET /api/v1/shelf/audiobooks", s.listAudiobooks)
	s.handleAPI("GET /api/v1/shelf/authors", s.listShelfAuthors)
	s.handleAPI("GET /api/v1/shelf/authors/{id}", s.getShelfAuthor)
	s.handleAPI("GET /api/v1/shelf/series", s.listShelfSeries)
	s.handleAPI("GET /api/v1/shelf/series/{id}", s.getShelfSeries)
	s.handleAPI("GET /api/v1/shelf/podcasts", s.listPodcasts)
	s.handleAPI("GET /api/v1/shelf/podcast-feeds", s.listPodcastFeeds)
	s.handleAPI("POST /api/v1/shelf/podcast-feeds", s.createPodcastFeed)
	s.handleAPI("GET /api/v1/shelf/podcast-feeds/{id}", s.getPodcastFeed)
	s.handleAPI("PATCH /api/v1/shelf/podcast-feeds/{id}", s.updatePodcastFeed)
	s.handleAPI("POST /api/v1/shelf/podcast-feeds/poll", s.runPodcastPollCycle)
	s.handleAPI("POST /api/v1/shelf/podcast-feeds/{id}/refresh", s.refreshPodcastFeed)
	s.handleAPI("DELETE /api/v1/shelf/podcast-feeds/{id}", s.deletePodcastFeed)
	s.handleAPI("GET /api/v1/shelf/episodes", s.listPodcastEpisodes)
	s.handleAPI("GET /api/v1/shelf/episodes/{id}", s.getPodcastEpisode)
	s.handleAPI("GET /api/v1/shelf/search", s.searchShelf)

	s.handleAPI("GET /api/v1/internet-radio/stations", s.listInternetRadioStations)
	s.handleAPI("POST /api/v1/internet-radio/stations", s.createInternetRadioStation)
	s.handleAPI("GET /api/v1/internet-radio/stations/{id}", s.getInternetRadioStation)
	s.handleAPI("DELETE /api/v1/internet-radio/stations/{id}", s.deleteInternetRadioStation)

	s.mux.HandleFunc("GET /radio/{id}/playlist.m3u", s.playlist)
	s.mux.HandleFunc("GET /radio/{id}/stream", s.stream)
	s.mux.HandleFunc("GET /internet-radio/{id}/playlist.m3u", s.internetRadioPlaylist)
	s.mux.HandleFunc("GET /internet-radio/{id}/stream", s.internetRadioStream)
}

func (s *Server) handleAPI(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, s.requireAPIAuth(handler))
}

type healthResponse struct {
	OK        bool      `json:"ok"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		OK:        true,
		Service:   "samo-server",
		Timestamp: time.Now().UTC(),
	})
}

type stationResponse struct {
	radio.StationSummary
	Now         *radio.ProgramSlot `json:"now,omitempty"`
	StreamURL   string             `json:"streamUrl"`
	PlaylistURL string             `json:"playlistUrl"`
}

func (s *Server) listStations(w http.ResponseWriter, r *http.Request) {
	stations := s.radio.ListStations()
	response := make([]stationResponse, 0, len(stations))

	for _, station := range stations {
		item := stationResponse{
			StationSummary: station,
			StreamURL:      publicURL(r, "/radio/"+url.PathEscape(station.ID)+"/stream"),
			PlaylistURL:    publicURL(r, "/radio/"+url.PathEscape(station.ID)+"/playlist.m3u"),
		}
		if now, err := s.radio.CurrentSlot(station.ID, time.Now().UTC()); err == nil {
			item.Now = &now
		}
		response = append(response, item)
	}

	writeJSON(w, http.StatusOK, response)
}

type stationDetailResponse struct {
	stationResponse
	Upcoming []radio.ProgramSlot `json:"upcoming"`
}

func (s *Server) getStation(w http.ResponseWriter, r *http.Request) {
	stationID := r.PathValue("id")
	station, ok := s.radio.Station(stationID)
	if !ok {
		writeError(w, http.StatusNotFound, "station not found")
		return
	}

	now, _ := s.radio.CurrentSlot(station.ID, time.Now().UTC())
	upcoming, err := s.radio.Upcoming(station.ID, time.Now().UTC(), 12)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stationDetailResponse{
		stationResponse: stationResponse{
			StationSummary: station,
			Now:            &now,
			StreamURL:      publicURL(r, "/radio/"+url.PathEscape(station.ID)+"/stream"),
			PlaylistURL:    publicURL(r, "/radio/"+url.PathEscape(station.ID)+"/playlist.m3u"),
		},
		Upcoming: upcoming,
	})
}

func (s *Server) getNow(w http.ResponseWriter, r *http.Request) {
	slot, err := s.radio.CurrentSlot(r.PathValue("id"), time.Now().UTC())
	if errors.Is(err, radio.ErrStationNotFound) {
		writeError(w, http.StatusNotFound, "station not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, slot)
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request) {
	from := time.Now().UTC()
	if rawFrom := strings.TrimSpace(r.URL.Query().Get("from")); rawFrom != "" {
		parsed, err := time.Parse(time.RFC3339, rawFrom)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be an RFC3339 timestamp")
			return
		}
		from = parsed.UTC()
	}

	limit := 24
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be a number")
			return
		}
		limit = parsed
	}

	slots, err := s.radio.Upcoming(r.PathValue("id"), from, limit)
	if errors.Is(err, radio.ErrStationNotFound) {
		writeError(w, http.StatusNotFound, "station not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, slots)
}

func (s *Server) playlist(w http.ResponseWriter, r *http.Request) {
	station, ok := s.radio.Station(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "station not found")
		return
	}

	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s\n", station.Name, publicURL(r, "/radio/"+url.PathEscape(station.ID)+"/stream"))
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	stationID := r.PathValue("id")
	contentType, ok := s.radio.ContentType(stationID)
	if !ok {
		writeError(w, http.StatusNotFound, "station not found")
		return
	}
	if _, err := s.radio.CurrentSlot(stationID, time.Now().UTC()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
		w = flushWriter{ResponseWriter: w, flusher: flusher}
	}

	err := s.radio.Stream(r.Context(), stationID, time.Now().UTC(), w)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("radio stream failed: %v", err)
	}
}

func (s *Server) requireAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" || tokenFromRequest(r) == s.apiToken {
			next(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Bearer realm="samo"`)
		writeError(w, http.StatusUnauthorized, "missing or invalid API token")
	}
}

func tokenFromRequest(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("bearer "):])
	}
	return strings.TrimSpace(r.Header.Get("X-Samo-Token"))
}

func publicURL(r *http.Request, path string) string {
	scheme := "http"
	if forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwardedProto != "" {
		scheme = strings.Split(forwardedProto, ",")[0]
	} else if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host + path
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("failed to write json response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type flushWriter struct {
	http.ResponseWriter
	flusher http.Flusher
}

func (w flushWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if n > 0 {
		w.flusher.Flush()
	}
	return n, err
}

var homeTemplate = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Samo Server</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    body { margin: 0; background: Canvas; color: CanvasText; }
    main { width: min(960px, calc(100vw - 32px)); margin: 48px auto; }
    h1 { font-size: clamp(2rem, 4vw, 4rem); margin: 0 0 8px; letter-spacing: 0; }
    p { color: color-mix(in srgb, CanvasText 70%, Canvas); line-height: 1.5; }
    table { border-collapse: collapse; width: 100%; margin-top: 32px; }
    th, td { border-bottom: 1px solid color-mix(in srgb, CanvasText 14%, Canvas); padding: 12px 8px; text-align: left; vertical-align: top; }
    th { font-size: 0.75rem; letter-spacing: 0; text-transform: uppercase; color: color-mix(in srgb, CanvasText 56%, Canvas); }
    a { color: LinkText; }
    .empty { border: 1px solid color-mix(in srgb, CanvasText 14%, Canvas); padding: 24px; margin-top: 32px; border-radius: 8px; }
  </style>
</head>
<body>
  <main>
    <h1>Samo Server</h1>
    <p>Unified listening server core with the first radio module online.</p>
    {{if .Stations}}
    <table>
      <thead><tr><th>Station</th><th>Now</th><th>Links</th></tr></thead>
      <tbody>
        {{range .Stations}}
        <tr>
          <td><strong>{{.Name}}</strong>{{if .Description}}<br>{{.Description}}{{end}}</td>
          <td>{{if .Now}}{{.Now.Title}}{{if .Now.Artist}} by {{.Now.Artist}}{{end}}{{else}}No current slot{{end}}</td>
          <td><a href="{{.StreamPath}}">Stream</a> · <a href="{{.PlaylistPath}}">M3U</a></td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="empty">No radio stations are configured yet.</div>
    {{end}}
  </main>
</body>
</html>`))

type homeStation struct {
	Name         string
	Description  string
	Now          *radio.ProgramSlot
	StreamPath   string
	PlaylistPath string
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	stations := s.radio.ListStations()
	view := struct {
		Stations []homeStation
	}{Stations: make([]homeStation, 0, len(stations))}

	for _, station := range stations {
		item := homeStation{
			Name:         station.Name,
			Description:  station.Description,
			StreamPath:   "/radio/" + url.PathEscape(station.ID) + "/stream",
			PlaylistPath: "/radio/" + url.PathEscape(station.ID) + "/playlist.m3u",
		}
		if now, err := s.radio.CurrentSlot(station.ID, time.Now().UTC()); err == nil {
			item.Now = &now
		}
		view.Stations = append(view.Stations, item)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := homeTemplate.Execute(w, view); err != nil {
		log.Printf("failed to render home page: %v", err)
	}
}
