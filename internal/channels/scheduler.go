package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
// row to render a live cut-in. The full sources.InternetRadioStation
// has many more fields but channels only cares about the playable URL
// and the human-readable name.
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
	Now              func() time.Time

	// FillerLookahead is how far ahead the scheduler considers
	// upcoming rules when picking rotation items. Defaults to
	// 30 minutes. If the next rule is closer than the rotation
	// item's duration, the scheduler tries to pick something
	// shorter ("gap-fit") instead of overlapping the rule.
	FillerLookahead time.Duration

	// LookbackWindow is how far back the scheduler looks at the
	// play log when suppressing repeats. Defaults to 4 hours.
	LookbackWindow time.Duration
}

func (d Dependencies) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

func (d Dependencies) lookback() time.Duration {
	if d.LookbackWindow > 0 {
		return d.LookbackWindow
	}
	return 4 * time.Hour
}

func (d Dependencies) lookahead() time.Duration {
	if d.FillerLookahead > 0 {
		return d.FillerLookahead
	}
	return 30 * time.Minute
}

// Scheduler picks "what plays next" for a channel.
type Scheduler struct {
	deps Dependencies
	rng  *rand.Rand
}

func NewScheduler(deps Dependencies) *Scheduler {
	return &Scheduler{
		deps: deps,
		// Seeded per-scheduler so unit tests can control determinism
		// by re-creating their own scheduler. Real callers get fresh
		// randomness from current time.
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NextItem returns the PlaybackItem the streamer should play next for
// the channel, evaluated at `at`.
//
// Order of precedence:
//  1. Active schedule rule (highest-priority rule whose window contains `at`)
//  2. Rotation pool (sources with default_rotation = true)
//  3. Any enabled source (fallback so the channel never dead-airs)
//
// When called from a long-running streamer loop, `at` should be the
// current wall clock — the streamer re-asks after each item finishes.
func (s *Scheduler) NextItem(ctx context.Context, channelID string) (PlaybackItem, error) {
	at := s.deps.now().UTC()
	if s.deps.DB == nil {
		return PlaybackItem{}, errors.New("scheduler has no database")
	}

	sources, err := ListChannelSources(ctx, s.deps.DB, channelID)
	if err != nil {
		return PlaybackItem{}, err
	}
	sources = filterEnabledSources(sources)
	if len(sources) == 0 {
		return PlaybackItem{}, errors.New("channel has no enabled sources")
	}

	rules, err := ListScheduleRules(ctx, s.deps.DB, channelID)
	if err != nil {
		return PlaybackItem{}, err
	}

	recent, err := RecentItemRefs(ctx, s.deps.DB, channelID, s.deps.lookback())
	if err != nil {
		return PlaybackItem{}, err
	}

	// Pass 1 — scheduled rule. If one matches, we honor it regardless
	// of recent plays so live cut-ins never get blocked by suppression.
	if rule, ok := pickActiveRule(rules, at); ok {
		for _, src := range sources {
			if src.ID == rule.SourceID {
				item, err := s.resolveSource(ctx, channelID, src, recent, ruleWindowRemaining(rule, at))
				if err == nil {
					item.MaxDuration = ruleWindowRemaining(rule, at)
					item.IsRuleDriven = true
					item.RuleID = rule.ID
					return item, nil
				}
				// If the scheduled source failed to resolve (no fresh
				// episode, bad URL, etc.) fall through to rotation so
				// the channel doesn't go silent.
			}
		}
	}

	// Pass 2 — rotation. Compute how long we have until the next rule
	// starts so we can prefer items that fit.
	rotationPool := rotationCandidates(sources)
	if len(rotationPool) == 0 {
		rotationPool = sources
	}
	gap := nextRuleStartGap(rules, at, s.deps.lookahead())

	if item, err := s.pickFromRotation(ctx, channelID, rotationPool, recent, gap); err == nil {
		return item, nil
	}

	// Pass 3 — fallback. Try every source in order; pick the first
	// that resolves. Better than silence.
	for _, src := range sources {
		if item, err := s.resolveSource(ctx, channelID, src, recent, gap); err == nil {
			return item, nil
		}
	}
	return PlaybackItem{}, errors.New("no resolvable source for channel")
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

func rotationCandidates(items []Source) []Source {
	out := make([]Source, 0, len(items))
	for _, src := range items {
		if src.DefaultRotation {
			out = append(out, src)
		}
	}
	return out
}

// pickActiveRule walks rules sorted by priority desc and returns the
// first whose window contains `at` on the right weekday.
func pickActiveRule(rules []ScheduleRule, at time.Time) (ScheduleRule, bool) {
	at = at.UTC()
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
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Priority > matches[j].Priority })
	return matches[0], true
}

// ruleWindowRemaining returns the time left in `rule`'s window after
// `at`. Used to cap how long a live cut-in or scheduled show stays
// glued to the same source.
func ruleWindowRemaining(rule ScheduleRule, at time.Time) time.Duration {
	at = at.UTC()
	endMinute := rule.EndMinute
	startOfDay := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	end := startOfDay.Add(time.Duration(endMinute) * time.Minute)
	remaining := end.Sub(at)
	if remaining < 0 {
		remaining = 0
	}
	return remaining
}

// nextRuleStartGap returns the time until the next enabled schedule
// rule begins (capped at `cap`). When no rule starts within `cap`,
// returns `cap` so the caller treats the slot as "open." When a rule
// is currently active, returns 0.
func nextRuleStartGap(rules []ScheduleRule, at time.Time, cap time.Duration) time.Duration {
	if cap <= 0 {
		cap = 30 * time.Minute
	}
	at = at.UTC()
	weekday := int(at.Weekday())
	minute := at.Hour()*60 + at.Minute()
	best := cap
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if rule.WeekdayMask&(1<<weekday) == 0 {
			continue
		}
		if rule.StartMinute <= minute && rule.EndMinute > minute {
			return 0 // a rule is active right now
		}
		if rule.StartMinute <= minute {
			continue
		}
		gap := time.Duration(rule.StartMinute-minute) * time.Minute
		if gap < best {
			best = gap
		}
	}
	return best
}

