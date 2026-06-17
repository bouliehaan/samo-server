package catalog

import "time"

// AudiobookItem is one audiobook on disk (or RSS-imported).
//
// This replaces the old ShelfItem-with-MediaType=book. It deliberately
// shares NO model with PodcastItem — audiobooks and podcasts are
// independent product domains in Samo, even though they happen to share
// underlying audio-file storage. If an audiobook field would be useful for
// a podcast or vice versa, copy it across rather than coupling the types.
type AudiobookItem struct {
	ID              string         `json:"id"`
	LibraryID       string         `json:"libraryId,omitempty"`
	Path            string         `json:"path,omitempty"`
	FolderID        string         `json:"folderId,omitempty"`
	Inode           string         `json:"inode,omitempty"`
	SizeBytes       int64          `json:"sizeBytes,omitempty"`
	Missing         bool           `json:"missing"`
	Invalid         bool           `json:"invalid"`
	Cover           *Image         `json:"cover,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Genres          []string       `json:"genres,omitempty"`
	DurationSeconds int            `json:"durationSeconds"`
	Progress        PlaybackState  `json:"progress"`
	Book            *BookMetadata  `json:"book,omitempty"`
	AudioFiles      []AudioFile    `json:"audioFiles,omitempty"`
	Chapters        []AudioChapter `json:"chapters,omitempty"`
	// Chapter provenance, so clients can show how a book was chaptered and flag the
	// uncertain ones for review instead of trusting every marker equally.
	// ChapterSource: embedded | cue | audnexus | audio-aligned | file | none.
	// ChapterConfidence: 0..1 from the audio registration (0 for embedded/file).
	ChapterSource     string     `json:"chapterSource,omitempty"`
	ChapterConfidence float64    `json:"chapterConfidence,omitempty"`
	ChapterASIN       string     `json:"chapterAsin,omitempty"`
	AddedAt           *time.Time `json:"addedAt,omitempty"`
	UpdatedAt         *time.Time `json:"updatedAt,omitempty"`
	LastScanAt        *time.Time `json:"lastScanAt,omitempty"`
}

// BookMetadata is the book-specific metadata embedded in an AudiobookItem.
// Authors and narrators are stored separately so consumers don't have to
// filter by role string. Series is inline; the full series entity lives in
// the `series` table.
type BookMetadata struct {
	Title           string           `json:"title"`
	Subtitle        string           `json:"subtitle,omitempty"`
	SortTitle       string           `json:"sortTitle,omitempty"`
	Authors         []ContributorRef `json:"authors,omitempty"`
	Narrators       []ContributorRef `json:"narrators,omitempty"`
	Series          []SeriesRef      `json:"series,omitempty"`
	Publisher       string           `json:"publisher,omitempty"`
	PublishedDate   string           `json:"publishedDate,omitempty"`
	PublishedYear   string           `json:"publishedYear,omitempty"`
	Description     string           `json:"description,omitempty"`
	Language        string           `json:"language,omitempty"`
	Genres          []string         `json:"genres,omitempty"`
	Tags            []string         `json:"tags,omitempty"`
	ISBNs           []string         `json:"isbns,omitempty"`
	Explicit        bool             `json:"explicit,omitempty"`
	Abridged        bool             `json:"abridged,omitempty"`
	DurationSeconds int              `json:"durationSeconds"`
	ExternalIDs     ExternalIDs      `json:"externalIds,omitempty"`
}

// Contributor is a person who contributed to an audiobook — author,
// narrator, translator, etc. Backed by the `contributors` table. The role
// is NOT on this entity because the same person can have multiple roles
// across different audiobooks (e.g. author of one book, narrator of
// another). The role lives on the `audiobook_contributors` junction row,
// and surfaces as `ContributorRef.Role` when embedded inside a book.
//
// Podcasts deliberately do NOT use this — they have an inline
// `PodcastMetadata.Author` string. If we ever need first-class podcast
// host modelling we'll add a separate `podcast_hosts` table rather than
// muddying contributors.
type Contributor struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	SortName        string      `json:"sortName,omitempty"`
	Description     string      `json:"description,omitempty"`
	Images          []Image     `json:"images,omitempty"`
	ExternalIDs     ExternalIDs `json:"externalIds,omitempty"`
	AudiobookCount  int         `json:"audiobookCount"`
	SeriesCount     int         `json:"seriesCount"`
	DurationSeconds int         `json:"durationSeconds"`
}

// Series is a book series (Stormlight, Wheel of Time, etc.). Audiobook-only
// for now. Backed by the `series` table.
type Series struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Description     string           `json:"description,omitempty"`
	Authors         []ContributorRef `json:"authors,omitempty"`
	AudiobookIDs    []string         `json:"audiobookIds,omitempty"`
	AudiobookCount  int              `json:"audiobookCount"`
	DurationSeconds int              `json:"durationSeconds"`
	ExternalIDs     ExternalIDs      `json:"externalIds,omitempty"`
}

// ContributorDetail bundles a contributor with the audiobooks they
// contributed to. Returned by GET /api/v1/contributors/{id}.
type ContributorDetail struct {
	Contributor
	Audiobooks Page[AudiobookItem] `json:"audiobooks"`
}

// SeriesDetail bundles a series with its audiobooks. Returned by
// GET /api/v1/series/{id}.
type SeriesDetail struct {
	Series
	Audiobooks Page[AudiobookItem] `json:"audiobooks"`
}

// AudiobookSearchResults is the response shape for
// GET /api/v1/audiobooks/search. Note there is no `episodes` field — that
// belongs on PodcastSearchResults.
type AudiobookSearchResults struct {
	Audiobooks   []AudiobookItem `json:"audiobooks"`
	Contributors []Contributor   `json:"contributors"`
	Series       []Series        `json:"series"`
	Total        int             `json:"total"`
	Limit        int             `json:"limit"`
	Offset       int             `json:"offset"`
}
