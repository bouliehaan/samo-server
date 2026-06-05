package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/artistimages"
	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/channels"
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
	"github.com/bouliehaan/samo-server/internal/sources"
	"github.com/bouliehaan/samo-server/internal/users"
)

type ServerOptions struct {
	DB            *sql.DB
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
	Bookmarks     *bookmarks.Service
	Radio         *radio.Service
	Sources       *sources.Service
	LastFM        *lastfm.Service
	ArtistImages  *artistimages.Service
	Users         *users.Service
	Channels      *channels.Service
	ReloadCatalog func(context.Context) error
	// DisableInitialInternetRadioProbe turns off the fire-and-forget
	// post-create probe. Useful for tests that close DB/tempdirs immediately.
	DisableInitialInternetRadioProbe bool
	StartedAt                        time.Time
}

type Server struct {
	db                               *sql.DB
	apiToken                         string
	catalog                          *catalog.Service
	libraries                        *libraries.Service
	playback                         *playback.Service
	covers                           *covers.Service
	files                            *files.Service
	metadata                         *metadata.Service
	metadataApply                    *metadata.MetadataApplyService
	playlists                        *playlists.Service
	podcastStream                    *podcaststream.Service
	podcastCache                     *podcastcache.Service
	search                           *search.Service
	bookmarks                        *bookmarks.Service
	mux                              *http.ServeMux
	radio                            *radio.Service
	sources                          *sources.Service
	lastfm                           *lastfm.Service
	artistImages                     *artistimages.Service
	users                            *users.Service
	channels                         *channels.Service
	reloadCatalog                    func(context.Context) error
	disableInitialInternetRadioProbe bool
	startedAt                        time.Time
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
		db:                               options.DB,
		apiToken:                         strings.TrimSpace(options.APIToken),
		catalog:                          catalogService,
		libraries:                        options.Libraries,
		playback:                         options.Playback,
		covers:                           options.Covers,
		files:                            options.Files,
		metadata:                         metadataService,
		metadataApply:                    options.MetadataApply,
		playlists:                        options.Playlists,
		podcastStream:                    podcastStreamService,
		podcastCache:                     options.PodcastCache,
		search:                           searchService,
		bookmarks:                        options.Bookmarks,
		mux:                              http.NewServeMux(),
		radio:                            radioService,
		sources:                          options.Sources,
		lastfm:                           options.LastFM,
		artistImages:                     options.ArtistImages,
		users:                            options.Users,
		channels:                         options.Channels,
		reloadCatalog:                    options.ReloadCatalog,
		disableInitialInternetRadioProbe: options.DisableInitialInternetRadioProbe,
		startedAt:                        options.StartedAt,
	}
	if server.startedAt.IsZero() {
		server.startedAt = time.Now()
	}
	server.routes()
	return WithCORS(server)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.home)
	s.mux.HandleFunc("GET /app", s.appPage)
	s.mux.HandleFunc("GET /app/", s.appPage)
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("GET /login/", s.loginPage)
	s.mux.HandleFunc("GET /setup", s.setupPage)
	s.mux.HandleFunc("GET /setup/", s.setupPage)
	s.mux.HandleFunc("GET /health", s.health)

	// Browser favicon (public — fetched before login). Light/dark variants are
	// selected by the <link media="(prefers-color-scheme:…)"> tags in the page
	// heads; /favicon.ico is the bare-request fallback browsers ask for on every
	// page, so it gets the full logo.
	s.mux.HandleFunc("GET /favicon-light.png", serveFavicon(faviconLightPNG))
	s.mux.HandleFunc("GET /favicon-dark.png", serveFavicon(faviconDarkPNG))
	s.mux.HandleFunc("GET /favicon.ico", serveFavicon(faviconDarkPNG))

	s.mux.HandleFunc("POST /api/v1/auth/login", s.loginUser)
	s.handleAPI("POST /api/v1/auth/stream-token", s.issueStreamToken)

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
	s.handleAPI("GET /api/v1/catalog/recently-added", s.catalogRecentlyAdded)
	s.handleAPI("GET /api/v1/server/activity", s.serverActivity)
	s.handleAPI("POST /api/v1/catalog/reload", s.postCatalogReload)
	s.handleAPI("GET /api/v1/catalog/manifest", s.catalogManifest)
	s.handleAPI("GET /api/v1/catalog/sync/manifest", s.catalogSyncManifest)

	s.handleAPI("GET /api/v1/libraries", s.listLibraries)
	s.handleAPI("GET /api/v1/libraries/{id}", s.getLibrary)
	s.handleAPI("POST /api/v1/libraries", s.createLibrary)
	s.handleAPI("PATCH /api/v1/libraries/{id}", s.updateLibrary)
	s.handleAPI("DELETE /api/v1/libraries/{id}", s.deleteLibrary)
	s.handleAPI("POST /api/v1/libraries/{id}/scan", s.scanLibrary)
	s.handleAPI("POST /api/v1/scan", s.scanAllLibraries)
	s.handleAPI("GET /api/v1/scan/jobs", s.listScanJobs)
	s.handleAPI("GET /api/v1/scan/jobs/{id}", s.getScanJob)
	s.handleAPI("POST /api/v1/scan/jobs/{id}/cancel", s.cancelScanJob)
	s.handleAPI("POST /api/v1/scan/cancel", s.cancelActiveScan)
	s.handleAPI("GET /api/v1/missing-files", s.listMissingFiles)
	s.handleAPI("DELETE /api/v1/missing-files", s.removeAllMissingFiles)
	s.handleAPI("DELETE /api/v1/missing-files/{id}", s.removeMissingFile)

	s.handleAPI("GET /api/v1/playback/{kind}/{id}", s.getPlayback)
	s.handleAPI("PUT /api/v1/playback/{kind}/{id}", s.putPlayback)
	s.handleAPI("PATCH /api/v1/playback/{kind}/{id}", s.patchPlayback)

	s.handleAPI("GET /api/v1/lastfm/status", s.getLastFMStatus)
	s.handleAPI("GET /api/v1/lastfm/config", s.getLastFMConfig)
	s.handleAPI("PUT /api/v1/lastfm/config", s.updateLastFMConfig)
	s.handleAPI("DELETE /api/v1/lastfm/config", s.clearLastFMConfig)
	s.handleAPI("POST /api/v1/lastfm/auth/begin", s.beginLastFMAuth)
	s.handleAPI("POST /api/v1/lastfm/auth/complete", s.completeLastFMAuth)
	s.handleAPI("DELETE /api/v1/lastfm/auth/session", s.disconnectLastFM)
	s.handleAPI("POST /api/v1/lastfm/queue/flush", s.flushLastFMQueue)
	s.handleAPI("GET /api/v1/lastfm/queue", s.listLastFMQueue)
	s.handleAPI("GET /api/v1/lastfm/history", s.listLastFMHistory)

	s.handleAPI("POST /api/v1/scrobble/events", s.postScrobbleEvent)

	s.handleAPI("GET /api/v1/media/covers/{id}", s.getExtractedCover)
	s.handleAPI("GET /api/v1/media/covers/{id}/image", s.serveExtractedCover)
	s.handleAPI("GET /api/v1/media/images/{id}/image", s.serveMetadataImage)

	s.handleAPI("GET /api/v1/media/files/{id}", s.getMediaFile)
	s.handleAPI("GET /api/v1/media/files/{id}/stream", s.streamMediaFile)
	s.handleAPI("GET /api/v1/music/tracks/{id}/stream", s.streamMusicTrack)
	s.handleAPI("GET /api/v1/music/albums/{id}/cover", s.serveMusicAlbumCover)
	s.handleAPI("GET /api/v1/audiobooks/{id}/stream", s.streamAudiobook)
	s.handleAPI("GET /api/v1/audiobooks/{id}/cover", s.serveAudiobookCover)
	// Note: /api/v1/podcasts/{id}/cover would clash with the
	// /api/v1/podcasts/episodes/{id} routes — Go's ServeMux can't decide
	// between `/podcasts/episodes/cover` matching either pattern. Cover
	// + stream sit under /shows/ so each podcast verb has an unambiguous
	// path.
	s.handleAPI("GET /api/v1/podcasts/shows/{id}/cover", s.servePodcastCover)
	s.handleAPI("POST /api/v1/podcasts/shows/{id}/cover", s.uploadPodcastCover)
	s.handleAPI("DELETE /api/v1/podcasts/shows/{id}", s.deletePodcastShow)
	s.handleAPI("GET /api/v1/podcasts/shows/{id}/episodes", s.listPodcastShowEpisodes)
	s.handleAPI("GET /api/v1/podcasts/episodes/{id}/stream", s.streamPodcastEpisode)
	s.handleAPI("GET /api/v1/podcasts/cache", s.getPodcastCacheSummary)
	s.handleAPI("DELETE /api/v1/podcasts/cache", s.clearPodcastCache)
	s.handleAPI("GET /api/v1/podcasts/episodes/{id}/cache", s.getPodcastEpisodeCache)
	s.handleAPI("POST /api/v1/podcasts/episodes/{id}/cache", s.cachePodcastEpisode)
	s.handleAPI("DELETE /api/v1/podcasts/episodes/{id}/cache", s.deletePodcastEpisodeCache)

	s.handleAPI("GET /api/v1/metadata/providers", s.listMetadataProviders)
	s.handleAPI("GET /api/v1/metadata/search", s.searchMetadata)
	s.handleAPI("POST /api/v1/metadata/apply/preview", s.previewMetadataApply)
	s.handleAPI("POST /api/v1/metadata/apply", s.applyMetadata)
	s.handleAPI("GET /api/v1/metadata/overrides/{targetKind}/{targetId}", s.getMetadataOverride)
	s.handleAPI("DELETE /api/v1/metadata/overrides/{targetKind}/{targetId}", s.deleteMetadataOverride)
	s.handleAPI("PATCH /api/v1/metadata/overrides/{targetKind}/{targetId}", s.clearMetadataOverrideFields)

	s.handleAPI("GET /api/v1/music/artists", s.listMusicArtists)
	s.handleAPI("GET /api/v1/music/artists/{id}", s.getMusicArtist)
	s.handleAPI("GET /api/v1/music/artists/{id}/albums", s.listMusicArtistAlbums)
	s.handleAPI("GET /api/v1/music/artists/{id}/cover", s.serveMusicArtistCover)
	s.handleAPI("POST /api/v1/music/artists/images/backfill", s.startArtistImageBackfill)
	s.handleAPI("GET /api/v1/music/artists/images/backfill", s.getArtistImageBackfill)
	s.handleAPI("POST /api/v1/music/artists/images/backfill/cancel", s.cancelArtistImageBackfill)
	s.handleAPI("GET /api/v1/music/albums", s.listMusicAlbums)
	s.handleAPI("GET /api/v1/music/albums/{id}", s.getMusicAlbum)
	s.handleAPI("GET /api/v1/music/albums/{id}/tracks", s.listMusicAlbumTracks)
	s.handleAPI("DELETE /api/v1/music/albums/{id}", s.deleteMusicAlbum)
	s.handleAPI("GET /api/v1/music/tracks", s.listMusicTracks)
	s.handleAPI("GET /api/v1/music/tracks/{id}", s.getMusicTrack)
	s.handleAPI("GET /api/v1/music/genres", s.listMusicGenres)
	s.handleAPI("GET /api/v1/music/playlists", s.listMusicPlaylists)
	s.handleAPI("GET /api/v1/music/playlists/{id}", s.getMusicPlaylist)
	s.handleAPI("GET /api/v1/music/playlists/{id}/tracks", s.listMusicPlaylistTracks)
	s.handleAPI("GET /api/v1/music/playlists/{id}/cover", s.serveMusicPlaylistCover)
	s.handleAPI("POST /api/v1/music/playlists/{id}/cover", s.uploadMusicPlaylistCover)
	s.handleAPI("POST /api/v1/music/playlists", s.createMusicPlaylist)
	s.handleAPI("POST /api/v1/music/playlists/import", s.importMusicPlaylist)
	s.handleAPI("PATCH /api/v1/music/playlists/{id}", s.updateMusicPlaylist)
	s.handleAPI("DELETE /api/v1/music/playlists/{id}", s.deleteMusicPlaylist)
	s.handleAPI("GET /api/v1/music/browse/favorites", s.browseMusicFavorites)
	s.handleAPI("GET /api/v1/music/browse/starred", s.browseMusicStarred)
	s.handleAPI("GET /api/v1/music/browse/recently-played", s.browseMusicRecentlyPlayed)
	s.handleAPI("GET /api/v1/music/browse/recently-added", s.browseMusicRecentlyAdded)
	s.handleAPI("GET /api/v1/music/browse/unplayed", s.browseMusicUnplayed)
	s.handleAPI("GET /api/v1/music/browse/discovery", s.browseMusicDiscovery)
	s.handleAPI("GET /api/v1/music/search", s.searchMusic)

	// Audiobook domain. Music, audiobooks, podcasts, and radio are all
	// independent product domains in Samo. Their URL namespaces match.
	s.handleAPI("GET /api/v1/audiobooks", s.listAudiobooks)
	s.handleAPI("GET /api/v1/audiobooks/{id}", s.getAudiobook)
	s.handleAPI("DELETE /api/v1/audiobooks/{id}", s.deleteAudiobook)
	s.handleAPI("GET /api/v1/audiobooks/{id}/bookmarks", s.listAudiobookBookmarks)
	s.handleAPI("POST /api/v1/audiobooks/{id}/bookmarks", s.createAudiobookBookmark)
	s.handleAPI("GET /api/v1/audiobooks/{id}/sessions", s.listAudiobookSessions)
	s.handleAPI("GET /api/v1/audiobooks/search", s.searchAudiobooks)
	s.handleAPI("GET /api/v1/contributors", s.listContributors)
	s.handleAPI("GET /api/v1/contributors/{id}", s.getContributor)
	s.handleAPI("GET /api/v1/contributors/{id}/audiobooks", s.listContributorAudiobooks)
	s.handleAPI("GET /api/v1/series", s.listSeries)
	s.handleAPI("GET /api/v1/series/{id}", s.getSeries)
	s.handleAPI("GET /api/v1/series/{id}/audiobooks", s.listSeriesAudiobooks)
	s.handleAPI("GET /api/v1/bookmarks", s.listUserBookmarks)
	s.handleAPI("PATCH /api/v1/bookmarks/{id}", s.updateBookmark)
	s.handleAPI("DELETE /api/v1/bookmarks/{id}", s.deleteBookmark)
	s.handleAPI("GET /api/v1/collections", s.listCollections)
	s.handleAPI("POST /api/v1/collections", s.createCollection)
	s.handleAPI("GET /api/v1/collections/{id}", s.getCollection)
	s.handleAPI("PATCH /api/v1/collections/{id}", s.updateCollection)
	s.handleAPI("DELETE /api/v1/collections/{id}", s.deleteCollection)
	s.handleAPI("GET /api/v1/listening-sessions", s.listListeningSessions)

	// Podcast domain. Shows and episodes are split into separate
	// /shows/ and /episodes/ prefixes; that keeps the ServeMux happy and
	// makes the URL shape match the data model.
	s.handleAPI("GET /api/v1/podcasts", s.listPodcasts)
	s.handleAPI("GET /api/v1/podcasts/shows/{id}", s.getPodcast)
	s.handleAPI("GET /api/v1/podcasts/episodes", s.listPodcastEpisodes)
	s.handleAPI("GET /api/v1/podcasts/episodes/{id}", s.getPodcastEpisode)
	s.handleAPI("GET /api/v1/podcasts/search", s.searchPodcasts)
	s.handleAPI("GET /api/v1/podcasts/feeds", s.listPodcastFeeds)
	s.handleAPI("POST /api/v1/podcasts/feeds", s.createPodcastFeed)
	s.handleAPI("POST /api/v1/podcasts/shows/{id}/feeds", s.attachPodcastShowFeed)
	s.handleAPI("GET /api/v1/podcasts/feeds/{id}", s.getPodcastFeed)
	s.handleAPI("PATCH /api/v1/podcasts/feeds/{id}", s.updatePodcastFeed)
	s.handleAPI("POST /api/v1/podcasts/feeds/poll", s.runPodcastPollCycle)
	s.handleAPI("POST /api/v1/podcasts/feeds/{id}/refresh", s.refreshPodcastFeed)
	s.handleAPI("DELETE /api/v1/podcasts/feeds/{id}", s.deletePodcastFeed)

	s.handleAPI("GET /api/v1/internet-radio/stations", s.listInternetRadioStations)
	s.handleAPI("POST /api/v1/internet-radio/stations", s.createInternetRadioStation)
	s.handleAPI("GET /api/v1/internet-radio/stations/{id}", s.getInternetRadioStation)
	s.handleAPI("PATCH /api/v1/internet-radio/stations/{id}", s.updateInternetRadioStation)
	s.handleAPI("DELETE /api/v1/internet-radio/stations/{id}", s.deleteInternetRadioStation)
	s.handleAPI("POST /api/v1/internet-radio/stations/{id}/probe", s.probeInternetRadioStation)
	s.handleAPI("POST /api/v1/internet-radio/stations/{id}/cover", s.uploadInternetRadioCover)
	s.handleAPI("POST /api/v1/internet-radio/stations/probe", s.runInternetRadioProbeCycle)

	s.mux.HandleFunc("GET /radio/{id}/playlist.m3u", s.playlist)
	s.mux.HandleFunc("GET /radio/{id}/stream", s.stream)
	s.mux.HandleFunc("GET /internet-radio/{id}/playlist.m3u", s.internetRadioPlaylist)
	s.mux.HandleFunc("GET /internet-radio/{id}/stream", s.internetRadioStream)

	// Channels: Samo-native 24/7 programmed radio. Admin CRUD lives
	// under /api/v1/channels/*; public stream + M3U live at /channels/*
	// so HLS/podcast-app/m3u-friendly tools can subscribe directly.
	s.handleAPI("GET /api/v1/channels", s.listChannels)
	s.handleAPI("POST /api/v1/channels", s.createChannel)
	s.handleAPI("GET /api/v1/channels/{id}", s.getChannel)
	s.handleAPI("PATCH /api/v1/channels/{id}", s.updateChannel)
	s.handleAPI("DELETE /api/v1/channels/{id}", s.deleteChannel)
	s.handleAPI("GET /api/v1/channels/{id}/sources", s.listChannelSources)
	s.handleAPI("POST /api/v1/channels/{id}/sources", s.createChannelSource)
	s.handleAPI("PATCH /api/v1/channels/{id}/sources/{sourceId}", s.updateChannelSource)
	s.handleAPI("DELETE /api/v1/channels/{id}/sources/{sourceId}", s.deleteChannelSource)
	s.handleAPI("GET /api/v1/channels/{id}/schedule", s.listChannelScheduleRules)
	s.handleAPI("POST /api/v1/channels/{id}/schedule", s.createChannelScheduleRule)
	s.handleAPI("DELETE /api/v1/channels/{id}/schedule/{ruleId}", s.deleteChannelScheduleRule)
	s.handleAPI("GET /api/v1/channels/{id}/now", s.channelNowPlaying)
	s.handleAPI("GET /api/v1/channels/{id}/recent", s.channelRecentPlays)
	s.handleAPI("POST /api/v1/channels/{id}/preview", s.channelPreviewNext)
	// Channel playlist and stream go through requireUser so a
	// stream_token query param works for <audio src=...> in browsers
	// without forcing every listener URL to carry a real Authorization
	// header. Same pattern as /api/v1/music/tracks/{id}/stream.
	s.mux.HandleFunc("GET /channels/{id}/playlist.m3u", s.requireUser(s.channelPlaylist))
	s.mux.HandleFunc("GET /channels/{id}/stream", s.requireUser(s.channelStream))
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

// home is the front door. Setup pending → wizard. Otherwise → /app (which
// handles its own login bounce when no token is present). No standalone
// landing page; the dashboard is the product.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if status, err := s.computeSetupStatus(r.Context()); err == nil && status.NeedsSetup {
		http.Redirect(w, r, "/setup", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/app", http.StatusFound)
}
