package channels

import (
	"context"
	"strings"
	"time"
)

// zoneName reports a zone people can act on.
//
// time.Local stringifies as "Local", which tells a reader nothing and hides
// the thing they need to know — that it is almost certainly UTC, because
// servers are.
func zoneName(location *time.Location, at time.Time) string {
	name := location.String()
	if name != "Local" && name != "" {
		return name
	}
	if isUTC(at) {
		return "UTC"
	}
	return at.Format("MST")
}

func isUTC(at time.Time) bool {
	_, offset := at.Zone()
	return offset == 0
}

// ScheduleStatus explains what the scheduler thinks is going on right now.
//
// It exists because "my show is not playing" has several causes that look
// identical from outside — the clock is in the wrong zone, nothing is booked
// today, a slot matched but its content could not play — and guessing between
// them from the listening end is miserable.
type ScheduleStatus struct {
	// Timezone is the clock the schedule is being read in, and Now is the
	// current wall time in it. Almost every "the slot did not fire" report is
	// this being UTC when the operator meant local.
	Timezone string    `json:"timezone"`
	Now      time.Time `json:"now"`
	// UsingFallbackZone means nobody has said what clock this schedule is
	// written in, and it has landed on UTC. On a server — which is usually set
	// to UTC on purpose — that silently shifts every slot by the operator's
	// offset, so the UI can offer to fix it instead of letting them find out by
	// missing a show.
	UsingFallbackZone bool `json:"usingFallbackZone,omitempty"`
	// LocalTime and Weekday are the two values the matcher actually compares,
	// spelled out so a mismatch is visible without doing the arithmetic.
	LocalTime string `json:"localTime"`
	Weekday   string `json:"weekday"`
	Minute    int    `json:"minuteOfDay"`

	// ActiveRule is the booked slot that should be on air, if any.
	ActiveRule   *ScheduleRule `json:"activeRule,omitempty"`
	ActiveSource *Source       `json:"activeSource,omitempty"`
	// RuleError is why an active slot is NOT playing.
	RuleError string `json:"ruleError,omitempty"`

	// OnAir summarises the outcome in one line for the UI.
	OnAir string `json:"onAir"`

	// PlaybackError is the last real failure from the streamer — ffmpeg could
	// not open the stream, the host was unreachable. A URL that resolves is not
	// a URL that plays, so this is the difference between "the slot is on air"
	// and silence with a green light.
	PlaybackError     string    `json:"playbackError,omitempty"`
	PlaybackErrorItem string    `json:"playbackErrorItem,omitempty"`
	PlaybackErrorAt   time.Time `json:"playbackErrorAt,omitempty"`

	NextRule   *ScheduleRule `json:"nextRule,omitempty"`
	NextRuleIn string        `json:"nextRuleIn,omitempty"`
	NextRuleAt string        `json:"nextRuleAt,omitempty"`
	TotalRules int           `json:"totalRules"`
	RulesToday int           `json:"rulesToday"`

	// Programming is what the station currently believes about itself.
	Programming ProgrammingStatus `json:"programming"`
}

// ProgrammingStatus is the plan, as the station is currently living it.
//
// Deliberately free of any particular category name. The old version of this
// reported talkMinutes and musicMinutes because those were the only two things
// the engine could imagine, which meant a station of comedy and jazz could not
// be described by its own diagnostics.
type ProgrammingStatus struct {
	// PlanSource is "custom" when somebody has written a plan, "derived" when
	// the station is running the plan its sources and slots imply.
	PlanSource string `json:"planSource"`

	BlockID     string    `json:"blockId,omitempty"`
	BlockLabel  string    `json:"blockLabel,omitempty"`
	EnteredAt   time.Time `json:"enteredAt,omitempty"`
	EntryReason string    `json:"entryReason,omitempty"`
	ExitReason  string    `json:"exitReason,omitempty"`

	// WindowHours is how far back the balance below is measured.
	WindowHours float64 `json:"windowHours"`
	// Categories is each category's target against what actually aired.
	Categories []CategoryStatus `json:"categories,omitempty"`
	// Limits is how close any block limit is to biting.
	Limits []LimitStatus `json:"limits,omitempty"`

	// NextAnchor is the next appointment, and RoomMinutes is how much space is
	// left before it — the number that explains why a long item is not being
	// started.
	NextAnchor  *AnchorSummary `json:"nextAnchor,omitempty"`
	RoomMinutes int            `json:"roomMinutes,omitempty"`

	// ListeningDay is the window in which airing something counts as reaching
	// anybody, which is what decides whether an episode is still new.
	ListeningDay string `json:"listeningDay"`

	// Unreachable names enabled sources no pool can select, so the station can
	// say out loud that it is ignoring them. A plan saved before this was
	// refused on save can still be in this state, and the failure is invisible
	// from every other screen: the source reads ENABLED, its episodes read as
	// owed, and it never plays.
	Unreachable []string `json:"unreachable,omitempty"`
}

