package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// CatalogReader is the slice of the catalog the scheduler needs to
// resolve podcast-subscription sources. Kept as an interface so tests
// can supply a tiny stub instead of standing up the full service.
type CatalogReader interface {
	EpisodesForPodcast(podcastID string, page catalog.PageRequest) (catalog.Page[catalog.PodcastEpisode], error)
	// MusicTracksForPlaylist backs the music-playlist source kind, so a
	// channel can fall back to a playlist you already keep rather than making
	// you re-point it at a folder.
	MusicTracksForPlaylist(playlistID string) []catalog.MusicTrack
}

// EpisodeCacheLookup is the slice of internal/podcastcache.Service the
// scheduler uses to prefer a local cache path over a remote enclosure
// URL. Both LocalPath and ok=false fall back to the enclosure URL.
type EpisodeCacheLookup interface {
	Lookup(ctx context.Context, episodeID, enclosureURL string) (LocalCachedFile, bool, error)
}

// InternetStationLookup is the slice of internal/sources.Service the
// scheduler uses to resolve an internet-station source's configured
// stationId back to a playable URL plus display name.
type InternetStationLookup interface {
	GetInternetRadioStation(ctx context.Context, stationID string) (InternetStation, error)
}

// InternetStation is the minimum the scheduler needs from a sources
// row to render a live cut-in.
type InternetStation struct {
	ID        string
	Name      string
	StreamURL string
}

// LocalCachedFile mirrors the podcastcache.CachedFile fields the
// scheduler reads. Kept local so this package doesn't import a
// concrete cache type.
type LocalCachedFile struct {
	Path        string
	ContentType string
	SizeBytes   int64
}

// Dependencies bundles the readers the scheduler needs. nil values
// degrade gracefully — e.g., without a CatalogReader, podcast
// subscription sources are skipped instead of crashing the channel.
type Dependencies struct {
	DB               *sql.DB
	Catalog          CatalogReader
	Cache            EpisodeCacheLookup
	InternetStations InternetStationLookup
	// Listened reports how far listeners have already got through an
	// episode, so a podcast source airs things nobody has heard yet.
	Listened EpisodeProgressLookup
	// Skips holds sources somebody has just skipped away from.
	Skips *SkipRegistry
	// DefaultLocation is the wall clock a channel's schedule is read in when
	// the channel does not name its own. Nil means UTC.
	DefaultLocation *time.Location
	// DefaultTalkShare is the talk fraction used when deriving a plan for a
	// channel that has never been given one.
	DefaultTalkShare float64
	// Logger reports why a decision went the way it did. Nil is fine.
	Logger *log.Logger
	Now    func() time.Time

	// CommercialGap is the minimum time between separator items.
	// Defaults to 20 minutes; only applies when interstitial inventory exists.
	CommercialGap time.Duration
}

func (d Dependencies) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

// location resolves the wall clock a channel's schedule is written in.
//
// Schedule times are a bare minute-of-day, so they only mean anything relative
// to a zone. Matching them against UTC — which is what this once did — shifts
// every show by the operator's offset, and in a container, where TZ is unset and
// even the process's own clock is UTC, it can never accidentally be right.
func (d Dependencies) location(channel Channel) *time.Location {
	if zone := strings.TrimSpace(channel.Timezone); zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	if d.DefaultLocation != nil {
		return d.DefaultLocation
	}
	return time.UTC
}

func (d Dependencies) logf(format string, args ...any) {
	if d.Logger == nil {
		return
	}
	d.Logger.Printf(format, args...)
}

// commercialGap is the minimum time between separator items.
func (d Dependencies) commercialGap() time.Duration {
	if d.CommercialGap > 0 {
		return d.CommercialGap
	}
	return 20 * time.Minute
}

// talkShare is the split a derived plan starts from.
func (d Dependencies) talkShare(channel Channel) float64 {
	if channel.TalkShare > 0 && channel.TalkShare < 1 {
		return channel.TalkShare
	}
	if d.DefaultTalkShare > 0 && d.DefaultTalkShare < 1 {
		return d.DefaultTalkShare
	}
	return DefaultTalkShare
}

// DefaultTalkShare is the starting balance for a derived plan: mostly spoken
// word, with music threaded through it.
const DefaultTalkShare = 0.75

// EpisodeProgress is how far through an episode a listener already is.
type EpisodeProgress struct {
	Completed       bool
	ProgressSeconds int
}

