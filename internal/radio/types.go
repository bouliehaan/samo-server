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
