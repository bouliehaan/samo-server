package channels

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// These are the scheduling regressions, and almost none of them need a
// database any more.
//
// That is the point of putting history behind an interface: every rule the
// engine has is a rule about time, and the previous suite could only state "the
// station played talk all night" by seeding play-log rows into Postgres. Here it
// is three lines, so the awkward cases — the ones that actually happened — are
// cheap enough to write down.

// ---- harness -----------------------------------------------------------

type station struct {
	t       *testing.T
	engine  *Engine
	history *MemoryHistory
	state   ProgramState
	now     time.Time
}

func newStation(t *testing.T, plan Plan, sources []Source, cat CatalogReader, now time.Time) *station {
	t.Helper()
	if err := plan.Validate(); err != nil {
		t.Fatalf("test plan is not valid: %v", err)
	}
	history := NewMemoryHistory()
	return &station{
		t:       t,
		history: history,
		now:     now,
		engine: &Engine{
			Plan:        plan,
			Channel:     Channel{ID: "ch1", DayStartMinute: 8 * 60, DayEndMinute: 23 * 60},
			Sources:     sources,
			History:     history,
			Obligations: NewMemoryObligations(),
			Catalog:     cat,
			Skips:       NewSkipRegistry(func() time.Time { return now }),
			Location:    time.UTC,
			Rand:        rand.New(rand.NewSource(1)),
		},
	}
}

// decide asks what plays next without advancing anything.
func (s *station) decide() (PlaybackItem, Decision) {
	s.t.Helper()
	s.engine.Rand = rand.New(rand.NewSource(1))
	item, decision, _, err := s.engine.Decide(context.Background(), s.now, s.state)
	if err != nil {
		s.t.Fatalf("decide at %s: %v (%s)", s.now.Format("15:04"), err, decision.Error)
	}
	return item, decision
}

// tryDecide is decide() for the cases where failing is the expected answer.
func (s *station) tryDecide() (PlaybackItem, Decision, error) {
	s.t.Helper()
	s.engine.Rand = rand.New(rand.NewSource(1))
	item, decision, next, err := s.engine.Decide(context.Background(), s.now, s.state)
	s.state = next
	return item, decision, err
}

// play advances the station by one item, recording it exactly as the streamer
// would.
func (s *station) play() PlaybackItem {
	item, _ := s.step()
	return item
}

// step is play(), with the reasoning. Anything asserting on what actually went
// out must use this rather than decide() — decide() answers the question
// without advancing, so its choice and the next played item are two different
// rolls.
func (s *station) step() (PlaybackItem, Decision) {
	s.t.Helper()
	s.engine.Rand = rand.New(rand.NewSource(s.now.Unix()))
	item, decision, next, err := s.engine.Decide(context.Background(), s.now, s.state)
	if err != nil {
		s.t.Fatalf("play at %s: %v", s.now.Format("15:04"), err)
	}
	length := simItemLength(item)
	s.history.Record(MemoryPlay{
		SourceID: item.SourceID, ItemRef: item.ItemRef, Artist: item.Artist,
		Category: item.Category, StartedAt: s.now, EndedAt: s.now.Add(length),
		DurationSeconds: int(length / time.Second),
	})
	// Credit obligations exactly as the streamer does, or a test station keeps
	// surfacing the same new episode for ever.
	if item.ItemRef != "" && item.Exposure > 0 {
		completed := item.DurationSeconds <= 0 || length >= time.Duration(item.DurationSeconds)*time.Second
		if credit := item.Exposure * playedFraction(item, length, completed); credit > 0 {
			if err := s.engine.Obligations.Credit(context.Background(), item.ItemRef, credit, s.now.Add(length)); err != nil {
				s.t.Fatalf("credit: %v", err)
			}
		}
	}
	s.state = next
	s.now = s.now.Add(length)
	return item, decision
}

// env is the enumeration context with the obligation queue filled in, which is
// what marks a candidate as owed.
func (s *station) env() enumerationContext {
	ctx := context.Background()
	env := s.engine.enumerationEnv(ctx, s.now, time.UTC)
	env.owed = s.engine.refreshObligations(ctx, s.now, env)
	return env
}

// candidates is everything the default block could play right now.
func (s *station) candidates() []Candidate {
	env := s.env()
	return s.engine.Enumerate(context.Background(),
		ProgrammingIntent{Pools: s.engine.Plan.DefaultBlock().Pools}, env)
}

// aired fabricates history: "this category was on air for this long, ending
// now".
func (s *station) aired(sourceID string, category CategoryID, length time.Duration) {
	s.history.Record(MemoryPlay{
		SourceID:        sourceID,
		ItemRef:         "past:" + sourceID + ":" + s.now.Format("150405"),
		Category:        category,
		StartedAt:       s.now.Add(-length),
		EndedAt:         s.now,
		DurationSeconds: int(length / time.Second),
	})
}

// ---- fixtures ----------------------------------------------------------

func podcastSource(id, label, podcastID string) Source {
	return Source{
		ID: id, ChannelID: "ch1", Kind: SourcePodcastSubscription, Label: label,
		Config: map[string]any{"podcastId": podcastID}, Enabled: true, Weight: 1, Role: RoleTalk,
	}
}

func musicSource(id, label, playlistID string) Source {
	return Source{
		ID: id, ChannelID: "ch1", Kind: SourceMusicPlaylist, Label: label,
		Config: map[string]any{"playlistId": playlistID}, Enabled: true, Weight: 1, Role: RoleMusic,
	}
}

