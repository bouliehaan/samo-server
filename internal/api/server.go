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
	"github.com/bouliehaan/samo-server/internal/lastfm"
	"github.com/bouliehaan/samo-server/internal/libraries"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/podcastcache"
	"github.com/bouliehaan/samo-server/internal/podcaststream"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/search"
	"github.com/bouliehaan/samo-server/internal/shelfuser"
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/subsonic"
	"github.com/bouliehaan/samo-server/internal/users"
)

type ServerOptions struct {
	APIToken      string
	Catalog       *catalog.Service
	Libraries     *libraries.Service
	Playback      *playback.Service
	Covers        *covers.Service
	Files         *files.Service
	Metadata      *metadata.Service
	MetadataApply *metadata.MetadataApplyService
	Playlists     *playlists.Service
	PodcastStream *podcaststream.Service
	PodcastCache  *podcastcache.Service
	Search        *search.Service
	ShelfUser     *shelfuser.Service
	Radio         *radio.Service
	Sources       *sources.Service
	LastFM        *lastfm.Service
	Users         *users.Service
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
	metadataApply *metadata.MetadataApplyService
	playlists     *playlists.Service
	podcastStream *podcaststream.Service
	podcastCache  *podcastcache.Service
	search        *search.Service
	shelfUser     *shelfuser.Service
	mux           *http.ServeMux
	radio         *radio.Service
	sources       *sources.Service
	lastfm        *lastfm.Service
	users         *users.Service
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
	podcastStreamService := options.PodcastStream
	if podcastStreamService == nil {
		podcastStreamService = podcaststream.New()
	}

	searchService := options.Search
	if searchService == nil {
		searchService = search.New()
	}

	server := &Server{
		apiToken:      strings.TrimSpace(options.APIToken),
		catalog:       catalogService,
		libraries:     options.Libraries,
		playback:      options.Playback,
		covers:        options.Covers,
		files:         options.Files,
		metadata:      metadataService,
		metadataApply: options.MetadataApply,
		playlists:     options.Playlists,
		podcastStream: podcastStreamService,
		podcastCache:  options.PodcastCache,
		search:        searchService,
		shelfUser:     options.ShelfUser,
		mux:           http.NewServeMux(),
		radio:         radioService,
		sources:       options.Sources,
		lastfm:        options.LastFM,
		users:         options.Users,
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
	s.mux.HandleFunc("GET /setup", s.setupPage)
	s.mux.HandleFunc("GET /setup/", s.setupPage)
	s.mux.HandleFunc("GET /health", s.health)

	s.mux.HandleFunc("POST /api/v1/auth/login", s.loginUser)

	// Setup routes intentionally bypass requireUser so a first-time admin
	// can be created without a token. Each handler self-checks whether
	// setup is still pending and falls back to requireAdmin otherwise.
	s.mux.HandleFunc("GET /api/v1/setup/status", s.getSetupStatus)
	s.mux.HandleFunc("POST /api/v1/setup/admin", s.createSetupAdmin)
	s.mux.HandleFunc("GET /api/v1/setup/directories", s.browseSetupDirectories)
	s.mux.HandleFunc("POST /api/v1/setup/libraries", s.createSetupLibrary)
	s.mux.HandleFunc("POST /api/v1/setup/scan", s.runSetupScan)
	s.mux.HandleFunc("POST /api/v1/setup/complete", s.completeSetup)

	s.handleAPI("GET /api/v1/users/me", s.getCurrentUser)
	s.handleAPI("PATCH /api/v1/users/me", s.updateCurrentUser)
	s.handleAPI("GET /api/v1/users/me/tokens", s.listUserTokens)
	s.handleAPI("POST /api/v1/users/me/tokens", s.createUserToken)
	s.handleAPI("DELETE /api/v1/users/me/tokens/{id}", s.revokeUserToken)
	s.handleAPI("GET /api/v1/users", s.listUsers)
	s.handleAPI("POST /api/v1/users", s.createUser)

	s.handleAPI("GET /api/v1/radio/stations", s.listStations)
	s.handleAPI("GET /api/v1/radio/stations/{id}", s.getStation)
	s.handleAPI("GET /api/v1/radio/stations/{id}/now", s.getNow)
	s.handleAPI("GET /api/v1/radio/stations/{id}/schedule", s.getSchedule)

	s.handleAPI("GET /api/v1/radio/admin/stations", s.listRadioStationRecords)
	s.handleAPI("POST /api/v1/radio/admin/stations", s.createRadioStation)
	s.handleAPI("GET /api/v1/radio/admin/stations/{id}", s.getRadioStationRecord)
	s.handleAPI("PATCH /api/v1/radio/admin/stations/{id}", s.updateRadioStation)
	s.handleAPI("DELETE /api/v1/radio/admin/stations/{id}", s.deleteRadioStation)
	s.handleAPI("POST /api/v1/radio/admin/stations/{id}/items", s.addRadioStationItem)
	s.handleAPI("DELETE /api/v1/radio/admin/items/{itemId}", s.deleteRadioStationItem)

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

	s.handleAPI("GET /api/v1/lastfm/status", s.getLastFMStatus)
	s.handleAPI("POST /api/v1/lastfm/auth/begin", s.beginLastFMAuth)
	s.handleAPI("POST /api/v1/lastfm/auth/complete", s.completeLastFMAuth)
	s.handleAPI("DELETE /api/v1/lastfm/auth/session", s.disconnectLastFM)
	s.handleAPI("POST /api/v1/lastfm/queue/flush", s.flushLastFMQueue)
	s.handleAPI("GET /api/v1/lastfm/queue", s.listLastFMQueue)
	s.handleAPI("GET /api/v1/lastfm/history", s.listLastFMHistory)

	s.handleAPI("POST /api/v1/scrobble/events", s.postScrobbleEvent)

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
	s.handleAPI("POST /api/v1/metadata/apply/preview", s.previewMetadataApply)
	s.handleAPI("POST /api/v1/metadata/apply", s.applyMetadata)
	s.handleAPI("GET /api/v1/metadata/overrides/{targetKind}/{targetId}", s.getMetadataOverride)
	s.handleAPI("DELETE /api/v1/metadata/overrides/{targetKind}/{targetId}", s.deleteMetadataOverride)
	s.handleAPI("PATCH /api/v1/metadata/overrides/{targetKind}/{targetId}", s.clearMetadataOverrideFields)

	s.handleAPI("GET /api/v1/music/artists", s.listMusicArtists)
	s.handleAPI("GET /api/v1/music/artists/{id}", s.getMusicArtist)
	s.handleAPI("GET /api/v1/music/albums", s.listMusicAlbums)
	s.handleAPI("GET /api/v1/music/albums/{id}", s.getMusicAlbum)
	s.handleAPI("GET /api/v1/music/tracks", s.listMusicTracks)
	s.handleAPI("GET /api/v1/music/tracks/{id}", s.getMusicTrack)
	s.handleAPI("GET /api/v1/music/genres", s.listMusicGenres)
	s.handleAPI("GET /api/v1/music/playlists", s.listMusicPlaylists)
	s.handleAPI("GET /api/v1/music/playlists/{id}", s.getMusicPlaylist)
	s.handleAPI("POST /api/v1/music/playlists", s.createMusicPlaylist)
	s.handleAPI("PATCH /api/v1/music/playlists/{id}", s.updateMusicPlaylist)
	s.handleAPI("DELETE /api/v1/music/playlists/{id}", s.deleteMusicPlaylist)
	s.handleAPI("GET /api/v1/music/browse/favorites", s.browseMusicFavorites)
	s.handleAPI("GET /api/v1/music/browse/starred", s.browseMusicStarred)
	s.handleAPI("GET /api/v1/music/browse/recently-played", s.browseMusicRecentlyPlayed)
	s.handleAPI("GET /api/v1/music/browse/recently-added", s.browseMusicRecentlyAdded)
	s.handleAPI("GET /api/v1/music/search", s.searchMusic)

	s.handleAPI("GET /api/v1/shelf/libraries", s.listShelfLibraries)
	s.handleAPI("GET /api/v1/shelf/libraries/{id}", s.getShelfLibrary)
	s.handleAPI("GET /api/v1/shelf/items", s.listShelfItems)
	s.handleAPI("GET /api/v1/shelf/items/{id}", s.getShelfItem)
	s.handleAPI("GET /api/v1/shelf/audiobooks", s.listAudiobooks)
	s.handleAPI("GET /api/v1/shelf/authors", s.listShelfAuthors)
	s.handleAPI("GET /api/v1/shelf/authors/{id}", s.getShelfAuthor)
	s.handleAPI("GET /api/v1/shelf/authors/{id}/items", s.listShelfAuthorItems)
	s.handleAPI("GET /api/v1/shelf/series", s.listShelfSeries)
	s.handleAPI("GET /api/v1/shelf/series/{id}", s.getShelfSeries)
	s.handleAPI("GET /api/v1/shelf/series/{id}/items", s.listShelfSeriesItems)
	s.handleAPI("GET /api/v1/shelf/items/{id}/bookmarks", s.listShelfItemBookmarks)
	s.handleAPI("POST /api/v1/shelf/items/{id}/bookmarks", s.createShelfItemBookmark)
	s.handleAPI("PATCH /api/v1/shelf/bookmarks/{id}", s.updateShelfBookmark)
	s.handleAPI("DELETE /api/v1/shelf/bookmarks/{id}", s.deleteShelfBookmark)
	s.handleAPI("GET /api/v1/shelf/collections", s.listShelfCollections)
	s.handleAPI("POST /api/v1/shelf/collections", s.createShelfCollection)
	s.handleAPI("GET /api/v1/shelf/collections/{id}", s.getShelfCollection)
	s.handleAPI("PATCH /api/v1/shelf/collections/{id}", s.updateShelfCollection)
	s.handleAPI("DELETE /api/v1/shelf/collections/{id}", s.deleteShelfCollection)
	s.handleAPI("GET /api/v1/shelf/items/{id}/sessions", s.listShelfItemSessions)
	s.handleAPI("GET /api/v1/shelf/listening-sessions", s.listShelfListeningSessions)
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
	s.handleAPI("PATCH /api/v1/internet-radio/stations/{id}", s.updateInternetRadioStation)
	s.handleAPI("DELETE /api/v1/internet-radio/stations/{id}", s.deleteInternetRadioStation)
	s.handleAPI("POST /api/v1/internet-radio/stations/{id}/probe", s.probeInternetRadioStation)
	s.handleAPI("POST /api/v1/internet-radio/stations/probe", s.runInternetRadioProbeCycle)

	s.mux.HandleFunc("GET /radio/{id}/playlist.m3u", s.playlist)
	s.mux.HandleFunc("GET /radio/{id}/stream", s.stream)
	s.mux.HandleFunc("GET /internet-radio/{id}/playlist.m3u", s.internetRadioPlaylist)
	s.mux.HandleFunc("GET /internet-radio/{id}/stream", s.internetRadioStream)

	subsonicHandler := subsonic.New(subsonic.Options{
		Catalog:       s.catalog,
		Search:        s.search,
		Libraries:     s.libraries,
		Files:         s.files,
		LastFM:        s.lastfm,
		Users:         s.users,
		APIToken:      s.apiToken,
		ServerVersion: "0.1.0",
	})
	s.mux.HandleFunc("GET /rest/{action}", subsonicHandler.ServeHTTP)
}

func (s *Server) handleAPI(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, s.requireUser(handler))
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
  <title>SAMO SERVER</title>
  <style>` + samoBaseCSS + `</style>
  <style>
    .stations-empty {
      padding: 22px;
      font-family: var(--mono);
      font-size: 0.85rem;
      color: var(--text-dim);
      border: 1px dashed var(--line);
      background: var(--surface);
    }
    .station {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 24px;
      padding: 20px 22px;
      border: 1px solid var(--line);
      background: var(--surface);
      align-items: center;
    }
    .station + .station { border-top: 0; }
    .station .name {
      font-family: var(--sans);
      font-size: 1.25rem;
      font-weight: 800;
      letter-spacing: -0.01em;
      margin: 0 0 6px;
    }
    .station .description { color: var(--text-dim); font-size: 0.92rem; margin: 0 0 12px; max-width: 60ch; }
    .station .now {
      font-family: var(--mono);
      font-size: 0.78rem;
      letter-spacing: 0.14em;
      text-transform: uppercase;
      color: var(--accent);
      display: flex;
      align-items: center;
      gap: 8px;
    }
    .station .now .dot {
      width: 7px; height: 7px; background: var(--accent); display: inline-block;
      box-shadow: 0 0 10px var(--accent);
      animation: pulse 1.6s ease-in-out infinite;
    }
    .station .now.idle { color: var(--muted); }
    .station .now.idle .dot { background: var(--muted); box-shadow: none; animation: none; }
    .station .now .title { color: var(--text); font-family: var(--mono); }
    .station .now .artist { color: var(--text-dim); }
    @keyframes pulse {
      0%, 100% { opacity: 1; }
      50% { opacity: 0.35; }
    }
    .station .links { display: flex; gap: 8px; flex-direction: column; align-items: flex-end; }
    .section-head {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      margin-bottom: 14px;
    }
    .section-head h2 {
      margin: 0;
      font-family: var(--mono);
      font-size: 0.78rem;
      letter-spacing: 0.22em;
      text-transform: uppercase;
      color: var(--text-dim);
    }
    .section-head .meta {
      font-family: var(--mono);
      font-size: 0.7rem;
      letter-spacing: 0.18em;
      color: var(--muted);
    }
  </style>
</head>
<body>
  <div class="grid-bg"></div>
  <main>
    <header class="samo-head">
      <div class="wordmark">
        <div class="word">SAMO</div>
        <div class="word dim">SERVER</div>
        <div class="status">
          <span class="dot"></span><span class="status-text">ONLINE · CATALOG READY</span>
        </div>
      </div>
      <div class="ledger">
        <div><span class="label">PROTOCOL</span><span class="value">SAMO-NATIVE V1</span></div>
        <div><span class="label">STATIONS</span><span class="value">{{len .Stations}}</span></div>
      </div>
    </header>

    <section>
      <div class="section-head">
        <h2>// RADIO</h2>
        <div class="meta">[ STATION LOOP ]</div>
      </div>
      {{if .Stations}}
        {{range .Stations}}
        <div class="station">
          <div>
            <h3 class="name">{{.Name}}</h3>
            {{if .Description}}<p class="description">{{.Description}}</p>{{end}}
            {{if .Now}}
              <div class="now"><span class="dot"></span><span>NOW</span><span class="title">{{.Now.Title}}</span>{{if .Now.Artist}}<span class="artist">/ {{.Now.Artist}}</span>{{end}}</div>
            {{else}}
              <div class="now idle"><span class="dot"></span><span>IDLE</span></div>
            {{end}}
          </div>
          <div class="links">
            <a class="btn ghost" href="{{.StreamPath}}">STREAM &rarr;</a>
            <a class="btn ghost" href="{{.PlaylistPath}}">M3U &rarr;</a>
          </div>
        </div>
        {{end}}
      {{else}}
        <div class="stations-empty">// no stations programmed yet · use /api/v1/radio/admin to add one</div>
      {{end}}
    </section>
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
	if status, err := s.computeSetupStatus(r.Context()); err == nil && status.NeedsSetup {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}

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
