package channels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The air order: when the station will play each thing it owes.
//
// The owed queue is an order of URGENCY, and it is not the running order. A
// decision filters before it ranks: an episode that cannot finish before the
// next booked show waits for room it fits, and something shorter goes first.
// On Saturday 2026-09-26 the WAN Show (S tier, 4h12m, out the night before)
// headed the queue and Car Talk (A tier, 36m) aired at 09:00, because Wait
// Wait Don't Tell Me was booked at 10:00; WAN went out at 11:07, straight
// after it -- the earliest it could air whole. The wall read the queue as a
// running order, split it into "free now" and "held now" against the music
// hour that happened to be on air, and drew Car Talk first with nothing to
// say why. It looked like the tiers were being ignored, and they were not.
//
// Only the station knows when it will play something, so the forecast IS the
// station: the simulator (sim.go) run from the live channel -- its plan, its
// programme state, its play log, what it owes and what was skipped -- through
// the same Engine.Decide the streamer calls, on a virtual clock, with every
// store a private copy in memory. There is no second statement of any rule
// here, so the forecast and the air can only disagree about things that have
// not happened yet: a skip, a feed publishing, a stream that fails.

const (
	// forecastHorizon is as far ahead as a forecast looks: past the overnight
	// hold to the next listening day's first new episode, from any hour.
	forecastHorizon = 24 * time.Hour
	// forecastMaxSteps bounds the decisions in one run. A day of this station
	// is a hundred or so; a day of nothing but three-minute songs is ~480.
	forecastMaxSteps = 600
	// forecastMaxAge is how long a forecast is trusted when nothing it was made
	// from has changed. Songs drift the times by a minute or two an hour; this
	// bounds the drift without re-running a day of radio on every poll.
	forecastMaxAge = 15 * time.Minute
	// forecastTimeout bounds one run. A run cut short still places what it
	// reached.
	forecastTimeout = 2 * time.Minute
	// forecastRetry is the least time between two runs for one channel, so a
	// burst of asks while the inputs change starts one run, not one per ask.
	forecastRetry = 30 * time.Second
)

// Forecast is when the station expects to air what it owes.
type Forecast struct {
	// ComputedAt is the wall-clock moment the run was made.
	ComputedAt time.Time `json:"computedAt"`
	// From is where the run began: the end of the item on air, or now.
	From time.Time `json:"from"`
	// Until is how far the run reached: the moment it placed the last thing
	// it was waiting for, or, when it stopped short, where the horizon or the
	// time budget stopped it. Not the end of that last item -- a four-hour
	// episode placed at 11:00 is the answer at 11:00, and the forecast has
	// nothing to learn from playing it out.
	Until time.Time `json:"until"`
	// Complete is true when everything owed that the plan can reach found its
	// airing before the run stopped; false when the horizon or the time budget
	// came first.
	Complete bool `json:"complete"`
	// Airings is when each item first goes out in the run, by item ref.
	Airings map[string]time.Time `json:"-"`
}

// OnAir is the item a forecast starts behind: what the streamer is playing,
// when it started, and when the streamer's own clocks say it ends.
type OnAir struct {
	Item      PlaybackItem
	StartedAt time.Time
	// EndsAt is zero when nothing bounds the item -- an episode nobody
	// measured, in an open stretch -- and the forecast then starts now.
	EndsAt time.Time
}

// Forecast runs the channel forward from where it stands and says when each
// thing it owes will air.
//
// It reads the live tables once and writes nothing: the play log, the
// obligations and the skip registry it runs against are private copies, and
// nothing it "plays" is fetched (Engine.Simulated).
func (s *Scheduler) Forecast(ctx context.Context, channelID string, onAir *OnAir) (Forecast, error) {
	if s.deps.DB == nil {
		return Forecast{}, errors.New("scheduler has no database")
	}
	live, state, err := s.engineFor(ctx, channelID)
	if err != nil {
		return Forecast{}, err
	}
	loc := live.location()
	now := s.deps.now().In(loc)
	from := now
	if onAir != nil && onAir.EndsAt.After(now) {
		from = onAir.EndsAt.In(loc)
	}

	stored, err := ObligationsFor(ctx, s.deps.DB, channelID, now)
	if err != nil {
		return Forecast{}, err
	}
	owed := NewMemoryObligations()
	_ = owed.Notice(ctx, stored, now)

	plays, err := PlayLogSince(ctx, s.deps.DB, channelID, now.Add(-historyLookback(live.Plan)))
	if err != nil {
		return Forecast{}, err
	}
	history := NewMemoryHistory()
	for _, play := range plays {
		if play.EndedAt.IsZero() {
			play.EndedAt = openRowEnd(play, onAir, from, now)
		}
		history.Record(play)
	}
	if onAir != nil {
		creditOnAir(ctx, owed, *onAir, from)
	}

	run := *live
	run.History = history
	run.Obligations = owed
	forecast, err := forecastRun(ctx, &run, s.deps.Skips, state, from, forecastHorizon)
	if err != nil {
		return Forecast{}, err
	}
	forecast.ComputedAt = s.deps.now()
	return forecast, nil
}

