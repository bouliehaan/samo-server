// Package channels implements Samo-native 24/7 programmed radio
// channels. A channel pulls from a mix of source kinds (file pools,
// podcast subscriptions, live streams) and a scheduler decides what
// plays next based on time and rules. The streamer transcodes whatever
// source is chosen through ffmpeg into a unified output format so
// multiple input codecs and live streams can mux into one continuous
// listener-facing stream.
//
// This package owns:
//   - channel + source + schedule rule data model and SQLite store
//   - the scheduler that returns the next playable item for a channel
//   - the per-channel ffmpeg streamer + listener fan-out
//
// It does NOT own:
//   - podcast feed ingestion (that's internal/sources / catalog)
//   - audio file metadata (that's catalog)
//   - HTTP handlers (those live in internal/api)
package channels

import "time"

// Source kinds. New kinds can be added by extending the resolver in
// scheduler.go — the store stays kind-agnostic so unknown kinds round-
// trip cleanly until a resolver is registered.
const (
	SourceFilePool            = "file-pool"
	SourcePodcastSubscription = "podcast-subscription"
	SourceLiveStream          = "live-stream"
	SourceInternetStation     = "internet-station"
	SourceScheduledShow       = "scheduled-show"
)

// Channel is the user-facing programmed radio channel.
type Channel struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Codec        string    `json:"codec"`
	BitrateKbps  int       `json:"bitrateKbps"`
	SampleRateHz int       `json:"sampleRateHz"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`

	// Hydrated companions for the detail view. Empty on list endpoints
	// where the caller paginates separately.
	Sources       []Source       `json:"sources,omitempty"`
	ScheduleRules []ScheduleRule `json:"scheduleRules,omitempty"`
}

// Source is one thing the channel can play from. The shape of
// `Config` depends on `Kind` — see ParseSourceConfig.
type Source struct {
	ID              string         `json:"id"`
	ChannelID       string         `json:"channelId"`
	Kind            string         `json:"kind"`
	Label           string         `json:"label,omitempty"`
	Config          map[string]any `json:"config"`
	Enabled         bool           `json:"enabled"`
	Weight          int            `json:"weight"`
	DefaultRotation bool           `json:"defaultRotation"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

// ScheduleRule pins a source to a time window. When the current time
// falls inside the window AND the rule is enabled, the scheduler picks
// from the rule's source until the window ends.
//
// WeekdayMask is a 7-bit field: Sun=1, Mon=2, Tue=4, Wed=8, Thu=16,
// Fri=32, Sat=64. StartMinute/EndMinute are minute-of-day (0-1439).
// Cross-midnight windows are modelled by two rows (one per side) so
// the matcher can stay simple.
type ScheduleRule struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channelId"`
	SourceID    string    `json:"sourceId"`
	Label       string    `json:"label,omitempty"`
	WeekdayMask int       `json:"weekdayMask"`
	StartMinute int       `json:"startMinute"`
	EndMinute   int       `json:"endMinute"`
	Priority    int       `json:"priority"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

// PlaybackItem is what the scheduler hands the streamer. The streamer
// feeds URL (a local file path or http URL) into ffmpeg and labels the
// resulting stream segment with Title / Artist / SourceLabel for the
// now-playing endpoint.
//
// DurationSeconds is best-effort: 0 means "unknown" (a live stream
// with no clear end). MaxDuration caps how long the streamer should
// stay on this item — used so a live cut-in releases back to rotation
// when its scheduled window ends.
type PlaybackItem struct {
	URL             string        `json:"url"`
	Title           string        `json:"title"`
	Artist          string        `json:"artist,omitempty"`
	Kind            string        `json:"kind"`
	SourceID        string        `json:"sourceId,omitempty"`
	SourceLabel     string        `json:"sourceLabel,omitempty"`
	ItemRef         string        `json:"itemRef,omitempty"`
	DurationSeconds int           `json:"durationSeconds"`
	MaxDuration     time.Duration `json:"-"`
	Live            bool          `json:"live,omitempty"`
	// IsRuleDriven means this item was picked because a schedule
	// rule's window was active when NextItem was called. The streamer
	// skips its preemption watchdog for rule-driven items so they
	// don't preempt themselves on every tick.
	IsRuleDriven bool `json:"-"`
	// RuleID, if set, names the schedule rule that produced this item.
	// The preemption watchdog uses it to detect "the scheduler would
	// now pick a different rule" vs "the rule changed sources."
	RuleID string `json:"-"`
}

// NowPlaying summarises what the channel is currently emitting plus
// the most recent finished items, for the now-playing API/UI.
type NowPlaying struct {
	ChannelID     string         `json:"channelId"`
	Current       *PlaybackItem  `json:"current,omitempty"`
	StartedAt     *time.Time     `json:"startedAt,omitempty"`
	ListenerCount int            `json:"listenerCount"`
	Recent        []PlayLogEntry `json:"recent,omitempty"`
}

// PlayLogEntry is one item that's already played, returned in the
// `Recent` slice of NowPlaying.
type PlayLogEntry struct {
	ID              string    `json:"id"`
	ChannelID       string    `json:"channelId"`
	SourceID        string    `json:"sourceId,omitempty"`
	ItemRef         string    `json:"itemRef,omitempty"`
	Title           string    `json:"title"`
	Artist          string    `json:"artist,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	StartedAt       time.Time `json:"startedAt"`
	EndedAt         time.Time `json:"endedAt,omitempty"`
	DurationSeconds int       `json:"durationSeconds"`
}

// Inputs used by the store/service for mutation. Keeping them as
// separate types from the read models means we can add fields without
// breaking JSON round-trips on existing rows.

type CreateChannelInput struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Codec        string `json:"codec,omitempty"`
	BitrateKbps  int    `json:"bitrateKbps,omitempty"`
	SampleRateHz int    `json:"sampleRateHz,omitempty"`
}

type UpdateChannelInput struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Codec        *string `json:"codec,omitempty"`
	BitrateKbps  *int    `json:"bitrateKbps,omitempty"`
	SampleRateHz *int    `json:"sampleRateHz,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

type CreateSourceInput struct {
	Kind            string         `json:"kind"`
	Label           string         `json:"label,omitempty"`
	Config          map[string]any `json:"config,omitempty"`
	Weight          int            `json:"weight,omitempty"`
	DefaultRotation *bool          `json:"defaultRotation,omitempty"`
	Enabled         *bool          `json:"enabled,omitempty"`
}

type UpdateSourceInput struct {
	Label           *string         `json:"label,omitempty"`
	Config          *map[string]any `json:"config,omitempty"`
	Weight          *int            `json:"weight,omitempty"`
	DefaultRotation *bool           `json:"defaultRotation,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
}

type CreateScheduleRuleInput struct {
	SourceID    string `json:"sourceId"`
	Label       string `json:"label,omitempty"`
	WeekdayMask int    `json:"weekdayMask,omitempty"`
	StartMinute int    `json:"startMinute"`
	EndMinute   int    `json:"endMinute"`
	Priority    int    `json:"priority,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}
