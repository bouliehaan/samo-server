package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jakedebus/samo-server/internal/catalog"
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
			{Name: "metadata", PathPrefix: "/api/v1/metadata", Description: "Explicit external metadata lookup providers and search."},
			{Name: "music", PathPrefix: "/api/v1/music", Description: "Music artists, albums, tracks, playlists, genres, and search."},
			{Name: "shelf", PathPrefix: "/api/v1/shelf", Description: "Audiobook and podcast libraries, items, authors, series, episodes, and search."},
			{Name: "radio", PathPrefix: "/api/v1/radio", Description: "24/7 station metadata, now playing, and schedules."},
			{Name: "internetRadio", PathPrefix: "/api/v1/internet-radio", Description: "User-managed external internet radio streams."},
		},
		MetadataSets: []metadataSetManifest{
			{Name: "musicArtist", Fields: []string{"ids", "sort names", "biography", "country", "genres", "styles", "moods", "images", "external IDs", "counts", "playback state"}},
			{Name: "musicAlbum", Fields: []string{"album artists", "release dates", "release type/status", "label", "catalog number", "barcode", "genres", "images", "external IDs", "playback state"}},
			{Name: "musicTrack", Fields: []string{"artists", "album", "disc/track numbers", "audio technical metadata", "lyrics", "genres", "external IDs", "playback state"}},
			{Name: "shelfItem", Fields: []string{"library info", "media type", "filesystem identity", "cover", "tags", "genres", "progress", "audio files", "chapters", "book or podcast metadata"}},
			{Name: "bookMetadata", Fields: []string{"title", "authors", "narrators", "series", "publisher", "published date", "description", "language", "ISBNs", "external IDs", "duration"}},
			{Name: "podcastMetadata", Fields: []string{"feed URL", "site URL", "owner", "language", "categories", "explicit flag", "episode count", "external IDs"}},
			{Name: "metadataCandidate", Fields: []string{"provider", "media type", "score", "title", "contributors", "descriptions", "dates", "genres", "covers", "external IDs", "links"}},
		},
		Routes: map[string][]string{
			"metadata": {
				"GET /api/v1/metadata/providers",
				"GET /api/v1/metadata/search",
			},
			"music": {
				"GET /api/v1/music/artists",
				"GET /api/v1/music/artists/{id}",
				"GET /api/v1/music/albums",
				"GET /api/v1/music/albums/{id}",
				"GET /api/v1/music/tracks",
				"GET /api/v1/music/tracks/{id}",
				"GET /api/v1/music/genres",
				"GET /api/v1/music/playlists",
				"GET /api/v1/music/playlists/{id}",
				"GET /api/v1/music/search?q=",
			},
			"shelf": {
				"GET /api/v1/shelf/libraries",
				"GET /api/v1/shelf/libraries/{id}",
				"GET /api/v1/shelf/items",
				"GET /api/v1/shelf/items/{id}",
				"GET /api/v1/shelf/audiobooks",
				"GET /api/v1/shelf/authors",
				"GET /api/v1/shelf/authors/{id}",
				"GET /api/v1/shelf/series",
				"GET /api/v1/shelf/series/{id}",
				"GET /api/v1/shelf/podcasts",
				"GET /api/v1/shelf/podcast-feeds",
				"POST /api/v1/shelf/podcast-feeds",
				"GET /api/v1/shelf/podcast-feeds/{id}",
				"POST /api/v1/shelf/podcast-feeds/{id}/refresh",
				"DELETE /api/v1/shelf/podcast-feeds/{id}",
				"GET /api/v1/shelf/episodes",
				"GET /api/v1/shelf/episodes/{id}",
				"GET /api/v1/shelf/search?q=",
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

	return page, nil
}

func writeCatalogError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrNotFound) {
		writeError(w, http.StatusNotFound, "catalog item not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
