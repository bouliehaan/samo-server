package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ServiceOptions wires up dependencies the channels service needs.
type ServiceOptions struct {
	DB               *sql.DB
	Catalog          CatalogReader
	Cache            EpisodeCacheLookup
	InternetStations InternetStationLookup
	// Listened keeps podcast sources off episodes somebody already heard.
	Listened EpisodeProgressLookup
	// DefaultLocation is the wall clock schedules are read in when a channel
	// does not name its own.
	DefaultLocation *time.Location
	// DefaultTalkShare is the talk fraction for channels that do not set one.
	DefaultTalkShare float64
	FFmpegPath       string
	Logger           *log.Logger

	// Loudness levels items against each other so a podcast and a pop master
	// air at the same perceived volume. Nil leaves every item at its native
	// level.
	Loudness LoudnessPlanner

	// BaseContext roots every streamer's ffmpeg subprocess. Pass the process
	// lifetime context so shutdown reaps them; see StreamerOptions.BaseContext
	// for why leaving this unset leaks a transcoder per channel per restart.
	BaseContext context.Context
}

// Service is the public entry point. It owns one ffmpeg streamer per
// channel and re-uses it across listeners. CRUD methods proxy to the
// store; streaming methods proxy to the per-channel streamer.
type Service struct {
	db               *sql.DB
	catalog          CatalogReader
	cache            EpisodeCacheLookup
	internetStations InternetStationLookup
	listened         EpisodeProgressLookup
	defaultLocation  *time.Location
	defaultTalkShare float64
	skips            *SkipRegistry
	ffmpegPath       string
	logger           *log.Logger
	baseCtx          context.Context
	loudness         LoudnessPlanner

	mu        sync.Mutex
	streamers map[string]*channelStreamer
}

func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	baseCtx := opts.BaseContext
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return &Service{
		db:               opts.DB,
		catalog:          opts.Catalog,
		cache:            opts.Cache,
		internetStations: opts.InternetStations,
		listened:         opts.Listened,
		defaultLocation:  opts.DefaultLocation,
		defaultTalkShare: opts.DefaultTalkShare,
		skips:            NewSkipRegistry(nil),
		ffmpegPath:       opts.FFmpegPath,
		logger:           logger,
		baseCtx:          baseCtx,
		loudness:         opts.Loudness,
		streamers:        map[string]*channelStreamer{},
	}
}

// Close stops every running streamer and waits for their ffmpeg subprocesses
// to be reaped. Call it during shutdown: Go does not kill child processes on
// exit, so without this a restart leaves one orphaned transcoder per active
// channel, still holding its input and still burning CPU.
func (s *Service) Close(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	streamers := make([]*channelStreamer, 0, len(s.streamers))
	for id, streamer := range s.streamers {
		streamers = append(streamers, streamer)
		delete(s.streamers, id)
	}
	s.mu.Unlock()

	for _, streamer := range streamers {
		streamer.stopAndWait(ctx)
	}
}

// schedDeps builds the dependency bundle once so PreviewNext and
// streamerFor stay in sync (and any future caller only needs a single
// constructor to wire up).
func (s *Service) schedDeps() Dependencies {
	return Dependencies{
		DB:               s.db,
		Catalog:          s.catalog,
		Cache:            s.cache,
		InternetStations: s.internetStations,
		Listened:         s.listened,
		Skips:            s.skips,
		DefaultLocation:  s.defaultLocation,
		DefaultTalkShare: s.defaultTalkShare,
		Logger:           s.logger,
		Now:              func() time.Time { return time.Now().UTC() },
	}
}

// withEffectiveZone fills in the clock a channel's schedule is actually read
// in, so the UI can label its times instead of leaving them ambiguous.
func (s *Service) withEffectiveZone(ch Channel) Channel {
	deps := s.schedDeps()
	location := deps.location(ch)
	ch.EffectiveTimezone = zoneName(location, deps.now().In(location))
	return ch
}

// ----- CRUD ------------------------------------------------------------

func (s *Service) ListChannels(ctx context.Context) ([]Channel, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("channels service not initialised")
	}
	items, err := ListChannels(ctx, s.db)
	if err != nil {
		return nil, err
	}
	for index := range items {
		items[index] = s.withEffectiveZone(items[index])
	}
	return items, nil
}

