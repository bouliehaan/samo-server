// Package listenbrainz delivers measured listens to ListenBrainz and any
// API-compatible server.
//
// The decision of WHETHER a play has been listened to is not made here — that
// lives in internal/scrobble, shared with Last.fm, because both services
// publish the same rule and a second copy of that logic would drift. This
// package owns only what is specific to ListenBrainz: a per-user token, the
// submit-listens wire format, and a durable queue that keeps trying.
//
// Two things differ materially from Last.fm and shape the code below:
//
//   - There is no application key or OAuth handshake. A user pastes a token
//     from their settings page and that is the entire connection, so the
//     service is always available and "connected" is purely per-user.
//   - ListenBrainz accepts historical listens and takes up to 1000 of them per
//     request, so a backlog is delivered in batches and nothing is ever
//     discarded for being too old.
package listenbrainz

import (
	"errors"
	"time"

	"github.com/bouliehaan/samo-server/internal/scrobble"
)

var (
	ErrDisabled     = errors.New("listenbrainz integration is unavailable")
	ErrNotConnected = errors.New("listenbrainz account is not connected")
	ErrInvalidToken = errors.New("listenbrainz user token is invalid or expired")
	ErrMissingToken = errors.New("listenbrainz user token is required")
	ErrInvalidRoot  = errors.New("listenbrainz api root must be an absolute http or https url")
)

// The listen engine is shared; these aliases let this package speak in the
// same vocabulary as the rest of the server.
type (
	TrackSubmission = scrobble.TrackSubmission
	PlaybackInput   = scrobble.PlaybackInput
	EventInput      = scrobble.EventInput

	play              = scrobble.Play
	nowPlayingPointer = scrobble.NowPlayingPointer
)

var (
	ErrMissingMetadata = scrobble.ErrMissingMetadata
	ErrInvalidEvent    = scrobble.ErrInvalidEvent
)

// DefaultAPIRoot is the hosted ListenBrainz instance.
const DefaultAPIRoot = "https://api.listenbrainz.org"

const (
	queueKindListen     = "listen"
	queueKindPlayingNow = "playing_now"
)

const (
	submissionStatusSubmitted = "submitted"
	submissionStatusQueued    = "queued"
	submissionStatusFailed    = "failed"
	submissionStatusDropped   = "dropped"
)

// Status is what the UI shows about one user's connection.
type Status struct {
	Enabled     bool       `json:"enabled"`
	Connected   bool       `json:"connected"`
	Username    string     `json:"username,omitempty"`
	APIRoot     string     `json:"apiRoot,omitempty"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	QueueSize   int        `json:"queueSize"`
}

// ConnectInput carries the token a user pasted from their ListenBrainz
// settings page, and optionally the instance it belongs to.
type ConnectInput struct {
	Token string `json:"token"`
	// APIRoot points at a self-hosted or otherwise compatible server. Blank
	// means the server default.
	APIRoot string `json:"apiRoot,omitempty"`
}

type ConnectResponse struct {
	Username    string    `json:"username"`
	APIRoot     string    `json:"apiRoot"`
	Connected   bool      `json:"connected"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type EventResponse struct {
	TrackID         string `json:"trackId"`
	Event           string `json:"event"`
	NowPlaying      bool   `json:"nowPlaying,omitempty"`
	Scrobbled       bool   `json:"scrobbled,omitempty"`
	Queued          bool   `json:"queued,omitempty"`
	ProgressSeconds int    `json:"progressSeconds,omitempty"`
}

type QueueItem struct {
	ID              int64      `json:"id"`
	Kind            string     `json:"kind"`
	TrackID         string     `json:"trackId,omitempty"`
	Artist          string     `json:"artist"`
	Track           string     `json:"track"`
	Album           string     `json:"album,omitempty"`
	DurationSeconds int        `json:"durationSeconds,omitempty"`
	Timestamp       time.Time  `json:"timestamp"`
	Attempts        int        `json:"attempts"`
	LastError       string     `json:"lastError,omitempty"`
	NextAttemptAt   *time.Time `json:"nextAttemptAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type QueuePage struct {
	Items []QueueItem `json:"items"`
	Total int         `json:"total"`
}

type SubmissionRecord struct {
	ID              int64     `json:"id"`
	Kind            string    `json:"kind"`
	TrackID         string    `json:"trackId,omitempty"`
	Artist          string    `json:"artist"`
	Track           string    `json:"track"`
	Album           string    `json:"album,omitempty"`
	DurationSeconds int       `json:"durationSeconds,omitempty"`
	PlayedSeconds   int       `json:"playedSeconds,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
	Source          string    `json:"source,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

type HistoryPage struct {
	Items []SubmissionRecord `json:"items"`
	Total int                `json:"total"`
}

type playbackResult struct {
	NowPlaying bool
	Scrobbled  bool
	Queued     bool
}
