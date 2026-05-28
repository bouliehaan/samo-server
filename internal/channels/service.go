package channels

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sync"
	"time"
)

// ServiceOptions wires up dependencies the channels service needs.
type ServiceOptions struct {
	DB               *sql.DB
	Catalog          CatalogReader
	Cache            EpisodeCacheLookup
	InternetStations InternetStationLookup
	FFmpegPath       string
	Logger           *log.Logger
}

// Service is the public entry point. It owns one ffmpeg streamer per
// channel and re-uses it across listeners. CRUD methods proxy to the
// store; streaming methods proxy to the per-channel streamer.
type Service struct {
	db               *sql.DB
	catalog          CatalogReader
	cache            EpisodeCacheLookup
	internetStations InternetStationLookup
	ffmpegPath       string
	logger           *log.Logger

	mu        sync.Mutex
	streamers map[string]*channelStreamer
}

func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Service{
		db:               opts.DB,
		catalog:          opts.Catalog,
		cache:            opts.Cache,
		internetStations: opts.InternetStations,
		ffmpegPath:       opts.FFmpegPath,
		logger:           logger,
		streamers:        map[string]*channelStreamer{},
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
		Now:              func() time.Time { return time.Now().UTC() },
	}
}

// ----- CRUD ------------------------------------------------------------

func (s *Service) ListChannels(ctx context.Context) ([]Channel, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("channels service not initialised")
	}
	return ListChannels(ctx, s.db)
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
	return ch, nil
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

// PreviewNext runs the scheduler once and returns what would play
// right now, without starting a real ffmpeg. Useful for UI testing
// and "is this channel even configured?" checks.
func (s *Service) PreviewNext(ctx context.Context, channelID string) (PlaybackItem, error) {
	sched := NewScheduler(s.schedDeps())
	return sched.NextItem(ctx, channelID)
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
	streamer := newChannelStreamer(channel, deps, sched, StreamerOptions{FFmpegPath: s.ffmpegPath, Logger: s.logger}, &serviceRecorder{db: s.db})
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

type serviceRecorder struct {
	db *sql.DB
}

func (r *serviceRecorder) OnPlayStart(channelID string, item PlaybackItem) (string, error) {
	if r.db == nil {
		return "", nil
	}
	return RecordPlayStart(context.Background(), r.db, channelID, item)
}

func (r *serviceRecorder) OnPlayEnd(playLogID string) {
	if r.db == nil || playLogID == "" {
		return
	}
	_ = RecordPlayEnd(context.Background(), r.db, playLogID)
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
