package channels

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Candidate enumeration is the change that makes everything else possible.
//
// The old engine ranked SOURCES and only asked the winner for an item, so
// duration, creator, publication date and how well something fills the space
// before the next appointment could never compete for a slot — they could only
// veto one after the fact. You cannot score what you have not enumerated. So
// the pools are unrolled into actual items first, and everything downstream is
// a question about items.
//
// Enumeration is deliberately cheap: no URL resolution, no cache lookups, no
// network. Only the item that wins gets materialised, and if materialising it
// fails the next one down is tried.

// Candidate is one thing the station could play, before anything has been
// decided about it.
type Candidate struct {
	Ref        string
	Title      string
	Artist     string
	SourceID   string
	PoolID     string
	PoolWeight float64
	Category   CategoryID
	// Creator is the person-level attribution used for separation: a host for
	// spoken word, the recording artist for music.
	Creator string
	Family  string
	// Show is the programme this came from, which survives the same show being
	// added twice — on-disk episodes and the RSS feed are two sources and one
	// show. Anything rationing how often a show comes round keys on this.
	Show string
	// Duration is zero when unknown — a file nobody has probed, or a live
	// stream that has no end.
	Duration  time.Duration
	Published time.Time
	// Owed marks something the station has an outstanding obligation for, and
	// Urgency is how badly, from the obligation queue. Zero urgency with Owed
	// false is ordinary back catalogue.
	Owed    bool
	Urgency float64
	// Credit is how much of its obligation this item has already earned, 0..1.
	// Separation is scaled by it: an airing nobody heard is not a time you
	// heard it.
	Credit float64
	Traits Traits

	source  Source
	episode *catalog.PodcastEpisode
	track   *catalog.MusicTrack
	path    string
}

// CategoryInterstitial is the bucket separator inventory airs under.
//
// Reserved, and not one of the station's programming categories: a spot or an
// ident is not something the station is programming toward, so it has no
// airtime target and is kept out of the balance. It is a real category rather
// than an empty string so the play log and the reports can show it — "where did
// those four minutes go" should have an answer.
const CategoryInterstitial CategoryID = "interstitial"

// SourceCategory is which of the station's categories a source belongs to.
//
// Read from the source's own config, falling back to the legacy role, so a
// channel that predates plan-defined categories keeps its talk/music split and
// a station that wants comedy and old-time-radio just says so.
func SourceCategory(src Source) CategoryID {
	if TraitsFor(src).Interstitial {
		return CategoryInterstitial
	}
	if category := strings.TrimSpace(stringFromConfig(src.Config, "category")); category != "" {
		return CategoryID(category)
	}
	return LegacyCategoryOf(src)
}

// newReleaseHours is how recently something must have been published to count
// as news rather than back catalogue. Per source via `newWithinHours`.
//
// Three days, not the thirty of `maxAgeDays`: a three-week-old episode is
// perfectly good listening, but it is not the thing you want the moment you
// wake up, and treating the two as equally "fresh" is what let a stale
// four-hour episode beat two overnight releases.
const newReleaseHours = 72

func freshWindowFor(src Source) time.Duration {
	return time.Duration(intFromConfig(src.Config, "newWithinHours", newReleaseHours)) * time.Hour
}

// enumerationContext is what enumeration needs to know about the world.
type enumerationContext struct {
	now         time.Time
	location    *time.Location
	day         ListeningDay
	heardInDay  map[string]int
	searchDepth int
	// owed is what the station currently owes the listener, so a candidate can
	// be marked as satisfying an obligation without asking the store per item.
	owed ObligationQueue
}

// Enumerate unrolls a block's pools into candidate items.
func (e *Engine) Enumerate(ctx context.Context, intent ProgrammingIntent, env enumerationContext) []Candidate {
	out := []Candidate{}
	seen := map[string]bool{}
	for _, ref := range intent.Pools {
		pool, ok := e.Plan.Pool(ref.Pool)
		if !ok {
			continue
		}
		for _, src := range pool.Resolve(e.Sources) {
			if !src.Enabled {
				continue
			}
			for _, candidate := range e.enumerateSource(ctx, src, env) {
				// A source reachable through two of a block's pools is one
				// candidate, not two. Pools are allowed to overlap on purpose —
				// "everything" and "just the music" are both useful groupings
				// of the same rows — and without this the overlap would quietly
				// double a source's chances.
				//
				// Keyed on the SOURCE and the ref, not the ref alone: two
				// different sources may legitimately offer the same file, and
				// they are not the same candidate — they can sit in different
				// categories, with different creators. Deduping on the ref alone
				// silently deleted every candidate from whichever source was
				// enumerated second, which reads as that source being starved.
				key := src.ID + "\x00" + candidate.Ref
				if candidate.Ref != "" {
					if seen[key] {
						continue
					}
					seen[key] = true
				}
				candidate.PoolID = pool.ID
				candidate.PoolWeight = poolWeight(ref)
				out = append(out, candidate)
			}
		}
	}
	return out
}

