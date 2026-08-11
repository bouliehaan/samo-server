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

import (
	"strings"
	"time"
)

// Source kinds. New kinds can be added by extending the resolver in
// scheduler.go — the store stays kind-agnostic so unknown kinds round-
// trip cleanly until a resolver is registered.
const (
	SourceFilePool            = "file-pool"
	SourceMusicPlaylist       = "music-playlist"
	SourcePodcastSubscription = "podcast-subscription"
	SourceLiveStream          = "live-stream"
	SourceInternetStation     = "internet-station"
	SourceScheduledShow       = "scheduled-show"
)

// Source roles. A role says what a piece of content IS; the scheduler derives
// the running order from that rather than asking anyone to rank things.
//
// The ordering is deliberately not configurable. "A scheduled show outranks a
// new episode outranks filler" is what a radio station is, not a preference,
// and exposing it as a number is how a music playlist ends up interrupting the
// news.
const (
	// RoleShow only ever plays when a schedule rule calls for it.
	RoleShow = "show"
	// RoleTalk is spoken word in the rotation: podcasts, talk stations. A
	// podcast source serves its freshest unheard episode first and falls back
	// to reruns — that behaviour follows the KIND, not this role.
	RoleTalk = "talk"
	// RoleMusic is music in the rotation. It is a separate role rather than a
	// flavour of filler because the talk/music balance is the one split a
	// listener notices, and nothing about a source's kind reveals it: a folder
	// could be commercials or oldies, a stream could be BBC or lofi.
	RoleMusic = "music"

	// RolePodcast and RoleFiller are the previous names, still accepted so a
	// stored row or an in-flight client keeps working.
	RolePodcast = "podcast"
	RoleFiller  = "filler"
	// RoleCommercial is padding between items, never something the channel
	// chooses to "play".
	RoleCommercial = "commercial"
)

// NormalizeRole maps a stored or submitted role onto a known one, deriving a
// sensible default from the source kind when it is blank. Rows written before
// roles existed are backfilled by migration 0013; this covers everything else.
func NormalizeRole(role, kind string, defaultRotation bool) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleShow:
		return RoleShow
	case RoleCommercial:
		return RoleCommercial
	case RoleTalk, RolePodcast:
		return RoleTalk
	case RoleMusic, RoleFiller:
		return RoleMusic
	}
	if !defaultRotation {
		return RoleShow
	}
	return DefaultRoleForKind(kind)
}

// DefaultRoleForKind is the role a kind of content almost always wants. Only a
// guess for the ambiguous kinds — a folder or a stream could be either — which
// is exactly why the role is editable.
func DefaultRoleForKind(kind string) string {
	switch kind {
	case SourceMusicPlaylist:
		return RoleMusic
	case SourcePodcastSubscription:
		return RoleTalk
	default:
		return RoleTalk
	}
}

// Channel is the user-facing programmed radio channel.
type Channel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Codec        string `json:"codec"`
	BitrateKbps  int    `json:"bitrateKbps"`
	SampleRateHz int    `json:"sampleRateHz"`
	Enabled      bool   `json:"enabled"`
	// Timezone is the wall clock the schedule is written in, as an IANA name.
	// Empty means the server default. Schedule rules store a bare
	// minute-of-day, so without this "16:00" has no meaning.
	Timezone string `json:"timezone,omitempty"`
	// EffectiveTimezone is the zone actually in force — the channel's own, or
	// the server default resolved for it. Read-only, filled in on the way out,
	// so a client never has to guess what "16:00" means.
	EffectiveTimezone string `json:"effectiveTimezone,omitempty"`
	// TalkShare is the fraction of airtime spoken word should get, 0..1.
	// Zero means the server default. It is the one dial on the rotation,
	// because it is the only part that is taste rather than mechanism.
	TalkShare float64 `json:"talkShare,omitempty"`
	// DayStartMinute and DayEndMinute are the listening day: the window in
	// which airing something means somebody could have heard it. Minute-of-day
	// in the channel's own timezone. Everything the station believes about
	// "new" depends on these, because a podcast that drops at 04:00 and airs at
	// 04:20 has not reached anyone.
	DayStartMinute int       `json:"dayStartMinute"`
	DayEndMinute   int       `json:"dayEndMinute"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`

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
	// Role decides which rung of the ladder this source serves.
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
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
	URL             string `json:"url"`
	Title           string `json:"title"`
	Artist          string `json:"artist,omitempty"`
	Kind            string `json:"kind"`
	SourceID        string `json:"sourceId,omitempty"`
	SourceLabel     string `json:"sourceLabel,omitempty"`
	ItemRef         string `json:"itemRef,omitempty"`
	DurationSeconds int    `json:"durationSeconds"`
	// Category is the station's own name for what kind of programming this is,
	// carried from the source that produced it so the play log records what
	// KIND of listening aired. The balance is a question about categories and
	// it cannot be asked of a log that only knows source ids.
	Category    CategoryID    `json:"category,omitempty"`
	MaxDuration time.Duration `json:"-"`
	Live        bool          `json:"live,omitempty"`
	// BlockID names the programming block this item was chosen for, and is
	// recorded so "what was the station doing at the time" survives the
	// decision.
	BlockID string `json:"blockId,omitempty"`
	// Exposure is how much airing this counts toward satisfying an obligation
	// for it, 0..1, taken from the block that was on air when it started.
	//
	// Carried on the item rather than looked up when it ends, because by the
	// time a three-hour episode finishes the station is somewhere else, and the
	// credit belongs to where it began.
	Exposure float64 `json:"-"`
	// IsRuleDriven means a hard anchor was on air when this was picked. The
	// streamer skips its preemption watchdog for anchored items so they don't
	// preempt themselves on every tick.
	IsRuleDriven bool `json:"-"`
	// AnchorBlockID, if set, names the anchored block that produced this item.
	// The preemption watchdog uses it to tell "a different appointment is due
	// now" from "the same one is still on".
	AnchorBlockID string `json:"-"`
	// AnchorPolicy is how the anchor that is coming wants to take over. Only
	// startImmediately permits cutting into whatever is playing; makeNext means
	// the appointment waits for the item to finish, which is nearly always
	// right once candidates are already filtered to what fits before it.
	AnchorPolicy StartPolicy `json:"-"`
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
	Name           *string  `json:"name,omitempty"`
	Timezone       *string  `json:"timezone,omitempty"`
	TalkShare      *float64 `json:"talkShare,omitempty"`
	DayStartMinute *int     `json:"dayStartMinute,omitempty"`
	DayEndMinute   *int     `json:"dayEndMinute,omitempty"`
	Description    *string  `json:"description,omitempty"`
	Codec          *string  `json:"codec,omitempty"`
	BitrateKbps    *int     `json:"bitrateKbps,omitempty"`
	SampleRateHz   *int     `json:"sampleRateHz,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
}

type CreateSourceInput struct {
	Kind            string         `json:"kind"`
	Label           string         `json:"label,omitempty"`
	Config          map[string]any `json:"config,omitempty"`
	Weight          int            `json:"weight,omitempty"`
	Role            string         `json:"role,omitempty"`
	DefaultRotation *bool          `json:"defaultRotation,omitempty"`
	Enabled         *bool          `json:"enabled,omitempty"`
}

type UpdateSourceInput struct {
	Label           *string         `json:"label,omitempty"`
	Config          *map[string]any `json:"config,omitempty"`
	Weight          *int            `json:"weight,omitempty"`
	Role            *string         `json:"role,omitempty"`
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