func (s *Service) GetChannel(ctx context.Context, id string) (Channel, error) {
	ch, err := LoadChannel(ctx, s.db, id)
	if err != nil {
		return Channel{}, err
	}
	sources, _ := ListChannelSources(ctx, s.db, id)
	rules, _ := ListScheduleRules(ctx, s.db, id)
	ch.Sources = sources
	ch.ScheduleRules = rules
	return s.withEffectiveZone(ch), nil
}

func (s *Service) CreateChannel(ctx context.Context, input CreateChannelInput) (Channel, error) {
	return InsertChannel(ctx, s.db, input)
}

func (s *Service) UpdateChannel(ctx context.Context, id string, input UpdateChannelInput) (Channel, error) {
	ch, err := UpdateChannel(ctx, s.db, id, input)
	if err != nil {
		return Channel{}, err
	}
	// Output-format changes mean any running ffmpeg is now stale —
	// restart so listeners get the new bitrate/codec.
	s.restartIfRunning(id)
	return ch, nil
}

func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	s.stopStreamer(id)
	return DeleteChannel(ctx, s.db, id)
}

func (s *Service) AddSource(ctx context.Context, channelID string, input CreateSourceInput) (Source, error) {
	src, err := InsertSource(ctx, s.db, channelID, input)
	if err == nil {
		// New source might unblock a previously-empty channel that
		// errored out. Kick the streamer so it picks it up on the
		// next scheduler call.
		s.bumpStreamer(channelID)
	}
	return src, err
}

func (s *Service) UpdateSource(ctx context.Context, id string, input UpdateSourceInput) (Source, error) {
	src, err := UpdateSource(ctx, s.db, id, input)
	if err == nil {
		s.bumpStreamer(src.ChannelID)
	}
	return src, err
}

func (s *Service) DeleteSource(ctx context.Context, id string) error {
	src, _ := LoadSource(ctx, s.db, id)
	if err := DeleteSource(ctx, s.db, id); err != nil {
		return err
	}
	if src.ChannelID != "" {
		s.bumpStreamer(src.ChannelID)
	}
	return nil
}

func (s *Service) AddScheduleRule(ctx context.Context, channelID string, input CreateScheduleRuleInput) (ScheduleRule, error) {
	rule, err := InsertScheduleRule(ctx, s.db, channelID, input)
	if err == nil {
		s.bumpStreamer(channelID)
	}
	return rule, err
}

func (s *Service) DeleteScheduleRule(ctx context.Context, id string) error {
	rule, _ := LoadScheduleRule(ctx, s.db, id)
	if err := DeleteScheduleRule(ctx, s.db, id); err != nil {
		return err
	}
	if rule.ChannelID != "" {
		s.bumpStreamer(rule.ChannelID)
	}
	return nil
}

func (s *Service) ListSources(ctx context.Context, channelID string) ([]Source, error) {
	return ListChannelSources(ctx, s.db, channelID)
}

func (s *Service) ListScheduleRules(ctx context.Context, channelID string) ([]ScheduleRule, error) {
	return ListScheduleRules(ctx, s.db, channelID)
}

func (s *Service) RecentPlayLog(ctx context.Context, channelID string, limit int) ([]PlayLogEntry, error) {
	return RecentPlayLog(ctx, s.db, channelID, limit)
}

// ----- Streaming -------------------------------------------------------

// Attach hands the caller a listener channel hooked into the per-channel
// broadcaster. The returned detach function MUST be called when the
// HTTP request goroutine exits. Starts the streamer lazily on the
// first listener.
func (s *Service) Attach(ctx context.Context, channelID string) (<-chan []byte, string, func(), error) {
	streamer, err := s.streamerFor(ctx, channelID)
	if err != nil {
		return nil, "", func() {}, err
	}
	lis, detach := streamer.Attach()
	return lis.ch, contentTypeFor(streamer.channel.Codec), detach, nil
}

