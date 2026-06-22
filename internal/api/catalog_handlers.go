package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type catalogManifestResponse struct {
	Version      string                `json:"version"`
	Description  string                `json:"description"`
	Auth         authManifest          `json:"auth"`
	Namespaces   []namespaceManifest   `json:"namespaces"`
	MetadataSets []metadataSetManifest `json:"metadataSets"`
	Routes       map[string][]string   `json:"routes"`
}

type authManifest struct {
	APIUsesBearerToken bool     `json:"apiUsesBearerToken"`
	AcceptedHeaders    []string `json:"acceptedHeaders"`
	PublicRoutes       []string `json:"publicRoutes"`
}

type namespaceManifest struct {
	Name        string `json:"name"`
	PathPrefix  string `json:"pathPrefix"`
	Description string `json:"description"`
}

type metadataSetManifest struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

func (s *Server) catalogOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.catalog.Overview())
}

func (s *Server) catalogRecentlyAdded(w http.ResponseWriter, r *http.Request) {
	page, err := readPage(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if page.Limit <= 0 || page.Limit > 50 {
		page.Limit = 50
	}
	writeJSON(w, http.StatusOK, s.catalog.ListRecentlyAdded(page))
}

// catalogSyncManifest returns the server clock plus every current entity ID
// (playlists scoped to the caller) so an incremental client sync can reconcile
// deletions: drop any locally-mirrored row whose ID is absent from the set.
func (s *Server) catalogSyncManifest(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.SyncManifest(principal.User.ID))
}

