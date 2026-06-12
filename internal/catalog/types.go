package catalog

import (
	"time"

	"github.com/bouliehaan/samo-server/internal/media"
)

type PageRequest struct {
	Limit  int
	Offset int
	// UpdatedSince, when non-zero, restricts list results to entities whose
	// UpdatedAt is at or after this instant. It powers incremental ("delta")
	// client syncs: a client replays its last sync watermark and receives only
	// rows that changed since. Entities with a nil UpdatedAt are always
	// included so a row predating updated_at tracking is never hidden from a
	// delta consumer.
	UpdatedSince time.Time
}

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// Overview is the home-page summary. Each of music/audiobook/podcast/radio
// is an independent first-class domain — there is no umbrella "shelf"
// concept. Radio counts live in the radio service, not here.
type Overview struct {
	Music     MusicOverview     `json:"music"`
	Audiobook AudiobookOverview `json:"audiobook"`
	Podcast   PodcastOverview   `json:"podcast"`
}

type MusicOverview struct {
	ArtistCount     int `json:"artistCount"`
	AlbumCount      int `json:"albumCount"`
	TrackCount      int `json:"trackCount"`
	PlaylistCount   int `json:"playlistCount"`
	GenreCount      int `json:"genreCount"`
	DurationSeconds int `json:"durationSeconds"`
}

type AudiobookOverview struct {
	LibraryCount     int `json:"libraryCount"`
	AudiobookCount   int `json:"audiobookCount"`
	ContributorCount int `json:"contributorCount"`
	SeriesCount      int `json:"seriesCount"`
	DurationSeconds  int `json:"durationSeconds"`
}

type PodcastOverview struct {
	LibraryCount    int `json:"libraryCount"`
	PodcastCount    int `json:"podcastCount"`
	EpisodeCount    int `json:"episodeCount"`
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
	ID              string   `json:"id"`
	Path            string   `json:"path"`
	RelativePath    string   `json:"relativePath,omitempty"`
	FileName        string   `json:"fileName,omitempty"`
	Container       string   `json:"container,omitempty"`
	MimeType        string   `json:"mimeType,omitempty"`
	Codec           string   `json:"codec,omitempty"`
	CodecProfile    string   `json:"codecProfile,omitempty"`
	MetadataFormats []string `json:"metadataFormats,omitempty"`
	Bitrate         int      `json:"bitrate,omitempty"`
	BitDepth        int      `json:"bitDepth,omitempty"`
	SampleRate      int      `json:"sampleRate,omitempty"`
	Channels        int      `json:"channels,omitempty"`
	ChannelLayout   string   `json:"channelLayout,omitempty"`
	DurationSeconds int      `json:"durationSeconds"`
	// DurationMs is the exact file duration in milliseconds. ffprobe reports
	// fractional seconds; we keep them so multi-file audiobooks can compute
	// book-global offsets without the integer rounding that used to make late
	// chapters drift several seconds. Falls back to DurationSeconds*1000 for
	// rows scanned before this field existed.
	DurationMs int64 `json:"durationMs,omitempty"`
	// StartOffsetSeconds is this file's start position on the book-global
	// timeline (sum of every earlier file's exact duration). Always 0 for the
	// first file, for music tracks, and for podcast episodes. Clients use it as
	// the single source of truth for mapping book-time <-> (file, file-time) so
	// they never re-accumulate per-file durations and drift.
	StartOffsetSeconds float64    `json:"startOffsetSeconds,omitempty"`
	SizeBytes          int64      `json:"sizeBytes,omitempty"`
	ModifiedAt         *time.Time `json:"modifiedAt,omitempty"`
	Checksum           string     `json:"checksum,omitempty"`
	EmbeddedTags       Tags       `json:"embeddedTags,omitempty"`
}

type Tags map[string][]string

// AudioChapter is one navigable chapter on the book-global timeline.
//
// Start/EndSeconds are fractional (float64) because retail audiobooks place
// chapter boundaries at sub-second offsets and multi-file books accumulate
// per-file durations — integer seconds drifted by up to a second per file,
// which is why deep chapters used to land in the wrong place. The canonical
// storage is integer milliseconds (audiobook_chapters.start_ms/end_ms); these
// fields are ms/1000 and the JSON keys are unchanged so existing clients keep
// reading them, now with full precision.
type AudioChapter struct {
	ID           string  `json:"id,omitempty"`
	Index        int     `json:"index"`
	Title        string  `json:"title"`
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds,omitempty"`
}

// StartMs returns the chapter start as exact integer milliseconds.
func (c AudioChapter) StartMs() int64 { return int64(c.StartSeconds*1000 + 0.5) }

// EndMs returns the chapter end as exact integer milliseconds.
func (c AudioChapter) EndMs() int64 { return int64(c.EndSeconds*1000 + 0.5) }

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
	// StateUpdatedAt is the user_playback row's write clock — when THIS
	// user's playback state for the entity last changed. List endpoints fold
	// it into their updatedSince filter so a delta-syncing client re-receives
	// rows whose playback overlay (playCount / lastPlayedAt / favorite)
	// moved, not only rows whose catalog metadata changed. Without it, a
	// client mirror's "most played" data silently freezes at first-sync.
	StateUpdatedAt *time.Time `json:"stateUpdatedAt,omitempty"`
}

// ContributorRef is the inline "tag" form of a contributor — what you embed
// inside BookMetadata.Authors / BookMetadata.Narrators. The full entity
// (with bio, images, counts) is Contributor in audiobook_types.go. We keep
// these as separate types because the inline list has a stable JSON shape
// (id/name/sortName/role) that we don't want to drag the entity's heavier
// fields through every audiobook payload.
type ContributorRef struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	SortName string `json:"sortName,omitempty"`
	Role     string `json:"role,omitempty"`
}

// SeriesRef is the inline form of a series — embedded in BookMetadata.Series
// with just the sequence info. The entity Series (in audiobook_types.go) is
// the table-row form.
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