// NowPlaying returns the current item + recent play log for the channel.
// When the streamer hasn't started yet, Current is nil but the recent
// list still reflects historical playback.
func (s *Service) NowPlaying(ctx context.Context, channelID string) (NowPlaying, error) {
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return NowPlaying{}, err
	}
	recent, err := RecentPlayLog(ctx, s.db, channelID, 10)
	if err != nil {
		return NowPlaying{}, err
	}
	np := NowPlaying{ChannelID: channelID, Recent: recent}
	s.mu.Lock()
	streamer, ok := s.streamers[channelID]
	s.mu.Unlock()
	if ok {
		np.ListenerCount = streamer.ListenerCount()
		if item, startedAt, _, present := streamer.Now(); present {
			cur := item
			np.Current = &cur
			t := startedAt
			np.StartedAt = &t
		}
	}
	return np, nil
}

// PreviewNext returns what would play right now, without starting ffmpeg and
// without spending anything. Useful for UI testing and "is this channel even
// configured?" checks — neither of which should consume a listener's skip.
func (s *Service) PreviewNext(ctx context.Context, channelID string) (PlaybackItem, error) {
	sched := NewScheduler(s.schedDeps())
	return sched.PeekItem(ctx, channelID)
}

// ----- the plan --------------------------------------------------------

// PlanView is a channel's plan plus where it came from.
type PlanView struct {
	Plan Plan `json:"plan"`
	// Custom is false when nobody has written a plan and this is the one the
	// channel's own sources and booked slots already describe. Editing a
	// derived plan and saving it makes it custom — which is the intended way in,
	// because it means the first edit starts from something that works.
	Custom bool `json:"custom"`
}

// GetPlan returns the channel's plan, deriving one if it has none.
func (s *Service) GetPlan(ctx context.Context, channelID string) (PlanView, error) {
	channel, err := LoadChannel(ctx, s.db, channelID)
	if err != nil {
		return PlanView{}, err
	}
	if plan, ok, err := LoadPlan(ctx, s.db, channelID); err == nil && ok {
		return PlanView{Plan: plan, Custom: true}, nil
	} else if err != nil {
		s.logger.Printf("channel %s: %v", channelID, err)
	}
	sources, err := ListChannelSources(ctx, s.db, channelID)
	if err != nil {
		return PlanView{}, err
	}
	rules, err := ListScheduleRules(ctx, s.db, channelID)
	if err != nil {
		return PlanView{}, err
	}
	deps := s.schedDeps()
	return PlanView{
		Plan:   DerivePlan(channel, filterEnabledSources(sources), rules, deps.talkShare(channel)),
		Custom: false,
	}, nil
}

// SetPlan validates and stores a plan document.
func (s *Service) SetPlan(ctx context.Context, channelID string, raw []byte) (PlanView, error) {
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return PlanView{}, err
	}
	plan, err := ParsePlan(raw)
	if err != nil {
		return PlanView{}, err
	}
	// A plan that cannot reach some of the channel's own content is refused.
	// Silently unreachable content is how a tier-S podcast sat in the library
	// for days, showed as enabled on one screen and as owed on another, and
	// could never go to air.
	sources, err := ListChannelSources(ctx, s.db, channelID)
	if err != nil {
		return PlanView{}, err
	}
	if orphans := plan.UnreachableSources(filterEnabledSources(sources)); len(orphans) > 0 {
		names := make([]string, 0, len(orphans))
		for _, src := range orphans {
			names = append(names, firstNonEmpty(src.Label, src.Kind))
		}
		return PlanView{}, fmt.Errorf(
			"%w: no pool can reach %s — every enabled source needs a pool, "+
				"or the station will never play it",
			ErrInvalidID, strings.Join(names, ", "))
	}
	if err := SavePlan(ctx, s.db, channelID, plan); err != nil {
		return PlanView{}, err
	}
	// A plan edit can change which block the station belongs in, and the stored
	// state names a block that may no longer exist. Clearing it makes the next
	// decision re-enter from scratch rather than get stuck referring to
	// something that is gone.
	if err := SaveProgramState(ctx, s.db, channelID, ProgramState{}); err != nil {
		s.logger.Printf("channel %s: could not reset programme state after a plan edit: %v", channelID, err)
	}
	return PlanView{Plan: plan, Custom: true}, nil
}

