package files

import "time"

type MediaFile struct {
	ID              string     `json:"id"`
	LibraryID       string     `json:"libraryId"`
	ItemID          string     `json:"itemId,omitempty"`
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
