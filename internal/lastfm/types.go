package lastfm

import (
	"errors"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
)

var (
	ErrDisabled        = errors.New("last.fm integration is not configured")
	ErrNotConnected    = errors.New("last.fm account is not connected")
	ErrInvalidToken    = errors.New("last.fm auth token is invalid or expired")
	ErrSessionExpired  = errors.New("last.fm session is invalid or expired")
	ErrMissingMetadata = errors.New("track is missing artist or title metadata required for scrobbling")
	ErrInvalidEvent    = errors.New("invalid scrobble event")
	ErrInvalidConfig   = errors.New("last.fm api key and shared secret are required")
)

type ScrobbleEvent string

const (
	EventStart    ScrobbleEvent = "start"
	EventProgress ScrobbleEvent = "progress"
	EventComplete ScrobbleEvent = "complete"
	EventSkip     ScrobbleEvent = "skip"
)

const (
	submissionStatusSubmitted = "submitted"
	submissionStatusQueued    = "queued"
	submissionStatusFailed    = "failed"
	submissionStatusDropped   = "dropped"
)

type Status struct {
	Enabled     bool       `json:"enabled"`
	Connected   bool       `json:"connected"`
	Username    string     `json:"username,omitempty"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	QueueSize   int        `json:"queueSize"`
}

type AppConfig struct {
	Enabled         bool       `json:"enabled"`
	APIKey          string     `json:"apiKey,omitempty"`
	HasSharedSecret bool       `json:"hasSharedSecret"`
	Source          string     `json:"source,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
}

type AppConfigInput struct {
	APIKey       string `json:"apiKey"`
	SharedSecret string `json:"sharedSecret"`
}

type AuthBeginResponse struct {
	AuthURL string `json:"authUrl"`
	Token   string `json:"token"`
}

type AuthCompleteInput struct {
	Token string `json:"token"`
}

type AuthCompleteResponse struct {
	Username    string    `json:"username"`
	Connected   bool      `json:"connected"`
	ConnectedAt time.Time `json:"connectedAt"`
}

type ScrobbleEventInput struct {
	TrackID         string     `json:"trackId"`
	Event           string     `json:"event"`
	ProgressSeconds int        `json:"progressSeconds,omitempty"`
	DurationSeconds int        `json:"durationSeconds,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
}

type ScrobbleEventResponse struct {
	TrackID         string `json:"trackId"`
	Event           string `json:"event"`
	NowPlaying      bool   `json:"nowPlaying,omitempty"`
	Scrobbled       bool   `json:"scrobbled,omitempty"`
	Queued          bool   `json:"queued,omitempty"`
	ProgressSeconds int    `json:"progressSeconds,omitempty"`
}

type QueueItem struct {
	ID              int64     `json:"id"`
	Kind            string    `json:"kind"`
	TrackID         string    `json:"trackId,omitempty"`
	Artist          string    `json:"artist"`
	Track           string    `json:"track"`
	Album           string    `json:"album,omitempty"`
	DurationSeconds int       `json:"durationSeconds,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	Attempts        int       `json:"attempts"`
	LastError       string    `json:"lastError,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
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

type TrackSubmission struct {
	TrackID              string
	Artist               string
	Track                string
	Album                string
	DurationSeconds      int
	PlayedSeconds        int
	Timestamp            time.Time
	MusicBrainzRecording string
}

type PlaybackInput struct {
	UserID        string
	Track         catalog.MusicTrack
	Before        catalog.PlaybackState
	After         catalog.PlaybackState
	Patch         *playback.PatchInput
	Source        string
	ResumeSeconds int
	Event         ScrobbleEvent
}

type playbackResult struct {
	NowPlaying bool
	Scrobbled  bool
	Queued     bool
}
