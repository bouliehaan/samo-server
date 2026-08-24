package lastfm

import (
	"errors"
	"time"

	"github.com/bouliehaan/samo-server/internal/scrobble"
)

var (
	ErrDisabled         = errors.New("last.fm integration is not configured")
	ErrNotConnected     = errors.New("last.fm account is not connected")
	ErrInvalidToken     = errors.New("last.fm auth token is invalid or expired")
	ErrSessionExpired   = errors.New("last.fm session is invalid or expired")
	ErrInvalidConfig    = errors.New("last.fm api key and shared secret are required")
	ErrInvalidSignature = errors.New("last.fm api signature rejected")
)

// The listen engine is shared with every other scrobbling target; these
// aliases keep the Last.fm package (and its clients) spelling the types the
// way they always have.
type (
	ScrobbleEvent      = scrobble.ScrobbleEvent
	TrackSubmission    = scrobble.TrackSubmission
	PlaybackInput      = scrobble.PlaybackInput
	ScrobbleEventInput = scrobble.EventInput

	play              = scrobble.Play
	observation       = scrobble.Observation
	playUpdate        = scrobble.PlayUpdate
	nowPlayingPointer = scrobble.NowPlayingPointer
)

const (
	EventStart    = scrobble.EventStart
	EventProgress = scrobble.EventProgress
	EventComplete = scrobble.EventComplete
	EventSkip     = scrobble.EventSkip

	sourceStream = scrobble.SourceStream
)

var (
	ErrMissingMetadata = scrobble.ErrMissingMetadata
	ErrInvalidEvent    = scrobble.ErrInvalidEvent
)

// Last.fm refuses scrobbles older than two weeks. maxScrobbleAge is the point
// past which delivery is abandoned; scrobbleClampAge is where an older
// timestamp is pinned, deliberately inside it so a clamped listen does not land
// exactly on the drop boundary and get discarded on its way out.
const (
	maxScrobbleAge   = 13 * 24 * time.Hour
	scrobbleClampAge = maxScrobbleAge - 12*time.Hour
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

type ScrobbleEventResponse struct {
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
