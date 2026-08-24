// Package scrobble is the listen engine shared by every scrobbling target.
//
// Deciding whether a play has been listened to is hard, subtle, and identical
// no matter who the listen is reported to: Last.fm, ListenBrainz, and every
// compatible server all publish the same rule — half the track or four
// minutes, whichever comes first. Only the delivery differs.
//
// So the measurement lives here, once, and each target package owns nothing
// but its own credentials, wire format, and queue. A fix to the listen rules
// lands for every target at the same moment, which is the whole reason this
// package exists rather than a second copy of engine.go.
package scrobble

import (
	"errors"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

var (
	// ErrMissingMetadata is returned for a track with no artist or title,
	// which no scrobbling service will accept.
	ErrMissingMetadata = errors.New("track is missing artist or title metadata required for scrobbling")
	// ErrInvalidEvent is returned when a client sends an unrecognised event.
	ErrInvalidEvent = errors.New("invalid scrobble event")
)

// SourceStream marks an observation produced by opening the audio stream.
const SourceStream = "stream"

// ScrobbleEvent is an explicit client assertion about a play, as opposed to
// the position reports the server infers everything else from.
type ScrobbleEvent string

const (
	EventStart    ScrobbleEvent = "start"
	EventProgress ScrobbleEvent = "progress"
	EventComplete ScrobbleEvent = "complete"
	EventSkip     ScrobbleEvent = "skip"
)

// TrackSubmission snapshots the metadata a scrobbling service needs about one
// play. It is deliberately service-neutral: each target maps these fields onto
// its own wire format.
type TrackSubmission struct {
	TrackID              string
	Artist               string
	Track                string
	Album                string
	AlbumArtist          string
	TrackNumber          int
	DurationSeconds      int
	PlayedSeconds        int
	Timestamp            time.Time
	MusicBrainzRecording string
	MusicBrainzRelease   string
	MusicBrainzArtist    string
	MusicBrainzTrack     string
	// DedupeKey identifies the listen this submission represents. It is the
	// idempotency key the ledger is claimed with, so the same play can never be
	// scrobbled twice regardless of which code path rediscovers it.
	DedupeKey string
}

// PlaybackInput is one observation of one track, as it arrives from a client.
type PlaybackInput struct {
	UserID        string
	Track         catalog.MusicTrack
	Before        catalog.PlaybackState
	After         catalog.PlaybackState
	Patch         *playback.PatchInput
	Source        string
	ResumeSeconds int
	Event         ScrobbleEvent
	// ObservedAt is when the server received this report. Stamped by the HTTP
	// handler, not by the worker that processes it, so notifications that
	// overtake one another in flight can still be ordered.
	ObservedAt time.Time
	// DurationSeconds overrides the catalog duration when a client knows better.
	DurationSeconds int
	// StartedAt lets an explicit client event declare when the play began.
	StartedAt *time.Time
}

// EventInput is the body of an explicit client scrobble event.
type EventInput struct {
	TrackID         string     `json:"trackId"`
	Event           string     `json:"event"`
	ProgressSeconds int        `json:"progressSeconds,omitempty"`
	DurationSeconds int        `json:"durationSeconds,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
}
