package radio

import (
	"time"

	"github.com/bouliehaan/samo-server/internal/media"
)

type Config struct {
	Stations []StationConfig `json:"stations"`
}

type StationConfig struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Epoch       string            `json:"epoch,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Media       []MediaItemConfig `json:"media"`
}

type MediaItemConfig struct {
	ID              string     `json:"id,omitempty"`
	Title           string     `json:"title"`
	Artist          string     `json:"artist,omitempty"`
	Album           string     `json:"album,omitempty"`
	Kind            media.Kind `json:"kind,omitempty"`
	Path            string     `json:"path"`
	DurationSeconds int        `json:"durationSeconds"`
	Weight          int        `json:"weight,omitempty"`
}

type StationSummary struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	ContentType          string `json:"contentType"`
	MediaCount           int    `json:"mediaCount"`
	RotationCount        int    `json:"rotationCount"`
	TotalDurationSeconds int    `json:"totalDurationSeconds"`
	Source               string `json:"source,omitempty"`
	Enabled              bool   `json:"enabled"`
}

// Item source kinds. Station items may reference catalog entities or be
// raw filesystem paths. Future kinds (playlists, smart queries) can be added
// without breaking existing rows because resolution is done at hydration
// time.
const (
	ItemSourcePath = "path"
	// Item source kinds for radio_station_items.source_kind. These match
	// the playback target kinds in internal/playback so a single string
	// vocabulary is used across the codebase.
	ItemSourceMusicTrack     = "music-track"
	ItemSourceAudiobook      = "audiobook"
	ItemSourcePodcastEpisode = "podcast-episode"

	StationSourceDatabase = "database"
	StationSourceFile     = "file"
)

// StationItem is the API-facing view of a row in radio_station_items. The
// resolved path is only populated when hydration found a local file for the
// catalog reference.
type StationItem struct {
	ID              string `json:"id"`
	StationID       string `json:"stationId"`
	Position        int    `json:"position"`
	SourceKind      string `json:"sourceKind"`
	SourceID        string `json:"sourceId,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	ResolvedPath    string `json:"resolvedPath,omitempty"`
	Title           string `json:"title"`
	Artist          string `json:"artist,omitempty"`
	Album           string `json:"album,omitempty"`
	Kind            string `json:"kind"`
	DurationSeconds int    `json:"durationSeconds"`
	Weight          int    `json:"weight"`
	Missing         bool   `json:"missing,omitempty"`
}

type ProgramSlot struct {
	StationID       string     `json:"stationId"`
	MediaID         string     `json:"mediaId"`
	Title           string     `json:"title"`
	Artist          string     `json:"artist,omitempty"`
	Album           string     `json:"album,omitempty"`
	Kind            media.Kind `json:"kind"`
	StartsAt        time.Time  `json:"startsAt"`
	EndsAt          time.Time  `json:"endsAt"`
	DurationSeconds int        `json:"durationSeconds"`
	OffsetSeconds   int        `json:"offsetSeconds"`
}

type station struct {
	summary StationSummary
	epoch   time.Time
	media   []mediaItem
	loop    []mediaItem
}

type mediaItem struct {
	id              string
	title           string
	artist          string
	album           string
	kind            media.Kind
	path            string
	durationSeconds int
	weight          int
}
