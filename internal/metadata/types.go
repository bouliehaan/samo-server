package metadata

import (
	"context"

	"github.com/jakedebus/samo-server/internal/catalog"
)

type Kind string

const (
	KindAudiobook Kind = "audiobook"
	KindPodcast   Kind = "podcast"
	KindMusic     Kind = "music"
)

type MusicSearchType string

const (
	MusicSearchArtist MusicSearchType = "artist"
	MusicSearchAlbum  MusicSearchType = "album"
	MusicSearchTrack  MusicSearchType = "track"
)

type SearchRequest struct {
	Kind      Kind            `json:"kind"`
	Query     string          `json:"query,omitempty"`
	Provider  string          `json:"provider,omitempty"`
	Limit     int             `json:"limit,omitempty"`
	Title     string          `json:"title,omitempty"`
	Author    string          `json:"author,omitempty"`
	ISBN      string          `json:"isbn,omitempty"`
	Artist    string          `json:"artist,omitempty"`
	Album     string          `json:"album,omitempty"`
	Track     string          `json:"track,omitempty"`
	MusicType MusicSearchType `json:"musicType,omitempty"`
}

type SearchResponse struct {
	Query     SearchRequest    `json:"query"`
	Providers []ProviderStatus `json:"providers"`
	Results   []SearchResult   `json:"results"`
}

type ProviderStatus struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Kinds     []Kind   `json:"kinds"`
	Notes     []string `json:"notes,omitempty"`
	NeedsAuth bool     `json:"needsAuth,omitempty"`
}

type SearchResult struct {
	ID              string                `json:"id"`
	Provider        string                `json:"provider"`
	MediaType       string                `json:"mediaType"`
	Score           int                   `json:"score,omitempty"`
	Title           string                `json:"title"`
	Subtitle        string                `json:"subtitle,omitempty"`
	SortTitle       string                `json:"sortTitle,omitempty"`
	Description     string                `json:"description,omitempty"`
	Authors         []catalog.Contributor `json:"authors,omitempty"`
	Narrators       []catalog.Contributor `json:"narrators,omitempty"`
	Series          []catalog.SeriesRef   `json:"series,omitempty"`
	Publisher       string                `json:"publisher,omitempty"`
	PublishedDate   string                `json:"publishedDate,omitempty"`
	PublishedYear   string                `json:"publishedYear,omitempty"`
	Language        string                `json:"language,omitempty"`
	Genres          []string              `json:"genres,omitempty"`
	Tags            []string              `json:"tags,omitempty"`
	DurationSeconds int                   `json:"durationSeconds,omitempty"`
	Explicit        bool                  `json:"explicit,omitempty"`
	Cover           *catalog.Image        `json:"cover,omitempty"`
	ExternalIDs     catalog.ExternalIDs   `json:"externalIds,omitempty"`
	Links           []Link                `json:"links,omitempty"`
	Raw             map[string]any        `json:"raw,omitempty"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Provider interface {
	Name() string
	Supports(kind Kind) bool
	Status() ProviderStatus
	Search(ctx context.Context, request SearchRequest) ([]SearchResult, error)
}