func (s *Server) postCatalogReload(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if err := s.reloadCatalogProjection(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) catalogManifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, catalogManifestResponse{
		Version:     "v1",
		Description: "Samo-native catalog API for music, audiobooks, podcasts, radio, and playback state.",
		Auth: authManifest{
			APIUsesBearerToken: true,
			AcceptedHeaders:    []string{"Authorization: Bearer <token>", "X-Samo-Token: <token>"},
			PublicRoutes:       []string{"/health", "/radio/{id}/playlist.m3u", "/radio/{id}/stream", "/internet-radio/{id}/playlist.m3u", "/internet-radio/{id}/stream"},
		},
		Namespaces: []namespaceManifest{
			{Name: "libraries", PathPrefix: "/api/v1/libraries", Description: "Filesystem library management and scan orchestration."},
			{Name: "playback", PathPrefix: "/api/v1/playback", Description: "Listening progress, ratings, favorites, and play counts."},
			{Name: "media", PathPrefix: "/api/v1/media", Description: "Catalog media file metadata and byte streaming."},
			{Name: "metadata", PathPrefix: "/api/v1/metadata", Description: "Explicit external metadata lookup providers and search."},
			{Name: "music", PathPrefix: "/api/v1/music", Description: "Music artists, albums, tracks, playlists, genres, and search."},
			{Name: "audiobooks", PathPrefix: "/api/v1/audiobooks", Description: "Audiobooks, contributors, series, bookmarks, collections, and search."},
			{Name: "podcasts", PathPrefix: "/api/v1/podcasts", Description: "Podcast shows, episodes, RSS feeds, and search."},
			{Name: "radio", PathPrefix: "/api/v1/radio", Description: "24/7 station metadata, now playing, and schedules."},
			{Name: "internetRadio", PathPrefix: "/api/v1/internet-radio", Description: "User-managed external internet radio streams."},
			{Name: "lastfm", PathPrefix: "/api/v1/lastfm", Description: "Last.fm account linking and native scrobbling."},
		},
		MetadataSets: []metadataSetManifest{
			{Name: "musicArtist", Fields: []string{"ids", "sort names", "biography", "country", "genres", "styles", "moods", "images", "external IDs", "counts", "playback state"}},
			{Name: "musicAlbum", Fields: []string{"album artists", "release dates", "release type/status", "label", "catalog number", "barcode", "genres", "images", "external IDs", "playback state"}},
			{Name: "musicTrack", Fields: []string{"artists", "album", "disc/track numbers", "audio technical metadata", "lyrics", "genres", "external IDs", "playback state"}},
			{Name: "audiobook", Fields: []string{"library info", "filesystem identity", "cover", "tags", "genres", "progress", "audio files", "chapters", "book metadata"}},
			{Name: "podcast", Fields: []string{"library info", "filesystem identity", "cover", "tags", "genres", "feed url", "site url", "podcast metadata"}},
			{Name: "podcastEpisode", Fields: []string{"podcast show", "title", "published date", "duration", "enclosure", "progress"}},
			{Name: "bookMetadata", Fields: []string{"title", "authors", "narrators", "series", "publisher", "published date", "description", "language", "ISBNs", "external IDs", "duration"}},
			{Name: "podcastMetadata", Fields: []string{"feed URL", "site URL", "owner", "language", "categories", "explicit flag", "episode count", "external IDs"}},
			{Name: "metadataCandidate", Fields: []string{"provider", "media type", "score", "title", "contributors", "descriptions", "dates", "genres", "covers", "external IDs", "links"}},
		},
		Routes: map[string][]string{
			"libraries": {
				"GET /api/v1/libraries",
				"GET /api/v1/libraries/{id}",
				"POST /api/v1/libraries",
				"PATCH /api/v1/libraries/{id}",
				"DELETE /api/v1/libraries/{id}",
				"POST /api/v1/libraries/{id}/scan",
				"POST /api/v1/scan",
				"GET /api/v1/scan/jobs",
				"GET /api/v1/scan/jobs/{id}",
			},
			"playback": {
				"GET /api/v1/playback/{kind}/{id}",
				"PUT /api/v1/playback/{kind}/{id}",
				"PATCH /api/v1/playback/{kind}/{id}",
			},
			"lastfm": {
				"GET /api/v1/lastfm/status",
				"GET /api/v1/lastfm/config",
				"PUT /api/v1/lastfm/config",
				"DELETE /api/v1/lastfm/config",
				"POST /api/v1/lastfm/auth/begin",
				"POST /api/v1/lastfm/auth/complete",
				"DELETE /api/v1/lastfm/auth/session",
				"GET /api/v1/lastfm/queue",
				"GET /api/v1/lastfm/history",
				"POST /api/v1/lastfm/queue/flush",
			},
			"scrobble": {
				"POST /api/v1/scrobble/events",
			},
			"media": {
				"GET /api/v1/media/covers/{id}",
				"GET /api/v1/media/covers/{id}/image",
				"GET /api/v1/media/files/{id}",
				"GET /api/v1/media/files/{id}/stream",
				"GET /api/v1/music/tracks/{id}/stream",
				"GET /api/v1/music/albums/{id}/cover",
				"GET /api/v1/audiobooks/{id}/stream",
				"GET /api/v1/audiobooks/{id}/cover",
				"GET /api/v1/podcasts/shows/{id}/cover",
				"GET /api/v1/podcasts/episodes/{id}/stream",
			},
			"metadata": {
				"GET /api/v1/metadata/providers",
				"GET /api/v1/metadata/search",
				"POST /api/v1/metadata/apply/preview",
				"POST /api/v1/metadata/apply",
				"GET /api/v1/metadata/overrides/{targetKind}/{targetId}",
				"DELETE /api/v1/metadata/overrides/{targetKind}/{targetId}",
				"PATCH /api/v1/metadata/overrides/{targetKind}/{targetId}",
			},
			"music": {
				"GET /api/v1/music/artists",
				"GET /api/v1/music/artists/{id}",
				"POST /api/v1/music/artists/images/backfill",
				"GET /api/v1/music/artists/images/backfill",
				"POST /api/v1/music/artists/images/backfill/cancel",
				"GET /api/v1/music/albums",
				"GET /api/v1/music/albums/{id}",
				"GET /api/v1/music/tracks",
				"GET /api/v1/music/tracks/{id}",
				"GET /api/v1/music/genres",
				"GET /api/v1/music/playlists",
				"GET /api/v1/music/playlists/{id}",
				"GET /api/v1/music/playlists/{id}/tracks",
				"POST /api/v1/music/playlists",
				"POST /api/v1/music/playlists/import",
				"PATCH /api/v1/music/playlists/{id}",
				"DELETE /api/v1/music/playlists/{id}",
				"GET /api/v1/music/browse/favorites",
				"GET /api/v1/music/browse/starred",
				"GET /api/v1/music/browse/recently-played",
				"GET /api/v1/music/browse/recently-added",
				"GET /api/v1/music/search?q=",
			},
			"audiobooks": {
				"GET /api/v1/audiobooks",
				"GET /api/v1/audiobooks/{id}",
				"GET /api/v1/audiobooks/search?q=",
				"GET /api/v1/contributors",
				"GET /api/v1/contributors/{id}",
				"GET /api/v1/contributors/{id}/audiobooks",
				"GET /api/v1/series",
				"GET /api/v1/series/{id}",
				"GET /api/v1/series/{id}/audiobooks",
				"GET /api/v1/audiobooks/{id}/bookmarks",
				"POST /api/v1/audiobooks/{id}/bookmarks",
				"GET /api/v1/bookmarks",
				"PATCH /api/v1/bookmarks/{id}",
				"DELETE /api/v1/bookmarks/{id}",
				"GET /api/v1/collections",
				"POST /api/v1/collections",
				"GET /api/v1/collections/{id}",
				"PATCH /api/v1/collections/{id}",
				"DELETE /api/v1/collections/{id}",
				"GET /api/v1/audiobooks/{id}/sessions",
				"GET /api/v1/listening-sessions",
			},
			"podcasts": {
				"GET /api/v1/podcasts",
				"GET /api/v1/podcasts/shows/{id}",
				"GET /api/v1/podcasts/shows/{id}/episodes",
				"GET /api/v1/podcasts/episodes",
				"GET /api/v1/podcasts/episodes/{id}",
				"GET /api/v1/podcasts/search?q=",
				"GET /api/v1/podcasts/feeds",
				"POST /api/v1/podcasts/feeds",
				"GET /api/v1/podcasts/feeds/{id}",
				"PATCH /api/v1/podcasts/feeds/{id}",
				"POST /api/v1/podcasts/feeds/poll",
				"POST /api/v1/podcasts/feeds/{id}/refresh",
				"DELETE /api/v1/podcasts/feeds/{id}",
			},
			"radio": {
				"GET /api/v1/radio/stations",
				"GET /api/v1/radio/stations/{id}",
				"GET /api/v1/radio/stations/{id}/now",
				"GET /api/v1/radio/stations/{id}/schedule",
			},
			"internetRadio": {
				"GET /api/v1/internet-radio/stations",
				"POST /api/v1/internet-radio/stations",
				"GET /api/v1/internet-radio/stations/{id}",
				"DELETE /api/v1/internet-radio/stations/{id}",
				"GET /internet-radio/{id}/playlist.m3u",
				"GET /internet-radio/{id}/stream",
			},
		},
	})
}

