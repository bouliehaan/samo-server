package search

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const (
	SortRelevance = "relevance"
	SortTitle     = "title"
	SortAdded     = "added"
	SortPlayed    = "played"
)

type MusicQuery struct {
	Text           string
	Genre          string
	Year           int
	LibraryID      string
	Favorite       *bool
	Starred        *bool
	RecentlyPlayed bool
	RecentlyAdded  bool
	Completed      *bool
	MinRating      int
	Sort           string
	Page           catalog.PageRequest
}

type ShelfQuery struct {
	Text           string
	Genre          string
	MediaType      catalog.ShelfMediaType
	LibraryID      string
	Favorite       *bool
	Starred        *bool
	RecentlyPlayed bool
	RecentlyAdded  bool
	Completed      *bool
	MinRating      int
	Sort           string
	Page           catalog.PageRequest
}

type PlaybackOverlay struct {
	Tracks    map[string]catalog.PlaybackState
	Albums    map[string]catalog.PlaybackState
	Artists   map[string]catalog.PlaybackState
	Playlists map[string]catalog.PlaybackState
	Items     map[string]catalog.PlaybackState
	Episodes  map[string]catalog.PlaybackState
}

func ParseMusicQueryFromRequest(r *http.Request, page catalog.PageRequest) MusicQuery {
	query := MusicQuery{Page: page, Sort: SortRelevance}
	if r == nil {
		return query
	}
	query.Text = strings.TrimSpace(r.URL.Query().Get("q"))
	if query.Text == "" {
		query.Text = strings.TrimSpace(r.URL.Query().Get("query"))
	}
	query.Genre = strings.TrimSpace(r.URL.Query().Get("genre"))
	query.LibraryID = strings.TrimSpace(r.URL.Query().Get("libraryId"))
	query.Year = intQuery(r, "year")
	query.Favorite = boolQuery(r, "favorite")
	query.Starred = boolQuery(r, "starred")
	query.RecentlyPlayed = boolFlag(r, "recentlyPlayed")
	query.RecentlyAdded = boolFlag(r, "recentlyAdded")
	query.Completed = boolQuery(r, "completed")
	query.MinRating = intQuery(r, "minRating")
	query.Sort = normalizeSort(r.URL.Query().Get("sort"), SortRelevance)
	return query
}

func ParseShelfQueryFromRequest(r *http.Request, page catalog.PageRequest) ShelfQuery {
	query := ShelfQuery{Page: page, Sort: SortRelevance}
	if r == nil {
		return query
	}
	query.Text = strings.TrimSpace(r.URL.Query().Get("q"))
	if query.Text == "" {
		query.Text = strings.TrimSpace(r.URL.Query().Get("query"))
	}
	query.Genre = strings.TrimSpace(r.URL.Query().Get("genre"))
	query.LibraryID = strings.TrimSpace(r.URL.Query().Get("libraryId"))
	query.Favorite = boolQuery(r, "favorite")
	query.Starred = boolQuery(r, "starred")
	query.RecentlyPlayed = boolFlag(r, "recentlyPlayed")
	query.RecentlyAdded = boolFlag(r, "recentlyAdded")
	query.Completed = boolQuery(r, "completed")
	query.MinRating = intQuery(r, "minRating")
	query.Sort = normalizeSort(r.URL.Query().Get("sort"), SortRelevance)
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mediaType"))) {
	case string(catalog.ShelfMediaTypeBook):
		query.MediaType = catalog.ShelfMediaTypeBook
	case string(catalog.ShelfMediaTypePodcast):
		query.MediaType = catalog.ShelfMediaTypePodcast
	}
	return query
}

func normalizeSort(raw, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SortRelevance, SortTitle, SortAdded, SortPlayed:
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return fallback
	}
}

func boolFlag(r *http.Request, key string) bool {
	value := boolQuery(r, key)
	return value != nil && *value
}

func boolQuery(r *http.Request, key string) *bool {
	if r == nil {
		return nil
	}
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &parsed
}

func intQuery(r *http.Request, key string) int {
	if r == nil {
		return 0
	}
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return parsed
}