func episode(id, title string, published time.Time, minutes int) catalog.PodcastEpisode {
	at := published
	return catalog.PodcastEpisode{
		ID: id, Title: title, PublishedAt: &at, DurationSeconds: minutes * 60,
		AudioFiles: []catalog.AudioFile{{Path: "/audio/" + id + ".mp3"}},
	}
}

func track(id, title, artist string, seconds int) catalog.MusicTrack {
	return catalog.MusicTrack{
		ID: id, Title: title, DisplayArtist: artist, DurationSeconds: seconds,
		AudioFiles: []catalog.AudioFile{{Path: "/music/" + id + ".flac"}},
	}
}

// twoCategoryPlan is the shape a channel gets when nobody has written a plan:
// two categories, one pool each, one always-on block.
func twoCategoryPlan(talkShare float64) Plan {
	return Plan{
		Version: PlanVersion,
		Categories: []CategoryDef{
			{ID: "talk", Label: "Talk", Target: talkShare},
			{ID: "music", Label: "Music", Target: 1 - talkShare},
		},
		Pools: []Pool{
			{ID: "talk", SourceIDs: []string{"pod1", "pod2"}},
			{ID: "music", SourceIDs: []string{"mus1"}},
		},
		Blocks: []Block{{
			ID: "general", Label: "General rotation", Default: true,
			Pools: []PoolRef{{Pool: "talk", Weight: 1}, {Pool: "music", Weight: 1}},
		}},
	}
}

func twoPodcastsAndMusic() ([]Source, *stubCatalog, time.Time) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday morning
	sources := []Source{
		podcastSource("pod1", "Morning Talk", "p1"),
		podcastSource("pod2", "Afternoon Talk", "p2"),
		musicSource("mus1", "House Playlist", "pl1"),
	}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("e1", "Talk one", now.Add(-30*24*time.Hour), 40)},
			"p2": {episode("e2", "Talk two", now.Add(-31*24*time.Hour), 40)},
		},
		playlists: map[string][]catalog.MusicTrack{
			"pl1": {
				track("t1", "Song one", "Artist A", 210),
				track("t2", "Song two", "Artist B", 240),
				track("t3", "Song three", "Artist C", 200),
			},
		},
	}
	return sources, cat, now
}

// ---- the balance -------------------------------------------------------

// The night of 2026-08-09, in one test: eight hours of spoken word, then the
// station must go to music. Under the old per-source deficit comparison it went
// to more talk, every time, for fifteen hours.
func TestAfterALongTalkBlockTheStationPlaysMusic(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
	s.aired("pod1", "talk", 8*time.Hour)

	item, decision := s.decide()
	if item.Category != "music" {
		t.Fatalf("after 8h of talk the station played %s (%q)\n%s",
			item.Category, item.Title, decision.Explain())
	}
}

// The comparison that produced the marathon: with several podcasts and one
// playlist, every individual podcast is still behind ITS slice while talk as a
// whole is hours over. Category first, source second is the fix, and it has to
// stay fixed.
func TestCategoryBalanceBeatsPerSourceDeficit(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
	// Three and a half hours of talk split between two shows, half an hour of
	// music. Each podcast is behind its own 37.5% slice — so ranking sources
	// globally picks talk again. Talk as a whole is at 87.5% against a 75%
	// target, so asking the category first picks music.
	s.aired("pod1", "talk", 105*time.Minute)
	s.aired("pod2", "talk", 105*time.Minute)
	s.aired("mus1", "music", 30*time.Minute)

	scoring := s.engine.scoreEnv(context.Background(), s.now, ProgrammingIntent{
		Targets: map[CategoryID]float64{"talk": 0.75, "music": 0.25},
	}, nil, nil)
	talk := scoring.categoryDeficit("talk")
	music := scoring.categoryDeficit("music")
	if talk >= music {
		t.Fatalf("talk deficit %.3f should be below music's %.3f after 3h talk / 1h music", talk, music)
	}
}

// A category nobody has content for hands its share over rather than leaving
// part of the schedule permanently unspendable.
func TestAbsentCategoryHandsItsShareOver(t *testing.T) {
	plan := twoCategoryPlan(0.75)
	targets := plan.CategoryTargets(plan.Blocks[0], map[CategoryID]bool{"talk": true})
	if got := targets["talk"]; got != 1 {
		t.Fatalf("with no music available talk should target everything, got %.2f", got)
	}
	if _, ok := targets["music"]; ok {
		t.Fatalf("music should not be in the targets when nothing can serve it")
	}
}

// Weight splits a category's share between its own sources and never reaches
// across categories.
func TestWeightSplitsWithinCategoryOnly(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	sources[0].Weight = 3 // pod1 gets three times pod2's slice of TALK
	s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)

	shares := s.engine.sourceShares(ProgrammingIntent{
		Pools:   s.engine.Plan.Blocks[0].Pools,
		Targets: map[CategoryID]float64{"talk": 0.75, "music": 0.25},
	}, nil)
	if got, want := shares["pod1"], 0.5625; got < want-0.001 || got > want+0.001 {
		t.Fatalf("pod1 share = %.4f, want %.4f (3/4 of the 75%% talk share)", got, want)
	}
	if got, want := shares["mus1"], 0.25; got != want {
		t.Fatalf("music share = %.4f, want %.4f — weight must not cross categories", got, want)
	}
}