// pickFromRotation does a weighted random selection across rotation
// sources, biased away from recently-played items and toward items
// that fit within `gap` when one is small.
func (s *Scheduler) pickFromRotation(ctx context.Context, channelID string, sources []Source, recent map[string]time.Time, gap time.Duration) (PlaybackItem, error) {
	if len(sources) == 0 {
		return PlaybackItem{}, errors.New("rotation pool empty")
	}
	// Shuffle so equal-weight sources rotate fairly across calls.
	indices := s.rng.Perm(len(sources))
	type candidate struct {
		item   PlaybackItem
		weight int
	}
	candidates := make([]candidate, 0, len(sources))
	for _, idx := range indices {
		src := sources[idx]
		item, err := s.resolveSource(ctx, channelID, src, recent, gap)
		if err != nil {
			continue
		}
		// If the slot is tight and this item runs past it, deweight
		// instead of dropping — better to overflow a little than
		// dead-air the channel.
		w := src.Weight
		if w <= 0 {
			w = 1
		}
		if gap > 0 && item.DurationSeconds > 0 && time.Duration(item.DurationSeconds)*time.Second > gap {
			w = w / 4
			if w < 1 {
				w = 1
			}
		}
		candidates = append(candidates, candidate{item: item, weight: w})
	}
	if len(candidates) == 0 {
		return PlaybackItem{}, errors.New("rotation pool yielded no playable items")
	}
	total := 0
	for _, c := range candidates {
		total += c.weight
	}
	roll := s.rng.Intn(total)
	for _, c := range candidates {
		if roll < c.weight {
			return c.item, nil
		}
		roll -= c.weight
	}
	return candidates[len(candidates)-1].item, nil
}

// resolveSource turns a Source row into a concrete PlaybackItem the
// streamer can play. Unknown kinds return an error so the caller can
// fall through to another candidate.
func (s *Scheduler) resolveSource(ctx context.Context, channelID string, src Source, recent map[string]time.Time, gap time.Duration) (PlaybackItem, error) {
	switch src.Kind {
	case SourceFilePool, SourceScheduledShow:
		return s.resolveFilePool(src, recent)
	case SourcePodcastSubscription:
		return s.resolvePodcastSubscription(ctx, src, recent)
	case SourceLiveStream:
		return s.resolveLiveStream(src, gap)
	case SourceInternetStation:
		return s.resolveInternetStation(ctx, src)
	default:
		return PlaybackItem{}, fmt.Errorf("unknown source kind %q", src.Kind)
	}
}