// PoolHasContent reports whether a pool can currently produce anything at all.
// Backs the `pool.<id>.available` condition and the "this block has run dry"
// exit, so a block does not have to be told when it is finished.
func (e *Engine) PoolHasContent(ctx context.Context, poolID string, env enumerationContext) bool {
	pool, ok := e.Plan.Pool(poolID)
	if !ok {
		return false
	}
	for _, src := range pool.Resolve(e.Sources) {
		if !src.Enabled {
			continue
		}
		if len(e.enumerateSource(ctx, src, env)) > 0 {
			return true
		}
	}
	return false
}

func (e *Engine) enumerateSource(ctx context.Context, src Source, env enumerationContext) []Candidate {
	traits := TraitsFor(src)
	base := Candidate{
		SourceID: src.ID,
		Category: SourceCategory(src),
		Creator:  CreatorOf(src),
		Family:   FamilyOf(src),
		Show:     ShowOf(src),
		Traits:   traits,
		source:   src,
	}
	if !traits.HasCreator {
		base.Creator = ""
	}

	switch src.Kind {
	case SourcePodcastSubscription:
		return e.enumeratePodcast(ctx, src, base, env)
	case SourceMusicPlaylist:
		return e.enumeratePlaylist(src, base, env)
	case SourceFilePool, SourceScheduledShow:
		return e.enumerateFiles(src, base, env)
	case SourceLiveStream:
		target := strings.TrimSpace(stringFromConfig(src.Config, "url"))
		if target == "" {
			return nil
		}
		base.Ref = "stream:" + target
		base.Title = firstNonEmpty(src.Label, "Live stream")
		return []Candidate{base}
	case SourceInternetStation:
		stationID := stringFromConfig(src.Config, "stationId")
		if stationID == "" {
			return nil
		}
		base.Ref = "station:" + stationID
		base.Title = firstNonEmpty(src.Label, "Internet station")
		return []Candidate{base}
	}
	return nil
}