// ---- length, and the block limit that replaced the governor -------------

// A six-hour episode may not arrive unannounced as "what's on next" when the
// block says how long it will commit to one category. The rule is now the
// station owner's, expressed on the block, rather than a constant in the engine.
func TestOversizedItemIsNotStartedUnderABlockLimit(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := twoCategoryPlan(0.75)
	plan.Blocks[0].Limits = BlockLimits{MaxUnbroken: []CategoryLimit{{
		Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m",
	}}}
	sources := []Source{
		podcastSource("pod1", "Huge", "p1"),
		podcastSource("pod2", "Normal", "p2"),
		musicSource("mus1", "House", "pl1"),
	}
	// A realistic back catalogue: the huge episode is one item among many, and
	// it must never be the answer while ordinary programming exists.
	archive := []catalog.PodcastEpisode{episode("huge", "Six hours of history", now.Add(-400*24*time.Hour), 360)}
	for i := 0; i < 8; i++ {
		archive = append(archive, episode(
			"h"+string(rune('a'+i)), "History short "+string(rune('A'+i)),
			now.Add(-time.Duration(300+i)*24*time.Hour), 45))
	}
	normal := []catalog.PodcastEpisode{}
	for i := 0; i < 8; i++ {
		normal = append(normal, episode(
			"n"+string(rune('a'+i)), "Normal "+string(rune('A'+i)),
			now.Add(-time.Duration(40+i)*24*time.Hour), 25))
	}
	songs := []catalog.MusicTrack{}
	for i := 0; i < 12; i++ {
		songs = append(songs, track(
			"t"+string(rune('a'+i)), "Song "+string(rune('A'+i)),
			"Artist "+string(rune('A'+i)), 200))
	}
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"p1": archive, "p2": normal},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}
	s := newStation(t, plan, sources, cat, now)

	for i := 0; i < 20; i++ {
		item, decision := s.decide()
		if item.ItemRef == "episode:huge" {
			t.Fatalf("pick %d started a six-hour episode from ordinary rotation\n%s", i, decision.Explain())
		}
		s.play()
	}
}

// And the reason is recorded, so "why will it not play my long episode" has an
// answer rather than a shrug.
func TestTheLengthRuleSaysWhyInTheRecord(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := twoCategoryPlan(0.75)
	plan.Pools[0].SourceIDs = []string{"pod1", "pod2"}
	plan.Blocks[0].Limits = BlockLimits{MaxUnbroken: []CategoryLimit{{
		Category: "talk", Max: "90m", ResetAfter: "15m", MinItem: "20m",
	}}}
	cat := &stubCatalog{
		episodes: map[string][]catalog.PodcastEpisode{
			"p1": {episode("huge", "Six hours of history", now.Add(-400*24*time.Hour), 360)},
			"p2": {episode("ok", "Twenty five minutes", now.Add(-40*24*time.Hour), 25)},
		},
		playlists: map[string][]catalog.MusicTrack{"pl1": {track("t1", "Song", "Artist A", 200)}},
	}
	s := newStation(t, plan, []Source{
		podcastSource("pod1", "History", "p1"),
		podcastSource("pod2", "Normal", "p2"),
		musicSource("mus1", "House", "pl1"),
	}, cat, now)

	_, decision := s.decide()
	for _, rejection := range decision.Rejected {
		if rejection.Ref == "episode:huge" {
			if rejection.Rule != "itemFitsRun" {
				t.Fatalf("the huge episode was ruled out by %q, expected the length rule", rejection.Rule)
			}
			return
		}
	}
	t.Fatalf("the record should say why the six-hour episode was not played:\n%s", decision.Explain())
}

// The limit is a run, not an item count: ten five-minute news bulletins and one
// five-hour podcast are both "ten items" and are not remotely the same amount
// of somebody talking.
func TestCategoryRunMeasuresAirtimeNotItems(t *testing.T) {
	tail := []PlayTailEntry{
		{Category: "talk", Aired: 40 * time.Minute},
		{Category: "talk", Aired: 50 * time.Minute},
		{Category: "music", Aired: 3 * time.Minute},
		{Category: "talk", Aired: 60 * time.Minute},
	}
	// A three-minute interlude does not break a run when the reset is 15m.
	if run := CategoryRun(tail, "talk", 15*time.Minute, time.Time{}); run != 150*time.Minute {
		t.Fatalf("run = %s, want 2h30m — a short interlude must not clear the run", run)
	}
	// With no reset configured, the first other-category item ends the run.
	if run := CategoryRun(tail, "talk", 0, time.Time{}); run != 90*time.Minute {
		t.Fatalf("run = %s, want 1h30m with no reset window", run)
	}
}

// A continuous source has no length of its own, so the ceiling has to be
// applied to how long the station STAYS on it. Without this a station picked
// with ten minutes of run left settles in for its full hour.
func TestLiveSourceIsCappedByTheRemainingRun(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"live1"}}},
		Blocks: []Block{{
			ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}},
			Limits: BlockLimits{MaxUnbroken: []CategoryLimit{{Category: "talk", Max: "90m", MinItem: "20m"}}},
		}},
	}
	sources := []Source{{
		ID: "live1", ChannelID: "ch1", Kind: SourceLiveStream, Label: "News Radio",
		Config: map[string]any{"url": "http://example.test/stream"}, Enabled: true, Role: RoleTalk,
	}}
	s := newStation(t, plan, sources, &stubCatalog{}, now)
	// The station has been in this block for the whole run. A limit belongs to
	// a block, so the run it measures is the run inside that block — a station
	// that only just entered has no run yet, however much talk preceded it.
	s.state = ProgramState{BlockID: "general", EnteredAt: now.Add(-2 * time.Hour)}
	s.aired("live1", "talk", 70*time.Minute)

	item, decision := s.decide()
	if !item.Live {
		t.Fatalf("expected the live source, got %q", item.Title)
	}
	if item.MaxDuration > 25*time.Minute {
		t.Fatalf("live item capped at %s; only ~20m of the run was left\n%s",
			item.MaxDuration, decision.Explain())
	}
}