// forecastRun runs the engine forward from state at `from` and reports when
// each item first airs. The engine's stores must be private copies -- the
// simulator insists on a *MemoryHistory, and this on a *MemoryObligations --
// and the skip registry is copied here onto the run's clock.
func forecastRun(
	ctx context.Context,
	engine *Engine,
	skips *SkipRegistry,
	state ProgramState,
	from time.Time,
	horizon time.Duration,
) (Forecast, error) {
	if _, ok := engine.Obligations.(*MemoryObligations); !ok {
		return Forecast{}, fmt.Errorf("a forecast needs private obligations, not %T", engine.Obligations)
	}
	clock := from
	engine.Skips = skips.Snapshot(func() time.Time { return clock })
	// An episode the feeds brought in since the last real decision is noticed
	// into the private copy on the first decision, as owedSnapshot does.
	engine.ReadOnly = false

	aired := map[string]time.Time{}
	// waiting is whether anything owed is still to find its airing. An owed
	// episode whose source no pool reaches never will (JudgeOwed reports it
	// held, "unreachable"), so it cannot hold the run open.
	waiting := func(now time.Time) bool {
		for _, owed := range engine.pendingObligations(ctx, now).Pending {
			if _, done := aired[owed.ItemRef]; done {
				continue
			}
			if src, ok := engine.source(owed.SourceID); ok && !engine.Plan.reaches(src) {
				continue
			}
			return true
		}
		return false
	}

	// settled is the decision that placed the last thing the run was waiting
	// for, where the run stops. The simulator has played that item out by the
	// time it asks whether to stop, so its own end is the item's, not this.
	var settled time.Time
	result, err := Simulate(ctx, engine, SimOptions{
		Start:    from,
		Duration: horizon,
		MaxSteps: forecastMaxSteps,
		State:    state,
		Clock:    func(now time.Time) { clock = now },
		Stop: func(step SimStep) bool {
			if ref := step.Item.ItemRef; ref != "" {
				if _, seen := aired[ref]; !seen {
					aired[ref] = step.At.UTC()
				}
			}
			if waiting(step.Ends) {
				return false
			}
			settled = step.At
			return true
		},
	})
	if err != nil {
		return Forecast{}, err
	}
	until := result.Report.To
	if !settled.IsZero() {
		until = settled
	}
	return Forecast{
		From:     from.UTC(),
		Until:    until.UTC(),
		Complete: !waiting(result.Report.To),
		Airings:  aired,
	}, nil
}

// historyLookback is as far back as any rule the engine applies reads the play
// log: the rerun horizon, or a long-form rest longer than that.
func historyLookback(plan Plan) time.Duration {
	lookback := plan.rerunHorizon()
	consider := func(policy LongFormPolicy) {
		if rest := policy.rest(); rest > lookback && rest < neverAgain {
			lookback = rest
		}
	}
	consider(plan.LongForm)
	for _, block := range plan.Blocks {
		if block.LongForm != nil {
			consider(*block.LongForm)
		}
	}
	for _, floor := range []time.Duration{24 * time.Hour, plan.balanceHorizon()} {
		if floor > lookback {
			lookback = floor
		}
	}
	return lookback
}

// openRowEnd is where a play-log row with no end is taken to end: the item on
// air where the forecast starts, and anything else -- a row a crash left open
// -- at its own length, never later than now.
func openRowEnd(play MemoryPlay, onAir *OnAir, from, now time.Time) time.Time {
	if onAir != nil && play.ItemRef == onAir.Item.ItemRef && play.SourceID == onAir.Item.SourceID {
		return from
	}
	end := play.StartedAt.Add(time.Duration(play.DurationSeconds) * time.Second)
	if play.DurationSeconds <= 0 || end.After(now) {
		end = now
	}
	return end
}

// creditOnAir gives the item on air the credit OnPlayEnd will give it when it
// ends where the forecast starts: its exposure times the fraction played. An
// item ending at its own length completes; one capped short of it does not,
// and one nobody measured earns nothing when it is cut -- the same rules.
func creditOnAir(ctx context.Context, owed *MemoryObligations, onAir OnAir, from time.Time) {
	item := onAir.Item
	if item.ItemRef == "" || item.Exposure <= 0 || onAir.StartedAt.IsZero() {
		return
	}
	played := from.Sub(onAir.StartedAt)
	completed := item.DurationSeconds <= 0 && onAir.EndsAt.IsZero()
	if item.DurationSeconds > 0 {
		completed = played >= time.Duration(item.DurationSeconds)*time.Second
	}
	if credit := item.Exposure * playedFraction(item, played, completed); credit > 0 {
		_ = owed.Credit(ctx, item.ItemRef, credit, from)
	}
}

