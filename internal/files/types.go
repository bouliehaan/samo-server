package files

import "time"

// MediaFile is the API view of a row in `media_files`. A row can belong
// to an audiobook OR a podcast OR a music track OR a podcast episode —
// exactly one of AudiobookID, PodcastID, TrackID, EpisodeID is non-empty.
type MediaFile struct {
	ID              string     `json:"id"`
	LibraryID       string     `json:"libraryId"`
	AudiobookID     string     `json:"audiobookId,omitempty"`
	PodcastID       string     `json:"podcastId,omitempty"`
	TrackID         string     `json:"trackId,omitempty"`
	EpisodeID       string     `json:"episodeId,omitempty"`
	Path            string     `json:"path"`
	RelativePath    string     `json:"relativePath,omitempty"`
	FileName        string     `json:"fileName,omitempty"`
	MimeType        string     `json:"mimeType,omitempty"`
	Container       string     `json:"container,omitempty"`
	SizeBytes       int64      `json:"sizeBytes,omitempty"`
	DurationSeconds int        `json:"durationSeconds,omitempty"`
	ModifiedAt      *time.Time `json:"modifiedAt,omitempty"`
}