// ResetPlan drops a custom plan, returning the channel to the derived one.
func (s *Service) ResetPlan(ctx context.Context, channelID string) error {
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return err
	}
	if err := DeletePlan(ctx, s.db, channelID); err != nil {
		return err
	}
	return SaveProgramState(ctx, s.db, channelID, ProgramState{})
}

// Owed is what the station currently owes the listener, most urgent first.
//
// Read straight from the store rather than from a decision, so it answers even
// when nothing is on air — "why has my new episode not played yet" is usually
// asked about a channel nobody is listening to.
func (s *Service) Owed(ctx context.Context, channelID string) ([]Obligation, error) {
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return nil, err
	}
	deps := s.schedDeps()
	obligations, err := ObligationsFor(ctx, s.db, channelID, deps.now())
	if err != nil {
		return nil, err
	}
	// Label them from the sources as they are now, and order them the way the
	// scheduler will.
	sources, _ := ListChannelSources(ctx, s.db, channelID)
	byID := map[string]Source{}
	for _, src := range sources {
		byID[src.ID] = src
	}
	for index := range obligations {
		if src, ok := byID[obligations[index].SourceID]; ok {
			obligations[index].SourceLabel = src.Label
		}
	}
	channel, err := LoadChannel(ctx, s.db, channelID)
	if err != nil {
		return obligations, nil
	}
	plan, err := s.plan(ctx, channel, filterEnabledSources(sources))
	if err != nil {
		return obligations, nil
	}
	queue := NewObligationQueue(obligations, deps.now(), plan.Freshness)
	return append(queue.Pending, queue.Satisfied...), nil
}

// plan resolves the channel's plan without going through the API shape.
func (s *Service) plan(ctx context.Context, channel Channel, sources []Source) (Plan, error) {
	return NewScheduler(s.schedDeps()).PlanFor(ctx, channel, sources)
}

// ----- why did it play that --------------------------------------------

// Why returns the recorded decisions, newest first, and — when the channel is
// not currently running — what it would decide right now.
//
// Both, because they answer different questions. The recorded ones explain what
// you just heard; the live one explains what a channel with no listeners would
// do if you tuned in, which is the only way to debug a station that is silent.
func (s *Service) Why(ctx context.Context, channelID string, limit int) ([]Decision, error) {
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return nil, err
	}
	recorded, err := RecentDecisions(ctx, s.db, channelID, limit)
	if err != nil {
		return nil, err
	}
	if len(recorded) > 0 {
		return recorded, nil
	}
	sched := NewScheduler(s.schedDeps())
	decision, err := sched.Explain(ctx, channelID)
	if err != nil && decision.BlockID == "" {
		return nil, err
	}
	return []Decision{decision}, nil
}

// ----- internal helpers -----------------------------------------------

func (s *Service) streamerFor(ctx context.Context, channelID string) (*channelStreamer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.streamers[channelID]; ok {
		return existing, nil
	}
	channel, err := LoadChannel(ctx, s.db, channelID)
	if err != nil {
		return nil, err
	}
	if !channel.Enabled {
		return nil, errors.New("channel disabled")
	}
	deps := s.schedDeps()
	sched := NewScheduler(deps)
	streamer := newChannelStreamer(channel, deps, sched, StreamerOptions{
		FFmpegPath:  s.ffmpegPath,
		Logger:      s.logger,
		BaseContext: s.baseCtx,
		Loudness:    s.loudness,
	}, &serviceRecorder{db: s.db, baseCtx: s.baseCtx, logger: s.logger})
	s.streamers[channelID] = streamer
	return streamer, nil
}

// bumpStreamer is best-effort: if the channel has no running streamer
// (no listeners), do nothing. If it's running, leave it — the
// scheduler picks up source/rule changes on each NextItem call.
// In the future we could kill the in-flight ffmpeg to force an
// immediate re-pick, but that interrupts the listener mid-track which
// is worse than the small lag of "new rule takes effect next track."
func (s *Service) bumpStreamer(channelID string) {
	// Intentionally a no-op for now. Documenting intent for the
	// follow-up that might want to surface "schedule changed,
	// re-evaluating" UX.
}