// resolveInternetStation looks up an existing internet radio station
// by its catalog id and returns it as a live cut-in. Unlike
// resolveLiveStream (which takes a raw URL in config), this kind
// inherits the user-managed station metadata (name, url) from the
// sources service so re-pointing the station URL doesn't require
// editing every channel that uses it.
func (s *Scheduler) resolveInternetStation(ctx context.Context, src Source) (PlaybackItem, error) {
	if s.deps.InternetStations == nil {
		return PlaybackItem{}, errors.New("internet station lookup not configured")
	}
	stationID := stringFromConfig(src.Config, "stationId")
	if stationID == "" {
		return PlaybackItem{}, errors.New("internet-station source missing stationId")
	}
	station, err := s.deps.InternetStations.GetInternetRadioStation(ctx, stationID)
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

// resolveFilePool reads `config.paths` (a slice of absolute file paths,
// directories, or globs), expands them, filters out recently played
// items, and returns one at random.
func (s *Scheduler) resolveFilePool(src Source, recent map[string]time.Time) (PlaybackItem, error) {
	paths := stringSliceFromConfig(src.Config, "paths")
	if len(paths) == 0 {
		// Single-path convenience.
		if p := stringFromConfig(src.Config, "path"); p != "" {
			paths = []string{p}
		}
	}
	if len(paths) == 0 {
		return PlaybackItem{}, errors.New("file-pool source has no paths configured")
	}
	files, err := expandFilePaths(paths)
	if err != nil {
		return PlaybackItem{}, err
	}
	if len(files) == 0 {
		return PlaybackItem{}, errors.New("file-pool source matched no files")
	}
	// Prefer files not played in the lookback window. If everything
	// has been played, fall back to the oldest-played file so the
	// loop is at least round-robin-ish.
	fresh := make([]string, 0, len(files))
	for _, f := range files {
		if _, played := recent[f]; !played {
			fresh = append(fresh, f)
		}
	}
	pool := fresh
	if len(pool) == 0 {
		pool = files
		sort.SliceStable(pool, func(i, j int) bool { return recent[pool[i]].Before(recent[pool[j]]) })
	}
	pick := pool[s.rng.Intn(len(pool))]
	title := filepath.Base(pick)
	return PlaybackItem{
		URL:         pick,
		Title:       title,
		Kind:        src.Kind,
		SourceID:    src.ID,
		SourceLabel: src.Label,
		ItemRef:     pick,
		// Duration unknown without probing; let the streamer measure as
		// ffmpeg consumes the file.
	}, nil
}

// resolvePodcastSubscription pulls the newest episode (or one of the
// newest N) for the configured podcast id. Honours maxAgeDays to
// avoid resurfacing ancient back-catalog items the user probably
// already heard.
func (s *Scheduler) resolvePodcastSubscription(ctx context.Context, src Source, recent map[string]time.Time) (PlaybackItem, error) {
	if s.deps.Catalog == nil {
		return PlaybackItem{}, errors.New("catalog reader not configured")
	}
	podcastID := stringFromConfig(src.Config, "podcastId")
	if podcastID == "" {
		return PlaybackItem{}, errors.New("podcast-subscription source missing podcastId")
	}
	maxAgeDays := intFromConfig(src.Config, "maxAgeDays", 30)
	page, err := s.deps.Catalog.EpisodesForPodcast(podcastID, catalog.PageRequest{Limit: 25})
	if err != nil {
		return PlaybackItem{}, err
	}
	if len(page.Items) == 0 {
		return PlaybackItem{}, errors.New("podcast has no episodes")
	}
	cutoff := s.deps.now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	for _, ep := range page.Items {
		// Skip episodes older than maxAgeDays (when we know the date).
		if ep.PublishedAt != nil && ep.PublishedAt.Before(cutoff) {
			continue
		}
		ref := "episode:" + ep.ID
		if _, played := recent[ref]; played {
			continue
		}
		url, err := s.episodeURL(ctx, ep)
		if err != nil {
			continue
		}
		return PlaybackItem{
			URL:             url,
			Title:           ep.Title,
			Artist:          src.Label,
			Kind:            src.Kind,
			SourceID:        src.ID,
			SourceLabel:     src.Label,
			ItemRef:         ref,
			DurationSeconds: ep.DurationSeconds,
		}, nil
	}
	return PlaybackItem{}, errors.New("no fresh, unplayed episodes for subscription")
}

func (s *Scheduler) episodeURL(ctx context.Context, ep catalog.PodcastEpisode) (string, error) {
	if len(ep.AudioFiles) > 0 && strings.TrimSpace(ep.AudioFiles[0].Path) != "" {
		return ep.AudioFiles[0].Path, nil
	}
	if s.deps.Cache != nil {
		if cached, ok, err := s.deps.Cache.Lookup(ctx, ep.ID, ep.EnclosureURL); err == nil && ok && strings.TrimSpace(cached.Path) != "" {
			return cached.Path, nil
		}
	}
	if strings.TrimSpace(ep.EnclosureURL) == "" {
		return "", errors.New("episode has no playable source")
	}
	return ep.EnclosureURL, nil
}

// resolveLiveStream returns the live URL configured for the source.
// MaxDuration is the rule window (set by NextItem); here we just
// return the URL and let the streamer cap the lifetime.
func (s *Scheduler) resolveLiveStream(src Source, gap time.Duration) (PlaybackItem, error) {
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
// files are skipped. Directories are walked one level deep — deeper
// nesting is rare for commercial/filler pools and keeps surprises out.
func expandFilePaths(entries []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Glob support: anything with a wildcard goes through Glob.
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