// ---- freshness ---------------------------------------------------------

// An episode that dropped at 04:00 and aired at 04:15 has not reached anybody.
// It must still be new at breakfast.
func TestOvernightAiringDoesNotSpendANewEpisode(t *testing.T) {
	published := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	morning := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{podcastSource("pod1", "Overnight Drop", "p1"), musicSource("mus1", "House", "pl1")}
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"p1": {episode("fresh", "Today's episode", published, 30)}},
		playlists: map[string][]catalog.MusicTrack{"pl1": {track("t1", "Song", "Artist A", 200)}},
	}
	plan := twoCategoryPlan(0.75)
	plan.Pools[0].SourceIDs = []string{"pod1"}
	s := newStation(t, plan, sources, cat, morning)

	// It went out at 03:15, in a part of the day worth nothing — so it earned
	// no credit and the station still owes it.
	night := time.Date(2026, 8, 10, 3, 15, 0, 0, time.UTC)
	exposure := s.engine.Plan.ExposureFor(plan.Blocks[0], night, s.engine.listeningDay())
	if exposure != 0 {
		t.Fatalf("03:15 is outside an 08:00–23:00 listening day; exposure should be 0, got %v", exposure)
	}
	s.history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:fresh", Category: "talk",
		StartedAt: night, EndedAt: night.Add(30 * time.Minute),
	})

	for _, candidate := range s.candidates() {
		if candidate.Ref == "episode:fresh" {
			if !candidate.Owed {
				t.Fatalf("the overnight airing spent the episode's newness; it should still be owed at 09:00")
			}
			return
		}
	}
	t.Fatalf("the episode was not even enumerated")
}

// The other half: an airing where exposure counts DOES settle it.
func TestAnAiringThatCountsSettlesTheObligation(t *testing.T) {
	published := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	later := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	sources := []Source{podcastSource("pod1", "Overnight Drop", "p1")}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("fresh", "Today's episode", published, 30)},
	}}
	plan := twoCategoryPlan(0.75)
	plan.Pools[0].SourceIDs = []string{"pod1"}
	plan.Pools[1].SourceIDs = nil
	s := newStation(t, plan, sources, cat, later)

	// Notice it, then credit a full airing in a block that counts.
	s.env()
	if err := s.engine.Obligations.Credit(context.Background(), "episode:fresh", 1.0, later); err != nil {
		t.Fatalf("credit: %v", err)
	}
	for _, candidate := range s.candidates() {
		if candidate.Ref == "episode:fresh" && candidate.Owed {
			t.Fatalf("a full airing where exposure counts should have settled the obligation")
		}
	}
}

// And the case that used to burn an episode outright: cut off after five
// minutes of forty-five, it is mostly still owed.
func TestAPartialAiringLeavesItMostlyOwed(t *testing.T) {
	published := time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC)
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	sources := []Source{podcastSource("pod1", "Show", "p1")}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("fresh", "Today's episode", published, 45)},
	}}
	plan := twoCategoryPlan(1)
	plan.Categories = []CategoryDef{{ID: "talk", Target: 1}}
	plan.Pools = []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}}
	plan.Blocks[0].Pools = []PoolRef{{Pool: "talk"}}
	s := newStation(t, plan, sources, cat, now)
	s.env()

	item := PlaybackItem{ItemRef: "episode:fresh", DurationSeconds: 45 * 60, Exposure: 1}
	credit := item.Exposure * playedFraction(item, 5*time.Minute, false)
	if credit < 0.10 || credit > 0.12 {
		t.Fatalf("five of forty-five minutes should be about 0.11 credit, got %.3f", credit)
	}
	if err := s.engine.Obligations.Credit(context.Background(), "episode:fresh", credit, now); err != nil {
		t.Fatalf("credit: %v", err)
	}

	found := false
	for _, candidate := range s.candidates() {
		if candidate.Ref == "episode:fresh" {
			found = candidate.Owed
		}
	}
	if !found {
		t.Fatalf("five minutes of a forty-five minute episode must not settle it — that is the bug that burned episodes")
	}
}

// An episode with no publication date can never be shown to be recent. The old
// filter was written the other way round and waved every undated row through as
// current — which is how a years-old episode arrives labelled as today's.
func TestUndatedEpisodesAreNeverOwed(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	sources := []Source{podcastSource("pod1", "Archive", "p1")}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"p1": {{
		ID: "undated", Title: "No date on this one",
		AudioFiles: []catalog.AudioFile{{Path: "/audio/undated.mp3"}},
	}}}}
	plan := twoCategoryPlan(1)
	plan.Categories = []CategoryDef{{ID: "talk", Target: 1}}
	plan.Pools = []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}}
	plan.Blocks[0].Pools = []PoolRef{{Pool: "talk"}}
	s := newStation(t, plan, sources, cat, now)

	candidates := s.candidates()
	if len(candidates) != 1 {
		t.Fatalf("expected the undated episode to be a candidate, got %d", len(candidates))
	}
	if candidates[0].Owed {
		t.Fatalf("an undated episode is back catalogue, never owed")
	}
}