func (e *Engine) enumeratePodcast(ctx context.Context, src Source, base Candidate, env enumerationContext) []Candidate {
	if e.Catalog == nil {
		return nil
	}
	freshness := e.Plan.Freshness
	podcastID := stringFromConfig(src.Config, "podcastId")
	if podcastID == "" {
		return nil
	}
	page, err := e.Catalog.EpisodesForPodcast(podcastID, catalog.PageRequest{Limit: episodePageSize})
	if err != nil || len(page.Items) == 0 {
		return nil
	}
	episodes := newestFirst(page.Items)
	if len(episodes) > env.searchDepth {
		episodes = episodes[:env.searchDepth]
	}
	freshFor := freshWindowFor(src)
	// Back catalogue has no age ceiling by default: a podcast whose last
	// episode was five years ago, or a 1955 radio serial added as a feed, is
	// something you added BECAUSE it is old. Bound it per source for daily news,
	// where a three-year-old episode really is worthless.
	var rerunCutoff time.Time
	if days := intFromConfig(src.Config, "rerunMaxAgeDays", 0); days > 0 {
		rerunCutoff = env.now.AddDate(0, 0, -days)
	}

	out := make([]Candidate, 0, len(episodes))
	for index := range episodes {
		episode := episodes[index]
		if !rerunCutoff.IsZero() && episode.PublishedAt != nil && episode.PublishedAt.Before(rerunCutoff) {
			continue
		}
		// Too short to be programming — an announcement, a trailer, a "we are
		// on break" post. Never applied to items of unknown length: unmeasured
		// is not the same as short.
		if floor := e.Plan.minItem(); floor > 0 && episode.DurationSeconds > 0 &&
			time.Duration(episode.DurationSeconds)*time.Second < floor {
			continue
		}
		// A feed that says an episode publishes tomorrow is not offering the
		// station something to play today. Rare, but it happens, and an
		// embargoed episode surfacing early is the kind of thing that only ever
		// gets noticed by the person it was embargoed from.
		if episode.PublishedAt != nil && episode.PublishedAt.After(env.now) {
			continue
		}
		candidate := base
		candidate.Ref = "episode:" + episode.ID
		candidate.Title = episode.Title
		// The show's name, falling back to the feed's own title when the source
		// row has no label — which is the common case, because nothing makes you
		// name a subscription you added by picking a podcast. Without this the
		// owed list identifies episodes by raw source id, and "which show is
		// this" becomes unanswerable from the screen that exists to answer it.
		candidate.Artist = firstNonEmpty(src.Label, episode.PodcastTitle)
		candidate.Duration = time.Duration(episode.DurationSeconds) * time.Second
		candidate.episode = &episodes[index]
		if episode.PublishedAt != nil {
			candidate.Published = *episode.PublishedAt
		}
		// Whether the station owes this is the obligation queue's answer, not a
		// calculation repeated here. An episode with no publication date can
		// never be owed — there is nothing for it to be recent relative to, and
		// the old age filter was written the other way round, waving every
		// undated row through as current.
		if obligation, ok := env.owed.Get(candidate.Ref); ok && obligation.Pending() {
			// Saving an overnight drop for the morning is the one thing the
			// queue cannot decide on its own: it is a question about NOW.
			if !holdForListeningDay(candidate.Published, env.now, env.day, freshFor) {
				candidate.Owed = true
				candidate.Urgency = obligation.Urgency(env.now, freshness)
				candidate.Credit = obligation.Credit
			}
		}
		out = append(out, candidate)
	}
	return out
}

func (e *Engine) enumeratePlaylist(src Source, base Candidate, env enumerationContext) []Candidate {
	if e.Catalog == nil {
		return nil
	}
	playlistID := stringFromConfig(src.Config, "playlistId")
	if playlistID == "" {
		return nil
	}
	// The whole playlist, every time, and deliberately not searchWindow'd.
	//
	// A shuffled source's queue IS its candidate set: "every song once before
	// any of them twice" is a claim about the playlist, and it cannot be
	// checked against two thirds of one. Truncating here is also what made the
	// tail of a playlist unplayable in the first place. The cost is a slice of
	// an in-memory projection — no files, no queries, no network — so a
	// thousand-track playlist is a thousand struct copies, not a thousand of
	// anything expensive.
	tracks := e.Catalog.MusicTracksForPlaylist(playlistID)
	out := make([]Candidate, 0, len(tracks))
	for index := range tracks {
		track := tracks[index]
		if len(track.AudioFiles) == 0 || strings.TrimSpace(track.AudioFiles[0].Path) == "" {
			continue
		}
		candidate := base
		candidate.Ref = "track:" + track.ID
		candidate.Title = track.Title
		candidate.Artist = firstNonEmpty(track.DisplayArtist, strings.Join(track.ArtistNames, ", "))
		candidate.Duration = time.Duration(track.DurationSeconds) * time.Second
		candidate.track = &tracks[index]
		// The artist is the creator here. "Two shows with the same host" and
		// "two songs by the same artist" are the same mistake, so they get the
		// same rule rather than two rules that drift apart.
		if candidate.Traits.HasCreator {
			candidate.Creator = firstNonEmpty(track.DisplayArtist, firstArtistName(track), base.Creator)
		}
		out = append(out, candidate)
	}
	return out
}

// searchWindow is which slice of a collection one decision looks at.
//
// searchDepth exists to bound what a decision costs, and taking the first N is
// the right way to spend it only when the order MEANS something. Episodes are
// sorted newest-first, so the first two hundred are the two hundred most recent
// — a real answer to "what should this show offer today".
//
// A playlist has no such ordering. Its tracks come back in playlist position,
// so tracks[:200] on a three-hundred-song playlist does not sample it, it
// deletes the last hundred songs: never enumerated, never scored, never
// rejected, absent from the record entirely. Which hundred is decided by where
// they happen to sit in the list, which is why adding a block of one artist to
// the end of a playlist makes that artist disappear from the station.
//
// So the window rotates instead of anchoring. The cost stays exactly what
// searchDepth says it is, and everything comes into view within one turn.
func searchWindow[T any](items []T, depth int, now time.Time) []T {
	if depth <= 0 || len(items) <= depth {
		return items
	}
	// One step a minute. Fast enough that a full turn takes hours rather than
	// days, slow enough that consecutive decisions see almost the same field —
	// the rotation is there to make everything reachable, not to be a second
	// source of randomness on top of the weighted pick.
	offset := int((now.Unix() / 60) % int64(len(items)))
	if offset < 0 {
		offset += len(items)
	}
	out := make([]T, 0, depth)
	for i := 0; i < depth; i++ {
		out = append(out, items[(offset+i)%len(items)])
	}
	return out
}