func (s *Service) restartIfRunning(channelID string) {
	s.mu.Lock()
	streamer, ok := s.streamers[channelID]
	delete(s.streamers, channelID)
	s.mu.Unlock()
	if ok {
		streamer.stopLoop()
	}
}

func (s *Service) stopStreamer(channelID string) {
	s.mu.Lock()
	streamer, ok := s.streamers[channelID]
	delete(s.streamers, channelID)
	s.mu.Unlock()
	if ok {
		streamer.stopLoop()
	}
}

// ----- recorder implementation ----------------------------------------

// serviceRecorder writes the play log. Its writes are deliberately given their
// own short deadline rather than the streamer's item context: the play log for
// a track that just ended should still be written when that item was cancelled,
// but it must not outlive the process.
type serviceRecorder struct {
	db      *sql.DB
	baseCtx context.Context
	logger  *log.Logger
}

const playLogWriteTimeout = 5 * time.Second

func (r *serviceRecorder) writeCtx() (context.Context, context.CancelFunc) {
	base := r.baseCtx
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, playLogWriteTimeout)
}

func (r *serviceRecorder) OnPlayStart(channelID string, item PlaybackItem) (string, error) {
	if r.db == nil {
		return "", nil
	}
	ctx, cancel := r.writeCtx()
	defer cancel()
	return RecordPlayStart(ctx, r.db, channelID, item)
}

func (r *serviceRecorder) OnPlayDiscard(playLogID string) {
	if r.db == nil || playLogID == "" {
		return
	}
	ctx, cancel := r.writeCtx()
	defer cancel()
	_ = DiscardPlayLog(ctx, r.db, playLogID)
}

// OnPlayEnd closes the play-log row and settles any obligation the item was
// surfacing.
//
// The credit is the airing's exposure — how much the block it started in counts
// for — times how much of the item actually went out. That one multiplication
// is what makes an overnight play leave an episode still owed, a five-minute
// preemption leave it mostly owed, and a full daytime airing settle it.
func (r *serviceRecorder) OnPlayEnd(channelID string, item PlaybackItem, played time.Duration, completed bool, playLogID string) {
	if r.db == nil {
		return
	}
	ctx, cancel := r.writeCtx()
	defer cancel()
	if playLogID != "" {
		_ = RecordPlayEnd(ctx, r.db, playLogID)
	}
	if item.ItemRef == "" || item.Exposure <= 0 {
		return
	}
	credit := item.Exposure * playedFraction(item, played, completed)
	if credit <= 0 {
		return
	}
	if err := NewSQLObligations(r.db, channelID).Credit(ctx, item.ItemRef, credit, time.Now().UTC()); err != nil {
		r.logf("channel %s: could not credit %s: %v", channelID, item.ItemRef, err)
	}
}

// playedFraction is how much of an item went out, 0..1.
//
// The awkward case is an item whose length nobody knows — feed episodes very
// often report no duration at all. A clean end means all of it, whatever "all"
// was. A cut with no known length means we cannot show it reached anybody, so
// it stays owed: for a subscription, offering something twice is a smaller
// failure than silently never offering it again.
func playedFraction(item PlaybackItem, played time.Duration, completed bool) float64 {
	if completed {
		return 1
	}
	if item.DurationSeconds <= 0 {
		return 0
	}
	fraction := float64(played) / float64(time.Duration(item.DurationSeconds)*time.Second)
	if fraction < 0 {
		return 0
	}
	if fraction > 1 {
		return 1
	}
	return fraction
}

func (r *serviceRecorder) logf(format string, args ...any) {
	if r.logger == nil {
		return
	}
	r.logger.Printf(format, args...)
}

func contentTypeFor(codec string) string {
	switch codec {
	case "aac":
		return "audio/aac"
	case "ogg":
		return "audio/ogg"
	case "opus":
		return "audio/ogg"
	default:
		return "audio/mpeg"
	}
}

// SkipScope says how much to skip past.
type SkipScope string

const (
	// SkipItem drops the thing playing and stays on the same source. For "not
	// this episode, give me another one".
	SkipItem SkipScope = "item"
	// SkipKind passes over every source of the same kind for a few hours, so
	// the ladder moves to different media entirely. For "not podcasts right
	// now, put some music on".
	SkipKind SkipScope = "kind"
	// SkipSource is the older name for SkipKind, kept so an in-flight client
	// does not get a 400 mid-deploy.
	SkipSource SkipScope = "source"
)

