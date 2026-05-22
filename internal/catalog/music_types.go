package catalog

import "time"

type MusicArtist struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	SortName        string        `json:"sortName,omitempty"`
	Disambiguation  string        `json:"disambiguation,omitempty"`
	Biography       string        `json:"biography,omitempty"`
	Country         string        `json:"country,omitempty"`
	Genres          []string      `json:"genres,omitempty"`
	Styles          []string      `json:"styles,omitempty"`
	Moods           []string      `json:"moods,omitempty"`
	Links           []string      `json:"links,omitempty"`
	Images          []Image       `json:"images,omitempty"`
	ExternalIDs     ExternalIDs   `json:"externalIds,omitempty"`
	AlbumCount      int           `json:"albumCount"`
	TrackCount      int           `json:"trackCount"`
	DurationSeconds int           `json:"durationSeconds"`
	Playback        PlaybackState `json:"playback"`
	AddedAt         *time.Time    `json:"addedAt,omitempty"`
	UpdatedAt       *time.Time    `json:"updatedAt,omitempty"`
}

type MusicAlbum struct {
	ID                  string        `json:"id"`
	Title               string        `json:"title"`
	SortTitle           string        `json:"sortTitle,omitempty"`
	Version             string        `json:"version,omitempty"`
	DisplayArtist       string        `json:"displayArtist,omitempty"`
	AlbumArtistIDs      []string      `json:"albumArtistIds,omitempty"`
	AlbumArtistNames    []string      `json:"albumArtistNames,omitempty"`
	ArtistIDs           []string      `json:"artistIds,omitempty"`
	ArtistNames         []string      `json:"artistNames,omitempty"`
	ReleaseDate         string        `json:"releaseDate,omitempty"`
	OriginalReleaseDate string        `json:"originalReleaseDate,omitempty"`
	ReleaseYear         int           `json:"releaseYear,omitempty"`
	ReleaseType         string        `json:"releaseType,omitempty"`
	ReleaseStatus       string        `json:"releaseStatus,omitempty"`
	Compilation         bool          `json:"compilation,omitempty"`
	RecordLabel         string        `json:"recordLabel,omitempty"`
	CatalogNumber       string        `json:"catalogNumber,omitempty"`
	Barcode             string        `json:"barcode,omitempty"`
	Genres              []string      `json:"genres,omitempty"`
	Styles              []string      `json:"styles,omitempty"`
	Moods               []string      `json:"moods,omitempty"`
	Tags                []string      `json:"tags,omitempty"`
	DiscCount           int           `json:"discCount,omitempty"`
	TrackCount          int           `json:"trackCount"`
	DurationSeconds     int           `json:"durationSeconds"`
	Images              []Image       `json:"images,omitempty"`
	ExternalIDs         ExternalIDs   `json:"externalIds,omitempty"`
	Playback            PlaybackState `json:"playback"`
	AddedAt             *time.Time    `json:"addedAt,omitempty"`
	UpdatedAt           *time.Time    `json:"updatedAt,omitempty"`
}

type MusicTrack struct {
	ID               string        `json:"id"`
	Title            string        `json:"title"`
	SortTitle        string        `json:"sortTitle,omitempty"`
	Subtitle         string        `json:"subtitle,omitempty"`
	DisplayArtist    string        `json:"displayArtist,omitempty"`
	ArtistIDs        []string      `json:"artistIds,omitempty"`
	ArtistNames      []string      `json:"artistNames,omitempty"`
	AlbumID          string        `json:"albumId,omitempty"`
	AlbumTitle       string        `json:"albumTitle,omitempty"`
	AlbumArtistIDs   []string      `json:"albumArtistIds,omitempty"`
	AlbumArtistNames []string      `json:"albumArtistNames,omitempty"`
	DiscNumber       int           `json:"discNumber,omitempty"`
	TrackNumber      int           `json:"trackNumber,omitempty"`
	TotalDiscs       int           `json:"totalDiscs,omitempty"`
	TotalTracks      int           `json:"totalTracks,omitempty"`
	ReleaseDate      string        `json:"releaseDate,omitempty"`
	ReleaseYear      int           `json:"releaseYear,omitempty"`
	Genres           []string      `json:"genres,omitempty"`
	Moods            []string      `json:"moods,omitempty"`
	Tags             []string      `json:"tags,omitempty"`
	DurationSeconds  int           `json:"durationSeconds"`
	Explicit         bool          `json:"explicit,omitempty"`
	BPM              int           `json:"bpm,omitempty"`
	Key              string        `json:"key,omitempty"`
	Comment          string        `json:"comment,omitempty"`
	Lyrics           []Lyric       `json:"lyrics,omitempty"`
	AudioFiles       []AudioFile   `json:"audioFiles,omitempty"`
	Images           []Image       `json:"images,omitempty"`
	ExternalIDs      ExternalIDs   `json:"externalIds,omitempty"`
	Playback         PlaybackState `json:"playback"`
	AddedAt          *time.Time    `json:"addedAt,omitempty"`
	UpdatedAt        *time.Time    `json:"updatedAt,omitempty"`
}

type Lyric struct {
	Language string `json:"language,omitempty"`
	Text     string `json:"text"`
	Synced   bool   `json:"synced"`
}

type MusicPlaylist struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Description     string        `json:"description,omitempty"`
	OwnerID         string        `json:"ownerId,omitempty"`
	Public          bool          `json:"public"`
	Collaborative   bool          `json:"collaborative,omitempty"`
	TrackIDs        []string      `json:"trackIds,omitempty"`
	TrackCount      int           `json:"trackCount"`
	DurationSeconds int           `json:"durationSeconds"`
	Images          []Image       `json:"images,omitempty"`
	Playback        PlaybackState `json:"playback"`
	CreatedAt       *time.Time    `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time    `json:"updatedAt,omitempty"`
}

type MusicSearchResults struct {
	Artists   []MusicArtist   `json:"artists"`
	Albums    []MusicAlbum    `json:"albums"`
	Tracks    []MusicTrack    `json:"tracks"`
	Playlists []MusicPlaylist `json:"playlists"`
	Total     int             `json:"total"`
	Limit     int             `json:"limit"`
	Offset    int             `json:"offset"`
}