func firstArtistName(track catalog.MusicTrack) string {
	if len(track.ArtistNames) > 0 {
		return track.ArtistNames[0]
	}
	return ""
}

func (e *Engine) enumerateFiles(src Source, base Candidate, env enumerationContext) []Candidate {
	paths := stringSliceFromConfig(src.Config, "paths")
	if len(paths) == 0 {
		if single := stringFromConfig(src.Config, "path"); single != "" {
			paths = []string{single}
		}
	}
	if len(paths) == 0 {
		return nil
	}
	files, err := expandFilePaths(paths)
	if err != nil || len(files) == 0 {
		return nil
	}
	// Sorted for a stable order, then rotated for the same reason a playlist is:
	// a five-thousand-file spot pool would otherwise only ever play the two
	// hundred whose names sort first.
	sort.Strings(files)
	files = searchWindow(files, env.searchDepth, env.now)
	out := make([]Candidate, 0, len(files))
	for _, file := range files {
		candidate := base
		candidate.Ref = file
		candidate.Title = filepath.Base(file)
		candidate.path = file
		out = append(out, candidate)
	}
	return out
}

// Materialise turns the winning candidate into something the streamer can play.
//
// Split from enumeration because it is the expensive half: resolving a podcast
// episode consults the local cache, and doing that for two hundred candidates
// to play one of them would put the cost of a decision up by two orders of
// magnitude for no benefit.
func (e *Engine) Materialise(ctx context.Context, candidate Candidate) (PlaybackItem, error) {
	src := candidate.source
	item := PlaybackItem{
		Title:       candidate.Title,
		Artist:      candidate.Artist,
		Kind:        src.Kind,
		SourceID:    src.ID,
		SourceLabel: src.Label,
		ItemRef:     candidate.Ref,
		Category:    candidate.Category,
		Shuffled:    candidate.Traits.Shuffled,
	}
	switch src.Kind {
	case SourcePodcastSubscription:
		if candidate.episode == nil {
			return PlaybackItem{}, errors.New("candidate has no episode")
		}
		url, err := e.episodeURL(ctx, *candidate.episode)
		if err != nil {
			return PlaybackItem{}, err
		}
		item.URL = url
		item.DurationSeconds = candidate.episode.DurationSeconds
	case SourceMusicPlaylist:
		if candidate.track == nil || len(candidate.track.AudioFiles) == 0 {
			return PlaybackItem{}, errors.New("candidate has no track")
		}
		path := strings.TrimSpace(candidate.track.AudioFiles[0].Path)
		if path == "" {
			return PlaybackItem{}, errors.New("track has no playable file")
		}
		item.URL = path
		item.SourceLabel = firstNonEmpty(src.Label, "Playlist")
		item.DurationSeconds = candidate.track.DurationSeconds
	case SourceFilePool, SourceScheduledShow:
		if candidate.path == "" {
			return PlaybackItem{}, errors.New("candidate has no file")
		}
		item.URL = candidate.path
	case SourceLiveStream:
		resolved, err := e.resolveLiveStream(src)
		if err != nil {
			return PlaybackItem{}, err
		}
		resolved.Category = candidate.Category
		return resolved, nil
	case SourceInternetStation:
		resolved, err := e.resolveInternetStation(ctx, src)
		if err != nil {
			return PlaybackItem{}, err
		}
		resolved.Category = candidate.Category
		return resolved, nil
	default:
		return PlaybackItem{}, errors.New("unknown source kind " + src.Kind)
	}
	if item.URL == "" {
		return PlaybackItem{}, errors.New("candidate resolved to no url")
	}
	return item, nil
}