// ----- the service's copy ------------------------------------------------

// forecastSlot is one channel's latest forecast and what it was made from.
type forecastSlot struct {
	forecast  Forecast
	have      bool
	key       string
	running   bool
	attempted time.Time
}

// forecastKey is what a forecast depends on that can change between two asks:
// the plan and its sources, what is owed and how far along each is, and the
// programme on air. Not a song -- a music hour's songs move the podcasts
// behind it by less than the drift forecastMaxAge already allows, and keying
// on them would re-run a day of radio every three minutes.
func forecastKey(plan Plan, sources []Source, pending []Obligation, onAir *OnAir) string {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode(plan)
	for _, src := range sources {
		_ = encoder.Encode([]any{src.ID, src.Enabled, src.Role, src.Weight, src.Config})
	}
	for _, owed := range pending {
		fmt.Fprintf(hash, "owed %s %.2f %s\n", owed.ItemRef, owed.Credit, owed.State)
	}
	if onAir != nil && !onAir.Item.Shuffled {
		fmt.Fprintf(hash, "on air %s %s %d\n", onAir.Item.ItemRef, onAir.StartedAt.UTC().Format(time.RFC3339), onAir.EndsAt.Unix())
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// airOrder is the channel's latest forecast, and a fresh run started in the
// background when that no longer describes the station -- the key changed, or
// it is older than forecastMaxAge.
//
// Never waits for a run: a day of radio takes seconds to simulate, and a
// request should not. The first ask for a channel gets nothing, and an ask
// while a run is going gets the last one that finished.
func (s *Service) airOrder(channelID, key string, onAir *OnAir) (Forecast, bool) {
	now := time.Now()
	s.forecastMu.Lock()
	defer s.forecastMu.Unlock()
	if s.forecasts == nil {
		s.forecasts = map[string]*forecastSlot{}
	}
	slot := s.forecasts[channelID]
	if slot == nil {
		slot = &forecastSlot{}
		s.forecasts[channelID] = slot
	}
	current := slot.have && slot.key == key && now.Sub(slot.forecast.ComputedAt) < forecastMaxAge
	if !current && !slot.running && !s.forecastClosed && now.Sub(slot.attempted) >= forecastRetry {
		slot.running = true
		slot.attempted = now
		// Added under forecastMu, which Close takes to set forecastClosed
		// before it waits, so no run can start once the wait has begun.
		s.forecastRuns.Add(1)
		go s.runForecast(channelID, key, onAir)
	}
	return slot.forecast, slot.have
}

// stopForecastRuns cancels every forecast still running and waits for them
// to finish, or for ctx. None starts after it.
func (s *Service) stopForecastRuns(ctx context.Context) {
	s.forecastMu.Lock()
	s.forecastClosed = true
	s.forecastMu.Unlock()
	if s.stopForecasts != nil {
		s.stopForecasts()
	}
	done := make(chan struct{})
	go func() {
		s.forecastRuns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Forecasted is the channel's latest finished forecast, without asking for a
// new one.
func (s *Service) Forecasted(channelID string) (Forecast, bool) {
	s.forecastMu.Lock()
	defer s.forecastMu.Unlock()
	slot := s.forecasts[channelID]
	if slot == nil || !slot.have {
		return Forecast{}, false
	}
	return slot.forecast, true
}

// runForecast makes one forecast and files it. It runs beside the station, so
// nothing that goes wrong in it may reach the station: a failure is logged and
// the previous forecast stands, and a panic is caught here rather than taking
// the process -- and every channel on the air -- down with it.
//
// A run Close cut short is neither: the service is going away, so it is not
// filed and not reported.
func (s *Service) runForecast(channelID, key string, onAir *OnAir) {
	defer s.forecastRuns.Done()
	var (
		forecast Forecast
		err      error
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
		s.forecastMu.Lock()
		defer s.forecastMu.Unlock()
		slot := s.forecasts[channelID]
		if slot == nil {
			return
		}
		slot.running = false
		if s.forecastCtx.Err() != nil {
			return
		}
		if err != nil {
			s.logger.Printf("channel %s: could not forecast the air order: %v", channelID, err)
			return
		}
		slot.forecast, slot.key, slot.have = forecast, key, true
	}()
	ctx, cancel := context.WithTimeout(s.forecastCtx, forecastTimeout)
	defer cancel()
	forecast, err = NewScheduler(s.schedDeps()).Forecast(ctx, channelID, onAir)
}