// A new release is held for the morning rather than spent on an empty room —
// but only while there is a morning left to save it for.
func TestNewReleaseIsHeldUntilTheListeningDay(t *testing.T) {
	day := ListeningDay{StartMinute: 8 * 60, EndMinute: 23 * 60}
	published := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	night := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)

	if !holdForListeningDay(published, night, day, 72*time.Hour) {
		t.Fatalf("a 03:00 drop with 72h of freshness should wait for 08:00")
	}
	// With only two hours of freshness left there is no morning to save it for.
	if holdForListeningDay(published, night, day, 2*time.Hour) {
		t.Fatalf("an episode that expires before the day starts should air now")
	}
}

// ---- separation --------------------------------------------------------

// Two shows hosted by the same person, back to back, is not variety. A naive
// source rule thinks it is.
func TestCreatorSeparationCatchesSharedHosts(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	shared := podcastSource("pod2", "Second Show", "p2")
	shared.Config["creator"] = "Person X"
	first := podcastSource("pod1", "First Show", "p1")
	first.Config["creator"] = "Person X"
	third := podcastSource("pod3", "Third Show", "p3")
	third.Config["creator"] = "Person Y"

	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"pod1", "pod2", "pod3"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("e1", "One", now.Add(-40*24*time.Hour), 30)},
		"p2": {episode("e2", "Two", now.Add(-41*24*time.Hour), 30)},
		"p3": {episode("e3", "Three", now.Add(-42*24*time.Hour), 30)},
	}}
	s := newStation(t, plan, []Source{first, shared, third}, cat, now)

	// Person X was on ten minutes ago, from the OTHER show.
	s.history.Record(MemoryPlay{
		SourceID: "pod1", ItemRef: "episode:e1", Category: "talk",
		StartedAt: now.Add(-10 * time.Minute), EndedAt: now,
	})

	item, decision := s.decide()
	if item.SourceID == "pod2" {
		t.Fatalf("played a second Person X show ten minutes after the first\n%s", decision.Explain())
	}
	if item.SourceID != "pod3" {
		t.Fatalf("expected the Person Y show, got %q", item.Title)
	}
}

// Source separation must NOT apply to a container of many artists, or two songs
// in a row become impossible — which is most of what a radio station does.
func TestSourceSeparationDoesNotBlockAMusicSet(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "music", Target: 1}},
		Pools:      []Pool{{ID: "music", SourceIDs: []string{"mus1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "music"}}}},
	}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": {
		track("t1", "One", "Artist A", 200),
		track("t2", "Two", "Artist B", 200),
		track("t3", "Three", "Artist C", 200),
	}}}
	s := newStation(t, plan, []Source{musicSource("mus1", "House", "pl1")}, cat, now)

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		item := s.play()
		if item.SourceID != "mus1" {
			t.Fatalf("expected consecutive tracks from the playlist, got %q", item.Title)
		}
		seen[item.ItemRef] = true
	}
	if len(seen) != 3 {
		t.Fatalf("a music set should move through the playlist; played %d distinct tracks", len(seen))
	}
}

// The same artist twice in a row is the same mistake as the same host twice in
// a row, and gets the same rule.
func TestArtistSeparationInsideAPlaylist(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "music", Target: 1}},
		Pools:      []Pool{{ID: "music", SourceIDs: []string{"mus1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "music"}}}},
	}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": {
		track("t1", "A one", "Artist A", 200),
		track("t2", "A two", "Artist A", 200),
		track("t3", "B one", "Artist B", 200),
	}}}
	s := newStation(t, plan, []Source{musicSource("mus1", "House", "pl1")}, cat, now)
	s.history.Record(MemoryPlay{
		SourceID: "mus1", ItemRef: "track:t1", Artist: "Artist A", Category: "music",
		StartedAt: now.Add(-3 * time.Minute), EndedAt: now,
	})

	item, decision := s.decide()
	if item.Artist == "Artist A" {
		t.Fatalf("played Artist A twice in a row\n%s", decision.Explain())
	}
}

// ---- run continuity ----------------------------------------------------

// A station plays a SET. Re-deciding after every three-minute track gives you a
// song, an episode, a song — each choice locally reasonable, the sequence
// deranged.
func TestMinimumRunKeepsAMusicSetTogether(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	plan := twoCategoryPlan(0.75)
	plan.Blocks[0].Limits = BlockLimits{MinUnbroken: []CategoryMinRun{{
		Category: "music", Min: "20m", ResetAfter: "1m",
	}}}
	cat.playlists["pl1"] = []catalog.MusicTrack{
		track("t1", "One", "Artist A", 200),
		track("t2", "Two", "Artist B", 200),
		track("t3", "Three", "Artist C", 200),
		track("t4", "Four", "Artist D", 200),
	}
	s := newStation(t, plan, sources, cat, now)
	// Deep in talk surplus, so the balance alone would send us straight back to
	// talk after one song.
	s.aired("pod1", "talk", 4*time.Hour)

	first := s.play()
	if first.Category != "music" {
		t.Fatalf("expected music after a long talk block, got %s", first.Category)
	}
	second := s.play()
	if second.Category != "music" {
		t.Fatalf("a music set ended after one track — that is song, episode, song")
	}
}

