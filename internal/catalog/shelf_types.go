package catalog

import "time"

type ShelfMediaType string

const (
	ShelfMediaTypeBook    ShelfMediaType = "book"
	ShelfMediaTypePodcast ShelfMediaType = "podcast"
)

type ShelfLibrary struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	MediaType   ShelfMediaType `json:"mediaType"`
	Path        string         `json:"path,omitempty"`
	Description string         `json:"description,omitempty"`
	ItemCount   int            `json:"itemCount"`
	CreatedAt   *time.Time     `json:"createdAt,omitempty"`
	UpdatedAt   *time.Time     `json:"updatedAt,omitempty"`
}

type ShelfItem struct {
	ID              string           `json:"id"`
	LibraryID       string           `json:"libraryId,omitempty"`
	MediaType       ShelfMediaType   `json:"mediaType"`
	MediaKind       string           `json:"mediaKind,omitempty"`
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
	Book            *BookMetadata    `json:"book,omitempty"`
	Podcast         *PodcastMetadata `json:"podcast,omitempty"`
	AudioFiles      []AudioFile      `json:"audioFiles,omitempty"`
	Chapters        []AudioChapter   `json:"chapters,omitempty"`
	AddedAt         *time.Time       `json:"addedAt,omitempty"`
	UpdatedAt       *time.Time       `json:"updatedAt,omitempty"`
	LastScanAt      *time.Time       `json:"lastScanAt,omitempty"`
}

type BookMetadata struct {
	Title           string        `json:"title"`
	Subtitle        string        `json:"subtitle,omitempty"`
	SortTitle       string        `json:"sortTitle,omitempty"`
	Authors         []Contributor `json:"authors,omitempty"`
	Narrators       []Contributor `json:"narrators,omitempty"`
	Series          []SeriesRef   `json:"series,omitempty"`
	Publisher       string        `json:"publisher,omitempty"`
	PublishedDate   string        `json:"publishedDate,omitempty"`
	PublishedYear   string        `json:"publishedYear,omitempty"`
	Description     string        `json:"description,omitempty"`
	Language        string        `json:"language,omitempty"`
	Genres          []string      `json:"genres,omitempty"`
	Tags            []string      `json:"tags,omitempty"`
	ISBNs           []string      `json:"isbns,omitempty"`
	Explicit        bool          `json:"explicit,omitempty"`
	Abridged        bool          `json:"abridged,omitempty"`
	DurationSeconds int           `json:"durationSeconds"`
	ExternalIDs     ExternalIDs   `json:"externalIds,omitempty"`
}

type ShelfAuthor struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	SortName        string      `json:"sortName,omitempty"`
	Description     string      `json:"description,omitempty"`
	Images          []Image     `json:"images,omitempty"`
	ExternalIDs     ExternalIDs `json:"externalIds,omitempty"`
	ItemCount       int         `json:"itemCount"`
	SeriesCount     int         `json:"seriesCount"`
	DurationSeconds int         `json:"durationSeconds"`
}

type ShelfSeries struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Description     string        `json:"description,omitempty"`
	Authors         []Contributor `json:"authors,omitempty"`
	ItemIDs         []string      `json:"itemIds,omitempty"`
	ItemCount       int           `json:"itemCount"`
	DurationSeconds int           `json:"durationSeconds"`
	ExternalIDs     ExternalIDs   `json:"externalIds,omitempty"`
}

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

type PodcastEpisode struct {
	ID              string         `json:"id"`
	LibraryID       string         `json:"libraryId,omitempty"`
	PodcastID       string         `json:"podcastId"`
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
	AddedAt         *time.Time     `json:"addedAt,omitempty"`
	UpdatedAt       *time.Time     `json:"updatedAt,omitempty"`
}

type ShelfSearchResults struct {
	Items    []ShelfItem      `json:"items"`
	Authors  []ShelfAuthor    `json:"authors"`
	Series   []ShelfSeries    `json:"series"`
	Episodes []PodcastEpisode `json:"episodes"`
	Total    int              `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
}
