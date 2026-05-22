package catalog

import (
	"time"

	"github.com/jakedebus/samo-server/internal/media"
)

type PageRequest struct {
	Limit  int
	Offset int
}

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type Overview struct {
	Music MusicOverview `json:"music"`
	Shelf ShelfOverview `json:"shelf"`
}

type MusicOverview struct {
	ArtistCount     int `json:"artistCount"`
	AlbumCount      int `json:"albumCount"`
	TrackCount      int `json:"trackCount"`
	PlaylistCount   int `json:"playlistCount"`
	GenreCount      int `json:"genreCount"`
	DurationSeconds int `json:"durationSeconds"`
}

type ShelfOverview struct {
	LibraryCount    int `json:"libraryCount"`
	ItemCount       int `json:"itemCount"`
	AudiobookCount  int `json:"audiobookCount"`
	PodcastCount    int `json:"podcastCount"`
	EpisodeCount    int `json:"episodeCount"`
	AuthorCount     int `json:"authorCount"`
	SeriesCount     int `json:"seriesCount"`
	DurationSeconds int `json:"durationSeconds"`
}

type Image struct {
	ID            string `json:"id,omitempty"`
	URL           string `json:"url,omitempty"`
	Path          string `json:"path,omitempty"`
	MimeType      string `json:"mimeType,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	DominantColor string `json:"dominantColor,omitempty"`
	Blurhash      string `json:"blurhash,omitempty"`
}

type ExternalIDs struct {
	MusicBrainzArtistID       string   `json:"musicBrainzArtistId,omitempty"`
	MusicBrainzReleaseGroupID string   `json:"musicBrainzReleaseGroupId,omitempty"`
	MusicBrainzReleaseID      string   `json:"musicBrainzReleaseId,omitempty"`
	MusicBrainzRecordingID    string   `json:"musicBrainzRecordingId,omitempty"`
	MusicBrainzTrackID        string   `json:"musicBrainzTrackId,omitempty"`
	MusicBrainzWorkID         string   `json:"musicBrainzWorkId,omitempty"`
	DiscogsID                 string   `json:"discogsId,omitempty"`
	SpotifyID                 string   `json:"spotifyId,omitempty"`
	AppleMusicID              string   `json:"appleMusicId,omitempty"`
	ISRC                      string   `json:"isrc,omitempty"`
	ISBN10                    string   `json:"isbn10,omitempty"`
	ISBN13                    string   `json:"isbn13,omitempty"`
	ASIN                      string   `json:"asin,omitempty"`
	AudibleASIN               string   `json:"audibleAsin,omitempty"`
	GoogleBooksID             string   `json:"googleBooksId,omitempty"`
	OpenLibraryID             string   `json:"openLibraryId,omitempty"`
	ITunesID                  string   `json:"itunesId,omitempty"`
	FeedGUID                  string   `json:"feedGuid,omitempty"`
	URLs                      []string `json:"urls,omitempty"`
}

type AudioFile struct {
	ID              string     `json:"id"`
	Path            string     `json:"path"`
	RelativePath    string     `json:"relativePath,omitempty"`
	FileName        string     `json:"fileName,omitempty"`
	Container       string     `json:"container,omitempty"`
	MimeType        string     `json:"mimeType,omitempty"`
	Codec           string     `json:"codec,omitempty"`
	CodecProfile    string     `json:"codecProfile,omitempty"`
	MetadataFormats []string   `json:"metadataFormats,omitempty"`
	Bitrate         int        `json:"bitrate,omitempty"`
	BitDepth        int        `json:"bitDepth,omitempty"`
	SampleRate      int        `json:"sampleRate,omitempty"`
	Channels        int        `json:"channels,omitempty"`
	ChannelLayout   string     `json:"channelLayout,omitempty"`
	DurationSeconds int        `json:"durationSeconds"`
	SizeBytes       int64      `json:"sizeBytes,omitempty"`
	ModifiedAt      *time.Time `json:"modifiedAt,omitempty"`
	Checksum        string     `json:"checksum,omitempty"`
	EmbeddedTags    Tags       `json:"embeddedTags,omitempty"`
}

type Tags map[string][]string

type AudioChapter struct {
	ID           string `json:"id,omitempty"`
	Index        int    `json:"index"`
	Title        string `json:"title"`
	StartSeconds int    `json:"startSeconds"`
	EndSeconds   int    `json:"endSeconds,omitempty"`
}

type PlaybackState struct {
	UserID          string     `json:"userId,omitempty"`
	PlayCount       int        `json:"playCount"`
	SkipCount       int        `json:"skipCount,omitempty"`
	Rating          int        `json:"rating,omitempty"`
	Starred         bool       `json:"starred"`
	Favorite        bool       `json:"favorite"`
	ProgressSeconds int        `json:"progressSeconds,omitempty"`
	Completed       bool       `json:"completed"`
	LastPlayedAt    *time.Time `json:"lastPlayedAt,omitempty"`
	LastPositionAt  *time.Time `json:"lastPositionAt,omitempty"`
}

type Contributor struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	SortName string `json:"sortName,omitempty"`
	Role     string `json:"role,omitempty"`
}

type SeriesRef struct {
	ID           string  `json:"id,omitempty"`
	Name         string  `json:"name"`
	Sequence     float64 `json:"sequence,omitempty"`
	SequenceText string  `json:"sequenceText,omitempty"`
}

type GenreSummary struct {
	Name       string     `json:"name"`
	Kind       media.Kind `json:"kind,omitempty"`
	ItemCount  int        `json:"itemCount"`
	TrackCount int        `json:"trackCount,omitempty"`
	AlbumCount int        `json:"albumCount,omitempty"`
}