// ---- the appointment boundary ------------------------------------------

// If a show starts in thirty minutes, a ninety-minute episode is not a
// candidate. The old engine started it anyway and had it cut off mid-sentence.
func TestNothingIsStartedThatCannotFinishBeforeAnAnchor(t *testing.T) {
	now := time.Date(2026, 8, 10, 17, 30, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools: []Pool{
			{ID: "rotation", SourceIDs: []string{"pod1"}},
			{ID: "show", SourceIDs: []string{"pod2"}},
		},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "rotation"}}},
			{
				ID: "evening", Label: "Evening Show",
				Enter: BlockEntry{At: "18:00", Days: "*", Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: "19:00"},
				Pools: []PoolRef{{Pool: "show"}},
			},
		},
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {
			episode("long", "Ninety minutes", now.Add(-40*24*time.Hour), 90),
			episode("short", "Twenty two minutes", now.Add(-41*24*time.Hour), 22),
		},
		"p2": {episode("show", "The Evening Show", now.Add(-1*24*time.Hour), 60)},
	}}
	s := newStation(t, plan, []Source{
		podcastSource("pod1", "Rotation", "p1"),
		podcastSource("pod2", "Evening", "p2"),
	}, cat, now)

	item, decision := s.decide()
	if item.ItemRef == "episode:long" {
		t.Fatalf("started 90 minutes of programming 30 minutes before a booked show\n%s", decision.Explain())
	}
	if item.ItemRef != "episode:short" {
		t.Fatalf("expected the 22-minute episode that fits the gap, got %q", item.Title)
	}
	if decision.NextAnchor == nil || decision.NextAnchor.At != "18:00" {
		t.Fatalf("the decision should record the 18:00 appointment, got %+v", decision.NextAnchor)
	}
}

// And when the appointment arrives, it is what is on air.
func TestAnAnchorIsOnAirInItsWindow(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 5, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools: []Pool{
			{ID: "rotation", SourceIDs: []string{"pod1"}},
			{ID: "show", SourceIDs: []string{"pod2"}},
		},
		Blocks: []Block{
			{ID: "general", Default: true, Pools: []PoolRef{{Pool: "rotation"}}},
			{
				ID: "evening", Label: "Evening Show",
				Enter: BlockEntry{At: "18:00", Days: "*", Hard: true, Start: StartMakeNext},
				Exit:  BlockExit{At: "19:00"},
				Pools: []PoolRef{{Pool: "show"}},
			},
		},
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("rot", "Rotation item", now.Add(-40*24*time.Hour), 30)},
		"p2": {episode("show", "The Evening Show", now.Add(-1*24*time.Hour), 60)},
	}}
	s := newStation(t, plan, []Source{
		podcastSource("pod1", "Rotation", "p1"),
		podcastSource("pod2", "Evening", "p2"),
	}, cat, now)

	item, decision := s.decide()
	if item.ItemRef != "episode:show" {
		t.Fatalf("the booked show should be on air at 18:05, got %q\n%s", item.Title, decision.Explain())
	}
	if !item.IsRuleDriven || item.AnchorBlockID != "evening" {
		t.Fatalf("the item should be marked as coming from the booked block")
	}
	if item.MaxDuration > 55*time.Minute {
		t.Fatalf("the show should be bounded by its own window, got %s", item.MaxDuration)
	}
}

// ---- determinism -------------------------------------------------------

// Same seed, same state, same station. Without this nothing above is a test.
func TestSelectionIsDeterministicUnderASeed(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	run := func() []string {
		s := newStation(t, twoCategoryPlan(0.75), sources, cat, now)
		out := []string{}
		for i := 0; i < 6; i++ {
			out = append(out, s.play().ItemRef)
		}
		return out
	}
	first, second := run(), run()
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("run diverged at %d: %q vs %q", index, first[index], second[index])
		}
	}
}

// Randomness happens strictly after the rules. A candidate ruled out by a
// constraint must never appear, under any seed.
func TestRandomnessNeverResurrectsARuledOutCandidate(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"pod1", "pod2"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("banned", "Just played", now.Add(-40*24*time.Hour), 30)},
		"p2": {episode("ok", "Fine to play", now.Add(-41*24*time.Hour), 30)},
	}}
	for seed := int64(0); seed < 200; seed++ {
		s := newStation(t, plan, []Source{
			podcastSource("pod1", "One", "p1"),
			podcastSource("pod2", "Two", "p2"),
		}, cat, now)
		s.history.Record(MemoryPlay{
			SourceID: "pod1", ItemRef: "episode:banned", Category: "talk",
			StartedAt: now.Add(-5 * time.Minute), EndedAt: now,
		})
		s.engine.Rand = rand.New(rand.NewSource(seed))
		item, _, _, err := s.engine.Decide(context.Background(), now, ProgramState{})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if item.ItemRef == "episode:banned" {
			t.Fatalf("seed %d played an item that aired five minutes ago", seed)
		}
	}
}

// ---- graceful failure --------------------------------------------------

