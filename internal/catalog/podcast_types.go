package catalog

import "time"

// PodcastItem is one podcast show (NOT one episode). One podcast row can
// hold many PodcastEpisode rows.
//
// This replaces the old ShelfItem-with-MediaType=podcast. PodcastItem has
// no contributors / series — podcasts are author-as-string (PodcastMetadata.Author).
type PodcastItem struct {
	ID              string           `json:"id"`
	LibraryID       string           `json:"libraryId,omitempty"`
	Path            string           `json:"path,omitempty"`
	FolderID        string           `json:"folderId,omitempty"`
	Inode           string           `json:"inode,omitempty"`
	SizeBytes       int64            `json:"sizeBytes,omitempty"`
	Missing         bool             `json:"missing"`
	Invalid         bool             `json:"invalid"`
	Cover           *Image           `json:"cover,omitempty"`
	Tags            []string         `json:"tags,omitempty"`
	Genres          []string         `json:"genres,omitempty"`
	DurationSeconds int              `json:"durationSeconds"`
	Progress        PlaybackState    `json:"progress"`
	Podcast         *PodcastMetadata `json:"podcast,omitempty"`
	/** Set when this show has an active row in podcast_feeds (hybrid or RSS-only). */
	RssFeed    *PodcastLinkedFeed `json:"rssFeed,omitempty"`
	AudioFiles []AudioFile        `json:"audioFiles,omitempty"`
	Episodes   []PodcastEpisode   `json:"episodes,omitempty"`
	AddedAt    *time.Time         `json:"addedAt,omitempty"`
	UpdatedAt  *time.Time         `json:"updatedAt,omitempty"`
	LastScanAt *time.Time         `json:"lastScanAt,omitempty"`
}

// PodcastLinkedFeed is the RSS subscription backing a hybrid (files + feed) show.
type PodcastLinkedFeed struct {
	FeedURL string `json:"feedUrl"`
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
}

// PodcastMetadata is the show-level metadata embedded in a PodcastItem.
type PodcastMetadata struct {
	Title        string      `json:"title"`
	Author       string      `json:"author,omitempty"`
	Description  string      `json:"description,omitempty"`
	FeedURL      string      `json:"feedUrl,omitempty"`
	SiteURL      string      `json:"siteUrl,omitempty"`
	Language     string      `json:"language,omitempty"`
	Explicit     bool        `json:"explicit,omitempty"`
	Categories   []string    `json:"categories,omitempty"`
	OwnerName    string      `json:"ownerName,omitempty"`
	OwnerEmail   string      `json:"ownerEmail,omitempty"`
	EpisodeCount int         `json:"episodeCount"`
	ExternalIDs  ExternalIDs `json:"externalIds,omitempty"`
}

// PodcastEpisode is one episode of a podcast show. Backed by the
// `podcast_episodes` table.
type PodcastEpisode struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId,omitempty"`
	PodcastID string `json:"podcastId"`
	/** Show title for list/detail payloads (derived from the parent podcast). */
	PodcastTitle    string         `json:"podcastTitle,omitempty"`
	Title           string         `json:"title"`
	Subtitle        string         `json:"subtitle,omitempty"`
	Description     string         `json:"description,omitempty"`
	PublishedAt     *time.Time     `json:"publishedAt,omitempty"`
	Season          int            `json:"season,omitempty"`
	Episode         int            `json:"episode,omitempty"`
	EpisodeType     string         `json:"episodeType,omitempty"`
	DurationSeconds int            `json:"durationSeconds"`
	Explicit        bool           `json:"explicit,omitempty"`
	EnclosureURL    string         `json:"enclosureUrl,omitempty"`
	EnclosureType   string         `json:"enclosureType,omitempty"`
	EnclosureBytes  int64          `json:"enclosureBytes,omitempty"`
	AudioFiles      []AudioFile    `json:"audioFiles,omitempty"`
	Chapters        []AudioChapter `json:"chapters,omitempty"`
	Progress        PlaybackState  `json:"progress"`
	ExternalIDs     ExternalIDs    `json:"externalIds,omitempty"`
	Cache           *EpisodeCache  `json:"cache,omitempty"`
	AddedAt         *time.Time     `json:"addedAt,omitempty"`
	UpdatedAt       *time.Time     `json:"updatedAt,omitempty"`
}

// EpisodeCache describes whether a remote podcast episode is stored locally.
type EpisodeCache struct {
	Cached       bool       `json:"cached,omitempty"`
	Local        bool       `json:"local,omitempty"`
	SizeBytes    int64      `json:"sizeBytes,omitempty"`
	DownloadedAt *time.Time `json:"downloadedAt,omitempty"`
}

// PodcastSearchResults is the response shape for
// GET /api/v1/podcasts/search.
type PodcastSearchResults struct {
	Podcasts []PodcastItem    `json:"podcasts"`
	Episodes []PodcastEpisode `json:"episodes"`
	Total    int              `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
}