// EpisodeProgressLookup reports playback progress for podcast episodes across
// every listener on the server.
type EpisodeProgressLookup interface {
	EpisodeProgress(ctx context.Context, episodeIDs []string) (map[string]EpisodeProgress, error)
}

// listenedFraction is how far through an episode counts as heard when it was
// never explicitly marked complete. Podcast outros and trailing ads mean people
// routinely stop before the file ends.
const listenedFraction = 0.9

// startedSeconds is the absolute fallback: this much of an episode means you
// have engaged with it, whatever its length.
//
// It is the signal that actually works. `Completed` is never set by the server
// — only a client can PATCH it, and none do — and a ratio needs a duration,
// which feed-derived episodes routinely lack (DurationSeconds = 0). Relying on
// either alone meant the filter could not return true for any real episode.
const startedSeconds = 120

func (p EpisodeProgress) listened(durationSeconds int) bool {
	if p.Completed {
		return true
	}
	if p.ProgressSeconds <= 0 {
		return false
	}
	if durationSeconds > 0 && float64(p.ProgressSeconds) >= float64(durationSeconds)*listenedFraction {
		return true
	}
	return p.ProgressSeconds >= startedSeconds
}

// Scheduler picks "what plays next" for a channel.
//
// It is a thin shell now. Everything that decides anything lives in Engine,
// which knows nothing about databases — the whole point being that the same
// code can be run forward for three days against an in-memory history without
// putting a byte on the air. This type's job is to fetch the plan, the state
// and the history, hand them over, and write back what came out.
type Scheduler struct {
	deps Dependencies
}

func NewScheduler(deps Dependencies) *Scheduler {
	return &Scheduler{deps: deps}
}

// NextItem returns the PlaybackItem the streamer should play next.
func (s *Scheduler) NextItem(ctx context.Context, channelID string) (PlaybackItem, error) {
	item, _, err := s.decide(ctx, channelID, true)
	return item, err
}

// PeekItem answers "what would play right now" without changing anything.
//
// The preemption watchdog asks this every fifteen seconds and the preview
// endpoint asks it on demand — neither is taking a turn. Committing state from
// a speculative caller is how a listener's BACK button gets eaten four times a
// minute by a watchdog.
func (s *Scheduler) PeekItem(ctx context.Context, channelID string) (PlaybackItem, error) {
	item, _, err := s.decide(ctx, channelID, false)
	return item, err
}

// Explain returns what would play right now together with the full record of
// how that answer was reached.
func (s *Scheduler) Explain(ctx context.Context, channelID string) (Decision, error) {
	_, decision, err := s.decide(ctx, channelID, false)
	return decision, err
}

func (s *Scheduler) decide(ctx context.Context, channelID string, commit bool) (PlaybackItem, Decision, error) {
	if s.deps.DB == nil {
		return PlaybackItem{}, Decision{}, errors.New("scheduler has no database")
	}
	engine, state, err := s.engineFor(ctx, channelID)
	if err != nil {
		return PlaybackItem{}, Decision{}, err
	}

	now := s.deps.now().In(engine.location())
	engine.Rand = rand.New(rand.NewSource(decisionSeed(engine.Plan, channelID, now)))

	item, decision, next, err := engine.Decide(ctx, now, state)
	if commit {
		if err := SaveProgramState(ctx, s.deps.DB, channelID, next); err != nil {
			s.deps.logf("channel %s: could not save programme state: %v", channelID, err)
		}
		if err := SaveDecision(ctx, s.deps.DB, channelID, decision); err != nil {
			s.deps.logf("channel %s: could not save the decision record: %v", channelID, err)
		}
		// BACK is spent once. Reading it without consuming it is what lets the
		// watchdog peek without stealing the listener's instruction.
		if s.deps.Skips.PreferredSource(channelID) != "" {
			s.deps.Skips.ClearPreferredSource(channelID)
		}
	}
	if err != nil {
		return PlaybackItem{}, decision, err
	}
	return item, decision, nil
}