// Silence is worse than an imperfect choice. When every candidate breaks
// something, the least important rule is given up — and the record says so.
func TestConstraintsRelaxRatherThanGoingSilent(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {
			episode("one", "Episode one", now.Add(-40*24*time.Hour), 30),
			episode("two", "Episode two", now.Add(-41*24*time.Hour), 30),
		},
	}}
	s := newStation(t, plan, []Source{podcastSource("pod1", "Only Show", "p1")}, cat, now)
	// Both aired minutes ago, so item AND source separation fail for everything
	// the station owns.
	for index, ref := range []string{"episode:one", "episode:two"} {
		s.history.Record(MemoryPlay{
			SourceID: "pod1", ItemRef: ref, Category: "talk",
			StartedAt: now.Add(-time.Duration(index+1) * time.Minute),
			EndedAt:   now.Add(-time.Duration(index) * time.Minute),
		})
	}

	item, decision := s.decide()
	if item.ItemRef == "" {
		t.Fatalf("the station must play something rather than go silent\n%s", decision.Explain())
	}
	if len(decision.Relaxed) == 0 {
		t.Fatalf("the record must say which rules were given up, or a station quietly breaking its own rules looks fine")
	}
}

// A source that cannot produce anything is not an error and must not stall the
// decision.
func TestASourceWithNoContentIsSkippedNotFatal(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"empty", "pod1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p-empty": {},
		"p1":      {episode("e1", "Something", now.Add(-40*24*time.Hour), 30)},
	}}
	s := newStation(t, plan, []Source{
		podcastSource("empty", "Empty Feed", "p-empty"),
		podcastSource("pod1", "Real Show", "p1"),
	}, cat, now)

	item, _ := s.decide()
	if item.ItemRef != "episode:e1" {
		t.Fatalf("expected the show that has content, got %q", item.Title)
	}
}

// ---- working with whatever is there ------------------------------------

// A ninety-minute gap between the same artist is a good rule for four hundred
// artists and an impossible one for three. The window shrinks to what the
// library can actually satisfy, so a small station gets a rule it can keep
// instead of one it breaks on every third song.
func TestSeparationShrinksToFitASmallLibrary(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	small := []Candidate{}
	for _, artist := range []string{"A", "B", "C"} {
		small = append(small, Candidate{
			Ref: "track:" + artist, SourceID: "mus1", Creator: "Artist " + artist,
			Duration: 4 * time.Minute, Traits: Traits{HasCreator: true},
		})
	}
	env := constraintEnv{now: now, separationCreator: 90 * time.Minute}
	fitted := fitSeparationToLibrary(env, small)
	// Three artists at four minutes could theoretically manage eight minutes
	// apart, but demanding the whole cycle would force the order; the headroom
	// leaves the rest of the model something to decide.
	if fitted.separationCreator != 6*time.Minute {
		t.Fatalf("three artists at four minutes should get about six minutes apart, got %s",
			fitted.separationCreator)
	}

	// A big library is untouched: the arithmetic exceeds what was asked for.
	big := []Candidate{}
	for index := 0; index < 60; index++ {
		big = append(big, Candidate{
			Ref: "track:" + strconv.Itoa(index), SourceID: "mus1",
			Creator:  "Artist " + strconv.Itoa(index),
			Duration: 4 * time.Minute, Traits: Traits{HasCreator: true},
		})
	}
	if got := fitSeparationToLibrary(env, big).separationCreator; got != 90*time.Minute {
		t.Fatalf("a large library should keep the configured window, got %s", got)
	}
}

// One artist cannot be kept apart from themselves. Pretending otherwise just
// means relaxing the rule on every single pick and filling the record with
// compromises that mean nothing.
func TestASingleCreatorMeansNoCreatorSeparation(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	env := constraintEnv{now: now, separationCreator: 90 * time.Minute}
	fitted := fitSeparationToLibrary(env, []Candidate{
		{Ref: "track:1", SourceID: "mus1", Creator: "The Only Band", Duration: 4 * time.Minute,
			Traits: Traits{HasCreator: true}},
		{Ref: "track:2", SourceID: "mus1", Creator: "The Only Band", Duration: 4 * time.Minute,
			Traits: Traits{HasCreator: true}},
	})
	if fitted.separationCreator != 0 {
		t.Fatalf("with one artist there is no separation to keep, got %s", fitted.separationCreator)
	}
}

