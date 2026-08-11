// Package samoradio is Samo's control plane for samo-radio devices —
// headless players wired into a physical audio output, usually this machine's
// own line-out.
//
// The split of responsibilities is deliberate. This package owns devices,
// credentials and the HTTP conversation with them; it knows nothing about
// Samo's catalog. Turning "play this album" into playable items is the API
// layer's job, because that is where the catalog already lives and there is no
// reason for a second mapping from ids to stream URLs to exist.
package samoradio

import "time"

// Device is one registered player.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// BaseURL is where Samo reaches the device's control API.
	BaseURL string `json:"baseUrl"`
	// StreamBaseURL is where the device reaches Samo to pull audio.
	StreamBaseURL string `json:"streamBaseUrl,omitempty"`
	Enabled       bool   `json:"enabled"`
	// Paired reports whether a Samo token has been issued to this device.
	Paired     bool      `json:"paired"`
	LastSeenAt time.Time `json:"lastSeenAt,omitempty"`
	LastError  string    `json:"lastError,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`

	// State is the device's own live status, filled in when Samo could reach
	// it. A device that is off or unreachable still lists — with State nil and
	// LastError explaining why — rather than vanishing from the UI.
	State *State `json:"state,omitempty"`

	// controlToken and token bookkeeping never leave the server.
	controlToken string
	tokenID      string
	tokenUserID  string
}

// Item is one playable thing to send to a device. Mirrors the daemon's own
// item shape: everything is already resolved, including the absolute URL.
type Item struct {
	Ref             string  `json:"ref"`
	Title           string  `json:"title"`
	Subtitle        string  `json:"subtitle,omitempty"`
	ArtworkURL      string  `json:"artworkUrl,omitempty"`
	StreamURL       string  `json:"streamUrl"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	Kind            string  `json:"kind,omitempty"`
	Live            bool    `json:"live,omitempty"`

	// GainDB levels this item against the rest of the queue: a constant
	// decibel offset the device applies to every sample. Computed here rather
	// than on the device because the measurement it comes from needs the
	// library and a cache, and the device has neither. Zero plays it as-is.
	GainDB float64 `json:"gainDb,omitempty"`

	// LimitPeaks asks the device for a true-peak limiter behind the gain, for
	// the rare item whose peaks would otherwise overshoot once lifted.
	LimitPeaks bool `json:"limitPeaks,omitempty"`

	// CeilingDBTP is that limiter's threshold.
	CeilingDBTP float64 `json:"ceilingDbtp,omitempty"`
}

// ChannelState is what a tuned station is airing. The JSON key stays `channel`
// for both kinds; Kind says which one it is.
type ChannelState struct {
	ID            string `json:"id"`
	Kind          string `json:"kind,omitempty"`
	Name          string `json:"name,omitempty"`
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	SourceLabel   string `json:"sourceLabel,omitempty"`
	ListenerCount int    `json:"listenerCount,omitempty"`
}

// StationRef names a channel or an internet radio station. Both are things the
// device can sit on indefinitely, which is all "station" means here.
type StationRef struct {
	// Kind is "channel" or "station".
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Station kinds, mirrored from the daemon.
const (
	StationChannel  = "channel"
	StationInternet = "station"
)

// OutputState is the device's sound-card status.
type OutputState struct {
	Backend    string `json:"backend"`
	Device     string `json:"device,omitempty"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Open       bool   `json:"open"`
	Restarts   int64  `json:"restarts,omitempty"`
	LastError  string `json:"lastError,omitempty"`
}

// ServerState is the device's view of Samo.
type ServerState struct {
	BaseURL string `json:"baseUrl,omitempty"`
	Name    string `json:"name,omitempty"`
	Paired  bool   `json:"paired"`
}

// State is a device's status snapshot, passed through from the daemon.
type State struct {
	DeviceName      string        `json:"deviceName"`
	Mode            string        `json:"mode"`
	Status          string        `json:"status"`
	Volume          float64       `json:"volume"`
	PositionSeconds float64       `json:"positionSeconds"`
	DurationSeconds float64       `json:"durationSeconds,omitempty"`
	Item            *Item         `json:"item,omitempty"`
	Queue           []Item        `json:"queue,omitempty"`
	QueueIndex      int           `json:"queueIndex"`
	Channel         *ChannelState `json:"channel,omitempty"`

	DefaultStation *StationRef `json:"defaultStation,omitempty"`

	Output OutputState `json:"output"`
	Server ServerState `json:"server"`

	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	Version   uint64    `json:"version"`
}

// AudioDevice is one selectable output on a device's machine.
type AudioDevice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Detail      string `json:"detail,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// Outputs is the device's audio output list.
type Outputs struct {
	Backend  string        `json:"backend"`
	Devices  []AudioDevice `json:"devices"`
	Selected string        `json:"selected,omitempty"`
	Backends []string      `json:"backends,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// CreateDeviceInput registers a device.
type CreateDeviceInput struct {
	Name          string `json:"name"`
	BaseURL       string `json:"baseUrl"`
	ControlToken  string `json:"controlToken,omitempty"`
	StreamBaseURL string `json:"streamBaseUrl,omitempty"`
}

// UpdateDeviceInput edits a device. Nil fields are left alone.
type UpdateDeviceInput struct {
	Name          *string `json:"name,omitempty"`
	BaseURL       *string `json:"baseUrl,omitempty"`
	ControlToken  *string `json:"controlToken,omitempty"`
	StreamBaseURL *string `json:"streamBaseUrl,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

// SettingsInput changes device-side settings through Samo.
type SettingsInput struct {
	DeviceName     *string     `json:"deviceName,omitempty"`
	DefaultStation *StationRef `json:"defaultStation,omitempty"`
	TuneNow        bool        `json:"tuneNow,omitempty"`
	AutoTuneOnBoot *bool       `json:"autoTuneOnBoot,omitempty"`
	Output         *struct {
		Backend      string   `json:"backend,omitempty"`
		Device       *string  `json:"device,omitempty"`
		SampleRate   int      `json:"sampleRate,omitempty"`
		Channels     int      `json:"channels,omitempty"`
		BufferMillis int      `json:"bufferMillis,omitempty"`
		Command      []string `json:"command,omitempty"`
	} `json:"output,omitempty"`
}