// Skip moves the channel on.
//
// Only meaningful while a streamer is running: a channel nobody is listening
// to has nothing to skip, and the ladder will pick fresh the moment somebody
// tunes in. Returns false when there was nothing playing.
func (s *Service) Skip(ctx context.Context, channelID string, scope SkipScope) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrNotFound
	}
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return false, err
	}

	s.mu.Lock()
	streamer, running := s.streamers[channelID]
	s.mu.Unlock()
	if !running {
		return false, nil
	}

	// Read the item before cancelling: the mirror is cleared as the loop moves
	// on, and then there is nothing left to act on.
	item, _, _, playing := streamer.Now()
	if !playing {
		return false, nil
	}

	switch scope {
	case SkipKind, SkipSource:
		// "Next media type" has to mean what it says: suppressing only the one
		// source would just move to the next podcast, which is the same thing
		// SKIP does. Pass over everything of this kind so the ladder is forced
		// onto different media.
		sources, err := ListChannelSources(ctx, s.db, channelID)
		if err != nil {
			return false, err
		}
		suppressed := 0
		for _, src := range sources {
			if src.Kind == item.Kind && src.Role != RoleShow {
				s.skips.Suppress(src.ID, DefaultSkipSuppression)
				suppressed++
			}
		}
		// A channel with only one kind of media has nowhere else to go; fall
		// back to skipping just this source so the button still does
		// something rather than silently nothing.
		if suppressed == 0 && item.SourceID != "" {
			s.skips.Suppress(item.SourceID, DefaultSkipSuppression)
		}
	default:
		// Not this. Move the PROGRAMMING on, which is a different thing from
		// moving to the next item.
		//
		// Two things happen, and deliberately only two: pass over the item, and
		// step off the show for a bit so the reply is not the next episode of
		// what you just walked out of. Then the whole decision runs again from
		// the top — block, window, balance, candidates. There is no "keep the
		// next one under forty-five minutes" rule any more; a length rule
		// invented at the skip button is exactly the kind of specific patch
		// that accumulates until nobody can say why the station does anything.
		if item.ItemRef != "" {
			s.skips.SuppressRef(item.ItemRef)
		}
		if item.SourceID != "" {
			s.skips.Suppress(item.SourceID, skipSourceStepAside)
		}
	}
	return streamer.skipCurrent(), nil
}

// Previous replays the last thing that actually aired.
//
// A live channel has no back-buffer to rewind into, so "previous" means
// re-airing the previous item from the top. The play log is the only record of
// what that was, which is also why a skipped item is removed from it: you
// should not land back on something you just skipped past.
func (s *Service) Previous(ctx context.Context, channelID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrNotFound
	}
	if _, err := LoadChannel(ctx, s.db, channelID); err != nil {
		return false, err
	}
	s.mu.Lock()
	streamer, running := s.streamers[channelID]
	s.mu.Unlock()
	if !running {
		return false, nil
	}

	recent, err := RecentPlayLog(ctx, s.db, channelID, 3)
	if err != nil {
		return false, err
	}
	current, _, _, playing := streamer.Now()
	for _, entry := range recent {
		// Skip over the row for whatever is on right now.
		if playing && entry.ItemRef == current.ItemRef {
			continue
		}
		if entry.SourceID == "" {
			continue
		}
		// Ask the next decision to go back to that source, and clear any
		// suppression standing in its way — going back is an explicit
		// instruction and it outranks a skip from ten minutes ago.
		s.skips.Clear([]string{entry.SourceID})
		s.skips.PreferSource(channelID, entry.SourceID)
		return streamer.skipCurrent(), nil
	}
	return false, nil
}

// ClearSkips forgets every suppression on a channel, for an "un-skip
// everything" affordance and so deleting a source cannot leave a ghost entry.
func (s *Service) ClearSkips(ctx context.Context, channelID string) error {
	sources, err := ListChannelSources(ctx, s.db, channelID)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.ID)
	}
	s.skips.Clear(ids)
	return nil
}