// engineFor assembles a channel's engine from the database.
func (s *Scheduler) engineFor(ctx context.Context, channelID string) (*Engine, ProgramState, error) {
	channel, err := LoadChannel(ctx, s.deps.DB, channelID)
	if err != nil {
		return nil, ProgramState{}, err
	}
	sources, err := ListChannelSources(ctx, s.deps.DB, channelID)
	if err != nil {
		return nil, ProgramState{}, err
	}
	sources = filterEnabledSources(sources)
	if len(sources) == 0 {
		return nil, ProgramState{}, errors.New("channel has no enabled sources")
	}
	plan, err := s.PlanFor(ctx, channel, sources)
	if err != nil {
		return nil, ProgramState{}, err
	}
	state, err := LoadProgramState(ctx, s.deps.DB, channelID)
	if err != nil {
		state = ProgramState{}
	}
	return &Engine{
		Plan:        plan,
		Channel:     channel,
		Sources:     sources,
		History:     NewSQLHistory(s.deps.DB, channelID),
		Obligations: NewSQLObligations(s.deps.DB, channelID),
		Catalog:     s.deps.Catalog,
		Cache:       s.deps.Cache,
		Stations:    s.deps.InternetStations,
		Listened:    s.deps.Listened,
		Skips:       s.deps.Skips,
		Location:    s.deps.location(channel),
		Logger:      s.deps.Logger,
	}, state, nil
}

// PlanFor is the channel's stored plan, or the one its existing configuration
// already describes.
//
// A channel nobody has given a plan is not a special case anywhere in the
// engine — it just gets the plan its sources and booked slots add up to, which
// is why the rebuild needed no migration and no flag day.
func (s *Scheduler) PlanFor(ctx context.Context, channel Channel, sources []Source) (Plan, error) {
	stored, ok, err := LoadPlan(ctx, s.deps.DB, channel.ID)
	if err != nil {
		// A stored plan that no longer validates — because a later version of
		// the engine tightened a rule it was saved under — must not take the
		// station off the air. Fall back to the plan its sources describe and
		// say so loudly enough to be fixed.
		s.deps.logf("channel %s: stored plan is not usable, falling back to the derived one: %v",
			channel.ID, err)
	}
	if ok {
		return stored, nil
	}
	rules, err := ListScheduleRules(ctx, s.deps.DB, channel.ID)
	if err != nil {
		return Plan{}, err
	}
	return DerivePlan(channel, sources, rules, s.deps.talkShare(channel)), nil
}

// decisionSeed makes a decision reproducible without making it predictable.
//
// Derived from the plan's seed, the channel and the SECOND the decision is
// being made in. Two consequences, both wanted: a peek and the real pick at the
// same instant agree, so the watchdog cannot change what the listener gets by
// looking at it; and a test that fixes the clock gets the same station every
// time, which is the only way any of this is testable at all.
func decisionSeed(plan Plan, channelID string, now time.Time) int64 {
	base := plan.Seed
	if base == 0 {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(channelID))
		base = int64(hash.Sum64() & 0x7fffffffffffffff)
	}
	return base ^ now.Unix()
}

func filterEnabledSources(items []Source) []Source {
	out := make([]Source, 0, len(items))
	for _, src := range items {
		if src.Enabled {
			out = append(out, src)
		}
	}
	return out
}

// ----- resolving the items that need the outside world -----------------

func (e *Engine) episodeURL(ctx context.Context, ep catalog.PodcastEpisode) (string, error) {
	if len(ep.AudioFiles) > 0 && strings.TrimSpace(ep.AudioFiles[0].Path) != "" {
		return ep.AudioFiles[0].Path, nil
	}
	if e.Cache != nil {
		if cached, ok, err := e.Cache.Lookup(ctx, ep.ID, ep.EnclosureURL); err == nil && ok && strings.TrimSpace(cached.Path) != "" {
			return cached.Path, nil
		}
	}
	if strings.TrimSpace(ep.EnclosureURL) == "" {
		return "", errors.New("episode has no playable source")
	}
	return ep.EnclosureURL, nil
}

// resolveLiveStream returns the live URL configured for the source.
func (e *Engine) resolveLiveStream(src Source) (PlaybackItem, error) {
	target := strings.TrimSpace(stringFromConfig(src.Config, "url"))
	if target == "" {
		return PlaybackItem{}, errors.New("live-stream source missing url")
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return PlaybackItem{}, fmt.Errorf("invalid live-stream url %q", target)
	}
	return PlaybackItem{
		URL:         target,
		Title:       firstNonEmpty(src.Label, parsed.Host, "Live stream"),
		Kind:        SourceLiveStream,
		SourceID:    src.ID,
		SourceLabel: src.Label,
		ItemRef:     "stream:" + target,
		Live:        true,
	}, nil
}

