package sources

import "time"

const (
	DefaultPollIntervalSeconds = 3600
	MinPollIntervalSeconds     = 900
	MaxPollIntervalSeconds     = 604800

	PollStatusOK       = "ok"
	PollStatusError    = "error"
	PollStatusDisabled = "disabled"
)

type UpdatePodcastFeedInput struct {
	Title               *string `json:"title,omitempty"`
	PollEnabled         *bool   `json:"pollEnabled,omitempty"`
	PollIntervalSeconds *int    `json:"pollIntervalSeconds,omitempty"`
}

type PollCycleResult struct {
	Checked int              `json:"checked"`
	Updated int              `json:"updated"`
	Failed  int              `json:"failed"`
	Skipped int              `json:"skipped"`
	Results []PollFeedResult `json:"results"`
}

type PollFeedResult struct {
	FeedID  string `json:"feedId"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

type PollSchedule struct {
	Enabled            bool       `json:"pollEnabled"`
	IntervalSeconds    int        `json:"pollIntervalSeconds"`
	NextPollAt         *time.Time `json:"nextPollAt,omitempty"`
	LastPollStartedAt  *time.Time `json:"lastPollStartedAt,omitempty"`
	LastPollFinishedAt *time.Time `json:"lastPollFinishedAt,omitempty"`
	ConsecutiveErrors  int        `json:"consecutiveErrors"`
}

const (
	DefaultProbeIntervalSeconds = 600
	MinProbeIntervalSeconds     = 60
	MaxProbeIntervalSeconds     = 86400

	ProbeStatusOK    = "ok"
	ProbeStatusError = "error"
)

type ProbeSchedule struct {
	Enabled             bool       `json:"probeEnabled"`
	IntervalSeconds     int        `json:"probeIntervalSeconds"`
	NextProbeAt         *time.Time `json:"nextProbeAt,omitempty"`
	LastProbeStartedAt  *time.Time `json:"lastProbeStartedAt,omitempty"`
	LastProbeFinishedAt *time.Time `json:"lastProbeFinishedAt,omitempty"`
	LastError           string     `json:"lastError,omitempty"`
	Status              string     `json:"status,omitempty"`
	ConsecutiveErrors   int        `json:"consecutiveErrors"`
}

type ProbeCycleResult struct {
	Checked int                  `json:"checked"`
	Updated int                  `json:"updated"`
	Failed  int                  `json:"failed"`
	Skipped int                  `json:"skipped"`
	Results []ProbeStationResult `json:"results"`
}

type ProbeStationResult struct {
	StationID  string `json:"stationId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	NowPlaying string `json:"nowPlaying,omitempty"`
	Error      string `json:"error,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
}
