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
	Text                  string
	Genre                 string
	Year                  int
	LibraryID             string
	PlaylistUserID        string
	FilterPlaylistsByUser bool
	Favorite              *bool
	Starred               *bool
	RecentlyPlayed        bool
	RecentlyAdded         bool
	Completed             *bool
	MinRating             int
	Sort                  string
	Page                  catalog.PageRequest
}

// AudiobookQuery is the parsed search query for /api/v1/audiobooks/search.
type AudiobookQuery struct {
	Text           string
	Genre          string
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

// PodcastQuery is the parsed search query for /api/v1/podcasts/search.
type PodcastQuery struct {
	Text           string
	Genre          string
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

// commonLongformFilter is shared by audiobook + podcast filters. It exists
// only so the playback/state predicates can be written once.
type commonLongformFilter struct {
	Text           string
	Genre          string
	Favorite       *bool
	Starred        *bool
	RecentlyPlayed bool
	RecentlyAdded  bool
	Completed      *bool
	MinRating      int
}

func (q AudiobookQuery) toCommon() commonLongformFilter {
	return commonLongformFilter{
		Text:           q.Text,
		Genre:          q.Genre,
		Favorite:       q.Favorite,
		Starred:        q.Starred,
		RecentlyPlayed: q.RecentlyPlayed,
		RecentlyAdded:  q.RecentlyAdded,
		Completed:      q.Completed,
		MinRating:      q.MinRating,
	}
}

func (q PodcastQuery) toCommon() commonLongformFilter {
	return commonLongformFilter{
		Text:           q.Text,
		Genre:          q.Genre,
		Favorite:       q.Favorite,
		Starred:        q.Starred,
		RecentlyPlayed: q.RecentlyPlayed,
		RecentlyAdded:  q.RecentlyAdded,
		Completed:      q.Completed,
		MinRating:      q.MinRating,
	}
}

// PlaybackOverlay carries authenticated per-user playback state that is
// layered over the read-only catalog projection. Each domain gets its own
// map keyed by that domain's primary id.
type PlaybackOverlay struct {
	Tracks     map[string]catalog.PlaybackState
	Albums     map[string]catalog.PlaybackState
	Artists    map[string]catalog.PlaybackState
	Playlists  map[string]catalog.PlaybackState
	Audiobooks map[string]catalog.PlaybackState
	Podcasts   map[string]catalog.PlaybackState
	Episodes   map[string]catalog.PlaybackState
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

func ParseAudiobookQueryFromRequest(r *http.Request, page catalog.PageRequest) AudiobookQuery {
	query := AudiobookQuery{Page: page, Sort: SortRelevance}
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
	return query
}

func ParsePodcastQueryFromRequest(r *http.Request, page catalog.PageRequest) PodcastQuery {
	query := PodcastQuery{Page: page, Sort: SortRelevance}
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