// resolveInternetStation looks up an existing internet radio station by its
// catalog id, so re-pointing the station URL doesn't require editing every
// channel that uses it.
func (e *Engine) resolveInternetStation(ctx context.Context, src Source) (PlaybackItem, error) {
	if e.Stations == nil {
		return PlaybackItem{}, errors.New("internet station lookup not configured")
	}
	stationID := stringFromConfig(src.Config, "stationId")
	if stationID == "" {
		return PlaybackItem{}, errors.New("internet-station source missing stationId")
	}
	station, err := e.Stations.GetInternetRadioStation(ctx, stationID)
	if err != nil {
		return PlaybackItem{}, err
	}
	streamURL := strings.TrimSpace(station.StreamURL)
	if streamURL == "" {
		return PlaybackItem{}, errors.New("internet station has no stream url")
	}
	return PlaybackItem{
		URL:         streamURL,
		Title:       firstNonEmpty(src.Label, station.Name, "Internet station"),
		Kind:        SourceInternetStation,
		SourceID:    src.ID,
		SourceLabel: firstNonEmpty(src.Label, station.Name),
		ItemRef:     "station:" + station.ID,
		Live:        true,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ----- config helpers --------------------------------------------------

func stringFromConfig(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	switch tv := v.(type) {
	case string:
		return tv
	case fmt.Stringer:
		return tv.String()
	default:
		return fmt.Sprint(tv)
	}
}

func intFromConfig(cfg map[string]any, key string, fallback int) int {
	if cfg == nil {
		return fallback
	}
	v, ok := cfg[key]
	if !ok {
		return fallback
	}
	switch tv := v.(type) {
	case int:
		return tv
	case int64:
		return int(tv)
	case float64:
		return int(tv)
	default:
		return fallback
	}
}

func stringSliceFromConfig(cfg map[string]any, key string) []string {
	if cfg == nil {
		return nil
	}
	v, ok := cfg[key]
	if !ok {
		return nil
	}
	switch tv := v.(type) {
	case []string:
		return tv
	case []any:
		out := make([]string, 0, len(tv))
		for _, item := range tv {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// expandFilePaths takes user-supplied entries (file paths, directory
// paths, glob patterns) and returns the concrete file list. Hidden
// files are skipped. Directories are walked one level deep.
func expandFilePaths(entries []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.ContainsAny(entry, "*?[") {
			matches, err := filepath.Glob(entry)
			if err != nil {
				return nil, fmt.Errorf("glob %q: %w", entry, err)
			}
			for _, m := range matches {
				addPath(seen, &out, m)
			}
			continue
		}
		info, err := os.Stat(entry)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %q: %w", entry, err)
		}
		if info.IsDir() {
			dirEntries, err := os.ReadDir(entry)
			if err != nil {
				return nil, fmt.Errorf("read dir %q: %w", entry, err)
			}
			for _, d := range dirEntries {
				if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
					continue
				}
				addPath(seen, &out, filepath.Join(entry, d.Name()))
			}
			continue
		}
		addPath(seen, &out, entry)
	}
	return out, nil
}

func addPath(seen map[string]struct{}, out *[]string, path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if _, ok := seen[abs]; ok {
		return
	}
	seen[abs] = struct{}{}
	*out = append(*out, abs)
}

// episodePageSize is how deep to look for something to play.
//
// The catalog neither sorts nor lets us ask for "the latest", so the page has
// to be deep enough that a newly published episode is inside it before we sort.
// A shallow page on a long-running show returns only its oldest episodes.
const episodePageSize = 500

// newestFirst orders episodes by publication date, newest first.
//
// Everything about "fresh" depends on this. The catalog hands back feed order —
// oldest first for anything ingested chronologically — so without it, picking
// the first eligible episode picks the oldest one in the show's history.
func newestFirst(items []catalog.PodcastEpisode) []catalog.PodcastEpisode {
	out := append([]catalog.PodcastEpisode(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		left, right := out[i].PublishedAt, out[j].PublishedAt
		if left == nil || right == nil {
			// Undated episodes sink below dated ones rather than jumping the
			// queue on a nil comparison.
			return right == nil && left != nil
		}
		return left.After(*right)
	})
	return out
}