func readPage(r *http.Request) (catalog.PageRequest, error) {
	page := catalog.PageRequest{Limit: 50}

	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return catalog.PageRequest{}, errors.New("limit must be a number")
		}
		page.Limit = limit
	}

	if rawOffset := strings.TrimSpace(r.URL.Query().Get("offset")); rawOffset != "" {
		offset, err := strconv.Atoi(rawOffset)
		if err != nil {
			return catalog.PageRequest{}, errors.New("offset must be a number")
		}
		page.Offset = offset
	}

	if rawSince := strings.TrimSpace(r.URL.Query().Get("updatedSince")); rawSince != "" {
		since, err := parseUpdatedSince(rawSince)
		if err != nil {
			return catalog.PageRequest{}, err
		}
		page.UpdatedSince = since
	}

	return page, nil
}

// readLimitParam reads an optional `?limit=` for the small fixed-size artist
// rails (top tracks / appears-on). Unlike readPage it never errors — a bad or
// missing value falls back to fallback, and the result is clamped to [1, max].
func readLimitParam(r *http.Request, fallback, max int) int {
	limit := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > max {
		limit = max
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// parseUpdatedSince accepts either unix milliseconds (what a JS client gets
// from Date) or an RFC3339 timestamp (what SyncManifest.serverTime marshals
// to). Both resolve to a UTC instant comparable against catalog UpdatedAt.
func parseUpdatedSince(raw string) (time.Time, error) {
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.UnixMilli(ms).UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, errors.New("updatedSince must be RFC3339 or unix milliseconds")
}

func writeCatalogError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "catalog item not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