// "We should be able to do just music radio." A station with one category, one
// pool and no spoken word at all has to work, with no relaxations and no
// silence.
func TestAMusicOnlyStationJustWorks(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	songs := []catalog.MusicTrack{}
	for index := 0; index < 24; index++ {
		songs = append(songs, track(
			"t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%8), 200+index))
	}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "music", Target: 1}},
		Pools:      []Pool{{ID: "music", SourceIDs: []string{"mus1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "music"}}}},
	}
	s := newStation(t, plan, []Source{musicSource("mus1", "The Whole Station", "pl1")},
		&stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": songs}}, now)

	// Two songs by one artist back to back is fine here — a playlist shuffles,
	// and the shelf's proportions are the instruction. What must not happen is
	// the same RECORD coming round while others are still waiting.
	played := map[string]int{}
	lastRef := ""
	for i := 0; i < 40; i++ {
		item, decision := s.step()
		if len(decision.Relaxed) > 0 {
			t.Fatalf("pick %d had to relax %v on a station with plenty of music\n%s",
				i, decision.Relaxed, decision.Explain())
		}
		if item.ItemRef == lastRef {
			t.Fatalf("pick %d played %q twice in a row", i, item.Title)
		}
		lastRef = item.ItemRef
		played[item.ItemRef]++
	}
	if len(played) < 20 {
		t.Fatalf("forty picks from twenty-four tracks should move around; only %d distinct", len(played))
	}
}

// A three-track station is a legitimate station. It should play, and say what
// it had to give up rather than pretending everything was fine.
func TestAThreeTrackStationStillPlays(t *testing.T) {
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "music", Target: 1}},
		Pools:      []Pool{{ID: "music", SourceIDs: []string{"mus1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "music"}}}},
	}
	cat := &stubCatalog{playlists: map[string][]catalog.MusicTrack{"pl1": {
		track("t1", "One", "Artist A", 210),
		track("t2", "Two", "Artist B", 210),
		track("t3", "Three", "Artist C", 210),
	}}}
	s := newStation(t, plan, []Source{musicSource("mus1", "Tiny", "pl1")}, cat, now)

	for i := 0; i < 12; i++ {
		item, _, err := s.tryDecide()
		if err != nil || item.URL == "" {
			t.Fatalf("pick %d found nothing to play on a three-track station: %v", i, err)
		}
		s.play()
	}
}

// ---- interstitials -----------------------------------------------------

// Separator inventory is not programming: it goes between items, is never
// chosen as "what's on next", and its airtime does not count toward a
// category's share.
func TestInterstitialAirtimeIsOutsideTheFormatBalance(t *testing.T) {
	sources, cat, now := twoPodcastsAndMusic()
	spots := Source{
		ID: "spots", ChannelID: "ch1", Kind: SourceFilePool, Label: "Spots",
		Config: map[string]any{"paths": []string{"/nonexistent"}}, Enabled: true, Role: RoleCommercial,
	}
	s := newStation(t, twoCategoryPlan(0.75), append(sources, spots), cat, now)
	s.aired("spots", "talk", 30*time.Minute)
	s.aired("pod1", "talk", 30*time.Minute)

	scoring := s.engine.scoreEnv(context.Background(), s.now, ProgrammingIntent{
		Targets: map[CategoryID]float64{"talk": 0.75, "music": 0.25},
	}, nil, nil)
	if got := scoring.airtime.ByCategory["talk"]; got > 31*time.Minute {
		t.Fatalf("talk airtime = %s; the 30 minutes of spots should not count toward it", got)
	}
}

// One unplayable episode must not turn the day into music.
//
// The programme state is committed when an item is CHOSEN, not when it airs, so
// a dead pick used to spend its turn in the cycle: the obligation position was
// consumed by something nobody heard, and the pattern moved on to a break. The
// next obligation came round after that break, failed again, and the listener
// got music, silence-nobody-hears, music, for as long as the episode stayed
// owed. Jacob's morning, exactly.
//
// A failure has to leave the cycle where it was AND pass the item over, so the
// slot is refilled with the next thing you are owed.
func TestADeadPickDoesNotSpendItsTurnInTheCycle(t *testing.T) {
	now := time.Date(2026, 8, 11, 9, 10, 0, 0, time.UTC)

	shows := []Source{}
	episodes := map[string][]catalog.PodcastEpisode{}
	for index := 0; index < 3; index++ {
		id := "p" + strconv.Itoa(index)
		src := podcastSource("pod"+strconv.Itoa(index), "Show "+strconv.Itoa(index), id)
		src.Config["tier"] = []string{"C", "B", "A"}[index]
		shows = append(shows, src)
		episodes[id] = []catalog.PodcastEpisode{
			episode(id+"-new", "Show "+strconv.Itoa(index)+" today", now.Add(-5*time.Hour), 45),
		}
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 30; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%15), 200))
	}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		Pools: []Pool{
			{ID: "podcasts", Match: &PoolMatch{Kind: SourcePodcastSubscription}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{{
			ID: "fresh", Default: true,
			Pools:   []PoolRef{{Pool: "podcasts"}},
			Pattern: []PatternStep{{Want: WantBreak}, {Want: WantObligation}},
			Breaks: &BreakPolicy{
				Between:  []CategoryID{"talk"},
				Target:   BreakSize{Duration: "6m", Items: 2},
				Accept:   BreakRange{Duration: []string{"3m", "9m"}, Items: []int{1, 2}},
				Elements: []BreakElement{{Pool: "music", Count: []int{1, 2}, Fill: true}},
			},
		}},
	}
	cat := &stubCatalog{episodes: episodes, playlists: map[string][]catalog.MusicTrack{"pl1": songs}}
	s := newStation(t, plan, append(shows, musicSource("mus1", "House", "pl1")), cat, now)

	// Sitting at the obligation position.
	s.state = ProgramState{BlockID: "fresh", EnteredAt: now.Add(-10 * time.Minute), PatternIndex: 1}

	item, _, next, err := s.engine.Decide(context.Background(), now, s.state)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if item.Category != "talk" {
		t.Fatalf("the obligation position should have picked something owed, got %s", item.Category)
	}

	// That item turns out to be unplayable. The streamer passes it over and
	// rewinds — so the cycle is still AT the obligation position.
	s.engine.Skips.SuppressRef(item.ItemRef)
	rewound := s.state

	again, _, _, err := s.engine.Decide(context.Background(), now.Add(2*time.Second), rewound)
	if err != nil {
		t.Fatalf("decide after the dead pick: %v", err)
	}
	if again.Category != "talk" {
		t.Fatalf("after a dead pick the slot played %s — the position was spent on something nobody heard",
			again.Category)
	}
	if again.ItemRef == item.ItemRef {
		t.Fatal("the dead item was handed back again")
	}
	_ = next
}