func formatMinute(minute int) string {
	return time.Date(2026, 1, 1, 0, minute, 0, 0, time.UTC).Format("15:04")
}

// programmingStatus measures the station so the UI can show it rather than
// imply it.
func (s *Service) programmingStatus(ctx context.Context, channelID string, sched *Scheduler) ProgrammingStatus {
	status := ProgrammingStatus{PlanSource: "derived"}

	engine, state, err := sched.engineFor(ctx, channelID)
	if err != nil {
		return status
	}
	if _, stored, err := LoadPlan(ctx, s.db, channelID); err == nil && stored {
		status.PlanSource = "custom"
	}
	day := engine.listeningDay().normalized()
	status.ListeningDay = formatMinute(day.StartMinute) + "–" + formatMinute(day.EndMinute)
	status.WindowHours = engine.Plan.balanceHorizon().Hours()
	for _, orphan := range engine.Plan.UnreachableSources(engine.Sources) {
		status.Unreachable = append(status.Unreachable, firstNonEmpty(orphan.Label, orphan.Kind))
	}

	loc := engine.location()
	now := s.schedDeps().now().In(loc)
	timeline := BuildTimeline(engine.Plan, now, loc)
	tail, err := engine.History.Tail(ctx, 24*time.Hour, 200, now)
	if err != nil {
		tail = nil
	}
	env := engine.enumerationEnv(ctx, now, loc)
	cond := ConditionContext{
		Window:        timeline.Window(),
		PoolAvailable: func(poolID string) bool { return engine.PoolHasContent(ctx, poolID, env) },
		// What the station owes, or every block gated on "while episodes are
		// owed" reports as not entered no matter how many are.
		//
		// The real decision path fills this in; this one did not, so the status
		// panel resolved the block against a world where nothing is ever owed
		// and confidently named the wrong one. A status screen that disagrees
		// with the scheduler is worse than no status screen — it is the thing
		// you check to find out why the scheduler is behaving oddly.
		//
		// READ, not refresh: a peek must never notice new obligations, because
		// noticing is a write and this endpoint is asked on every page load.
		ObligationsPending: engine.pendingObligations(ctx, now).Len(),
	}
	block := ResolveBlock(engine.Plan, timeline, state, cond, now)
	intent := engine.buildIntent(block, timeline, tail, env)

	status.BlockID = intent.Block.ID
	status.BlockLabel = intent.BlockLabel
	status.EnteredAt = intent.EnteredAt
	status.EntryReason = intent.EntryReason
	status.ExitReason = intent.ExitReason
	status.RoomMinutes = int(intent.Window.Minutes())

	if timeline.Next != nil {
		policy := timeline.Next.Policy
		if policy == "" {
			policy = StartMakeNext
		}
		status.NextAnchor = &AnchorSummary{
			BlockID: timeline.Next.BlockID,
			Label:   timeline.Next.Label,
			Start:   timeline.Next.Start,
			At:      timeline.Next.Start.Format("15:04"),
			In:      timeline.Next.Start.Sub(now).Round(time.Minute).String(),
			Policy:  string(policy),
		}
	}

	// No candidate set: this readout only wants the airtime totals, and
	// enumerating the whole library to fill in per-source shares nobody here
	// reads would put a catalogue walk behind a status poll.
	scoring := engine.scoreEnv(ctx, now, intent, tail, nil)
	preview := Decision{}
	preview.applyBalance(intent.Targets, scoring.airtime)
	status.Categories = preview.Targets
	for _, limit := range intent.Limits {
		status.Limits = append(status.Limits, LimitStatus{
			Category:     limit.Category,
			RunMinutes:   int(limit.Run.Minutes()),
			MaxMinutes:   int(limit.Max.Minutes()),
			Exceeded:     limit.Exceeded(),
			HeadroomMins: int(limit.Remaining().Minutes()),
		})
	}
	return status
}

// ScheduleStatus reports why the channel is playing what it is playing.
func (s *Service) ScheduleStatus(ctx context.Context, channelID string) (ScheduleStatus, error) {
	channel, err := LoadChannel(ctx, s.db, channelID)
	if err != nil {
		return ScheduleStatus{}, err
	}
	deps := s.schedDeps()
	sched := NewScheduler(deps)
	location := deps.location(channel)
	at := deps.now().In(location)

	status := ScheduleStatus{
		Timezone:          zoneName(location, at),
		UsingFallbackZone: strings.TrimSpace(channel.Timezone) == "" && isUTC(at),
		Now:               at,
		LocalTime:         at.Format("15:04"),
		Weekday:           at.Weekday().String(),
		Minute:            at.Hour()*60 + at.Minute(),
	}
	status.Programming = s.programmingStatus(ctx, channelID, sched)

	// A running streamer knows something the scheduler cannot: whether the
	// audio actually came out.
	s.mu.Lock()
	streamer, running := s.streamers[channelID]
	s.mu.Unlock()
	if running {
		if message, item, at := streamer.LastError(); message != "" {
			status.PlaybackError = message
			status.PlaybackErrorItem = item
			status.PlaybackErrorAt = at
		}
	}

	rules, err := ListScheduleRules(ctx, s.db, channelID)
	if err != nil {
		return status, err
	}
	status.TotalRules = len(rules)
	weekdayBit := 1 << int(at.Weekday())
	for _, rule := range rules {
		if rule.Enabled && rule.WeekdayMask&weekdayBit != 0 {
			status.RulesToday++
		}
	}

	sources, err := ListChannelSources(ctx, s.db, channelID)
	if err != nil {
		return status, err
	}
	byID := map[string]Source{}
	for _, src := range sources {
		byID[src.ID] = src
	}

	if rule, ok := pickActiveRule(rules, at); ok {
		active := rule
		status.ActiveRule = &active
		src, known := byID[rule.SourceID]
		switch {
		case !known:
			status.RuleError = "the slot points at a source that no longer exists"
		case !src.Enabled:
			status.ActiveSource = &src
			status.RuleError = "the slot's source is disabled"
		default:
			status.ActiveSource = &src
		}
		if status.RuleError == "" {
			status.OnAir = "scheduled slot is on air"
			if status.PlaybackError != "" {
				status.OnAir = "scheduled slot is selected but its audio is failing"
			}
		} else {
			status.OnAir = "scheduled slot could not play, so the rotation is filling in"
		}
		return status, nil
	}

	if next, at, ok := nextRuleAfter(rules, at); ok {
		upcoming := next
		status.NextRule = &upcoming
		status.NextRuleAt = at.Format("15:04")
		status.NextRuleIn = at.Sub(status.Now).Round(time.Minute).String()
	}
	switch {
	case status.TotalRules == 0 && status.Programming.PlanSource == "custom":
		status.OnAir = "running a custom plan"
	case status.TotalRules == 0:
		status.OnAir = "no slots booked; the rotation runs the whole day"
	case status.RulesToday == 0:
		status.OnAir = "no slots booked for " + status.Weekday
	default:
		status.OnAir = "no slot is open right now; the rotation is playing"
	}
	return status, nil
}

// pickActiveRule walks booked slots by priority and returns the first whose
// window contains `at` on the right weekday.
//
// Still here because booked slots remain a first-class way to programme a
// channel — a plan turns each one into an anchored block — and because the
// status panel reports on them in the operator's own vocabulary.
func pickActiveRule(rules []ScheduleRule, at time.Time) (ScheduleRule, bool) {
	weekday := int(at.Weekday()) // 0=Sun..6=Sat
	minute := at.Hour()*60 + at.Minute()
	matches := make([]ScheduleRule, 0)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if rule.WeekdayMask&(1<<weekday) == 0 {
			continue
		}
		if minute < rule.StartMinute || minute >= rule.EndMinute {
			continue
		}
		matches = append(matches, rule)
	}
	if len(matches) == 0 {
		return ScheduleRule{}, false
	}
	best := matches[0]
	for _, rule := range matches[1:] {
		if rule.Priority > best.Priority {
			best = rule
		}
	}
	return best, true
}

// nextRuleAfter finds the next slot due today, and when it starts.
func nextRuleAfter(rules []ScheduleRule, at time.Time) (ScheduleRule, time.Time, bool) {
	minute := at.Hour()*60 + at.Minute()
	weekdayBit := 1 << int(at.Weekday())
	best := -1
	bestStart := 0
	for index, rule := range rules {
		if !rule.Enabled || rule.WeekdayMask&weekdayBit == 0 {
			continue
		}
		if rule.StartMinute <= minute {
			continue
		}
		if best < 0 || rule.StartMinute < bestStart {
			best, bestStart = index, rule.StartMinute
		}
	}
	if best < 0 {
		return ScheduleRule{}, time.Time{}, false
	}
	loc := at.Location()
	return rules[best], wallClock(startOfDay(at, loc), bestStart, loc), true
}
