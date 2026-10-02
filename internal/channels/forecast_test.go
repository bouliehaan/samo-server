package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Saturday 2026-09-26, 08:04, the Morning Music Block on air. The wall's DUE
// row showed Car Talk in front of the WAN Show, and it read as the tiers being
// ignored: WAN was new overnight and S tier, Car Talk A.
//
// The station was right and the wall could not say why. WAN is 4h12m, and
// Wait Wait Don't Tell Me is booked from 10:00: the only room before it is the
// hour from 09:00, which Car Talk (36m) fits and WAN does not. WAN goes out at
// 11:07, the moment there is room for all of it. The wall was reading the
// queue -- urgency, WAN first -- split into "free this second" and "held this
// second", judged against the music hour, and drew Car Talk first with no
// time on anything. These tests pin the station's side of that morning and
// the forecast that now tells the wall the running order.

var saturdayMorning = time.Date(2026, 9, 26, 8, 4, 0, 0, time.UTC)

func saturdayAt(hour, minute int) time.Time {
	return time.Date(2026, 9, 26, hour, minute, 0, 0, time.UTC)
}

const (
	wanEpisode      = "episode:wan-0925"
	carTalkEpisode  = "episode:cartalk-2677"
	hubermanEpisode = "episode:huberman-neuralink"
)

// saturdayStation is his station at 08:04 that morning: the plan as live (the
// 09-17 document carries every Saturday booking -- the music hour 08:00-09:00,
// Wait Wait 10:00-11:00, Throughline 19:00, TED 21:00-23:00), the three
// episodes owed, and the music hour four minutes in.
func saturdayStation(t *testing.T) *station {
	t.Helper()
	now := saturdayMorning
	plan := jakeChannelPlan2026_09_17(t)

	songs := []catalog.MusicTrack{}
	for i := 0; i < 60; i++ {
		songs = append(songs, track("t"+strconv.Itoa(i), "Song "+strconv.Itoa(i),
			"Artist "+strconv.Itoa(i%20), 150+(i%5)*30))
	}
	sources := []Source{}
	for _, pool := range plan.Pools {
		for _, id := range pool.SourceIDs {
			if id == morningPlaylist {
				src := musicSource(id, "Morning Music Block", "pl1")
				src.Role = RoleShow
				sources = append(sources, src)
				continue
			}
			sources = append(sources, Source{
				ID: id, ChannelID: "ch1", Kind: SourceLiveStream, Label: pool.Label,
				Enabled: true, Role: RoleShow,
				Config: map[string]any{"url": "http://example.test/" + id},
			})
		}
	}
	sources = append(sources, musicSource(explorePlaylist, "Explore", "pl1"))
	for _, show := range []struct{ id, label, podcast, tier string }{
		{"src-wan", "The WAN Show", "p-wan", "S"},
		{"src-cartalk", "The Best of Car Talk", "p-cartalk", "A"},
		{"src-huberman", "Huberman Lab", "p-huberman", "B"},
	} {
		src := podcastSource(show.id, show.label, show.podcast)
		src.Config["tier"] = show.tier
		sources = append(sources, src)
	}

	cat := &stubCatalog{
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		episodes: map[string][]catalog.PodcastEpisode{
			"p-wan": {
				// Out at 22:30 the night before, 4h11m47s.
				episode("wan-0925", "YouTube Is Getting Way Worse And Also Way Better", now.Add(-9*time.Hour-34*time.Minute), 252),
				episode("wan-0918", "I'm a Tesla Driver Now", now.AddDate(0, 0, -7), 151),
			},
			"p-cartalk": {
				episode("cartalk-2677", "#2677: Stumps and Chumps", now.Add(-7*time.Hour-4*time.Minute), 36),
				episode("cartalk-2666", "#2666: The Spidermobile", now.AddDate(0, 0, -5), 36),
			},
			"p-huberman": {
				episode("huberman-neuralink", "Neuralink & Technologies to Enhance Human Brains", now.Add(-2*time.Hour-19*time.Minute), 122),
				episode("huberman-older", "An older Huberman", now.AddDate(0, 0, -9), 110),
			},
		},
	}
	s := newStation(t, plan, sources, cat, now)
	s.state = ProgramState{BlockID: morningMusicBlock, EnteredAt: saturdayAt(8, 0), ItemCount: 2}
	s.history.Record(MemoryPlay{
		SourceID: morningPlaylist, ItemRef: "track:t-first", Category: "talk",
		StartedAt: saturdayAt(8, 0), EndedAt: now, DurationSeconds: 240,
	})
	return s
}

// firstAirings plays the station until `until` and returns when each item
// first went out, and every decision by the moment it was made.
func (s *station) firstAirings(until time.Time) (map[string]time.Time, map[time.Time]Decision) {
	s.t.Helper()
	first := map[string]time.Time{}
	decisions := map[time.Time]Decision{}
	for s.now.Before(until) {
		began := s.now
		item, decision := s.step()
		decisions[began] = decision
		if _, seen := first[item.ItemRef]; !seen {
			first[item.ItemRef] = began
		}
	}
	return first, decisions
}

// The station's side, which was right: the hour before Wait Wait goes to the
// owed episode that fits it, and WAN goes out whole at the first moment it can.
func TestSaturdayWANWaitsForRoomAndCarTalkTakesTheHourBeforeWaitWait(t *testing.T) {
	s := saturdayStation(t)
	first, decisions := s.firstAirings(saturdayAt(17, 0))

	nine := decisions[saturdayAt(9, 0)]
	if got := first[carTalkEpisode]; !got.Equal(saturdayAt(9, 0)) {
		t.Fatalf("Car Talk first aired at %s, want 09:00, the hour WAN cannot fill\n%s",
			got.Format("15:04:05"), nine.Explain())
	}
	if rule, reason := rejectionOf(nine, wanEpisode); rule != "fitsBeforeAnchor" {
		t.Fatalf("at 09:00 WAN should be refused only for not fitting before Wait Wait, got %q %q\n%s",
			rule, reason, nine.Explain())
	}
	if len(nine.Owed) == 0 || nine.Owed[0].Ref != wanEpisode {
		t.Fatalf("WAN (S) must still head the queue at 09:00: %+v", nine.Owed)
	}

	wan := first[wanEpisode]
	if wan.Before(saturdayAt(11, 0)) || wan.After(saturdayAt(11, 15)) {
		t.Fatalf("WAN first aired at %s, want straight after Wait Wait (11:00, behind at most the opening break)",
			wan.Format("15:04:05"))
	}
	for ref, at := range first {
		if strings.HasPrefix(ref, "episode:") && at.After(saturdayAt(11, 0)) && at.Before(wan) {
			t.Fatalf("%s went out at %s, between Wait Wait and WAN", ref, at.Format("15:04:05"))
		}
	}
	if hub := first[hubermanEpisode]; !hub.After(wan.Add(252 * time.Minute)) {
		t.Fatalf("Huberman (B) at %s, want after WAN ends (%s)", hub.Format("15:04"),
			wan.Add(252*time.Minute).Format("15:04"))
	}
}

// forecast runs the air-order forecast from where the test station stands,
// against private copies of its stores, exactly as Scheduler.Forecast does
// against copies of the live tables.
func (s *station) forecast() Forecast {
	s.t.Helper()
	ctx := context.Background()
	history := NewMemoryHistory()
	for _, play := range s.history.Plays() {
		history.Record(play)
	}
	owed := NewMemoryObligations()
	stored, err := s.engine.Obligations.List(ctx, s.now)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := owed.Notice(ctx, stored, s.now); err != nil {
		s.t.Fatal(err)
	}
	run := *s.engine
	run.History, run.Obligations = history, owed
	forecast, err := forecastRun(ctx, &run, s.engine.Skips, s.state, s.now, forecastHorizon)
	if err != nil {
		s.t.Fatalf("forecast: %v", err)
	}
	return forecast
}

// The forecast is the station run forward, so what it says at 08:04 is what
// the station then does: the same order, at the same times give or take the
// lengths of the songs it draws differently. And it touches nothing of the
// station's own.
func TestTheForecastIsTheRunningOrderTheStationThenPlays(t *testing.T) {
	s := saturdayStation(t)
	historyBefore := s.history.Len()
	stateBefore := s.state

	forecast := s.forecast()
	if !forecast.Complete {
		t.Fatalf("every owed episode fits somewhere today; the forecast stopped at %s", forecast.Until)
	}
	if s.history.Len() != historyBefore || !reflect.DeepEqual(s.state, stateBefore) {
		t.Fatal("the forecast wrote to the station it was forecasting")
	}
	if list, _ := s.engine.Obligations.List(context.Background(), s.now); len(list) != 0 {
		t.Fatalf("the forecast noticed obligations into the station's own store: %+v", list)
	}

	refs := []string{carTalkEpisode, wanEpisode, hubermanEpisode}
	sort.SliceStable(refs, func(i, j int) bool { return forecast.Airings[refs[i]].Before(forecast.Airings[refs[j]]) })
	if strings.Join(refs, " ") != strings.Join([]string{carTalkEpisode, wanEpisode, hubermanEpisode}, " ") {
		t.Fatalf("forecast order %v, want Car Talk (09:00), WAN (after Wait Wait), Huberman", refs)
	}
	if got := forecast.Airings[carTalkEpisode]; !got.Equal(saturdayAt(9, 0)) {
		t.Fatalf("forecast Car Talk at %s, want 09:00", got.Format("15:04:05"))
	}

	first, _ := s.firstAirings(saturdayAt(17, 0))
	for _, ref := range refs {
		predicted, aired := forecast.Airings[ref], first[ref]
		if predicted.IsZero() || aired.IsZero() {
			t.Fatalf("%s: forecast %s, aired %s", ref, predicted, aired)
		}
		if drift := predicted.Sub(aired); drift > 10*time.Minute || drift < -10*time.Minute {
			t.Fatalf("%s: forecast %s, aired %s", ref, predicted.Format("15:04:05"), aired.Format("15:04:05"))
		}
	}
}

// Something the station owes and no pool can reach will never air, and must
// not keep a forecast running to its horizon looking for it.
func TestAForecastDoesNotWaitForWhatNothingCanPlay(t *testing.T) {
	s := saturdayStation(t)
	orphan := podcastSource("src-orphan", "A show no pool selects", "p-orphan")
	orphan.Kind = "podcast-orphan"
	s.engine.Sources = append(s.engine.Sources, orphan)
	if err := s.engine.Obligations.Notice(context.Background(), []Obligation{{
		SourceID: "src-orphan", ItemRef: "episode:orphan", Tier: TierS, State: ObligationPending,
		PublishedAt: saturdayMorning.Add(-time.Hour), ExpiresAt: saturdayMorning.Add(71 * time.Hour),
	}}, saturdayMorning); err != nil {
		t.Fatal(err)
	}
	forecast := s.forecast()
	if !forecast.Complete || forecast.Until.After(saturdayAt(17, 0)) {
		t.Fatalf("the forecast ran to %s for an episode nothing reaches", forecast.Until.Format("Mon 15:04"))
	}
	if _, placed := forecast.Airings["episode:orphan"]; placed {
		t.Fatal("an episode no pool reaches was placed")
	}
}

// A step-aside runs out on the forecast's clock, and nothing the forecast does
// reaches the real registry.
func TestASkipSnapshotRunsOnItsOwnClock(t *testing.T) {
	real := saturdayMorning
	registry := NewSkipRegistry(func() time.Time { return real })
	registry.Suppress("src-cartalk", 20*time.Minute)
	registry.SuppressRef("ch1", carTalkEpisode)
	registry.PreferRef("ch1", "episode:back")

	virtual := real
	snapshot := registry.Snapshot(func() time.Time { return virtual })
	if !snapshot.Suppressed("src-cartalk") || !snapshot.RefSuppressed("ch1", carTalkEpisode) {
		t.Fatal("the snapshot lost what the registry was passing over")
	}
	if snapshot.PreferredRef("ch1") != "" {
		t.Fatal("BACK is spent on the station's next decision, not carried into a forecast")
	}
	virtual = real.Add(21 * time.Minute)
	if snapshot.Suppressed("src-cartalk") {
		t.Fatal("a twenty-minute step-aside still held twenty-one virtual minutes in")
	}
	snapshot.Suppress("src-wan", time.Hour)
	if registry.Suppressed("src-wan") || !registry.Suppressed("src-cartalk") {
		t.Fatal("the snapshot and the registry share state")
	}
}

// A simulated decision's item is never played, so nothing is fetched for it:
// walking an enclosure's tracking chain counts as a download at every hop.
func TestASimulatedDecisionFetchesNothing(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer server.Close()

	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	remote := episode("remote", "Only on the CDN", now.Add(-time.Hour), 40)
	remote.AudioFiles = nil
	remote.EnclosureURL = server.URL + "/remote.mp3"
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{"p1": {remote}}}
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{"pod1"}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	s := newStation(t, plan, []Source{podcastSource("pod1", "Show", "p1")}, cat, now)

	result, err := Simulate(context.Background(), s.engine, SimOptions{Start: now, Duration: time.Hour})
	if err != nil || len(result.Steps) == 0 {
		t.Fatalf("simulate: %v, %d steps", err, len(result.Steps))
	}
	if hits.Load() != 0 {
		t.Fatalf("a simulation fetched the enclosure %d times", hits.Load())
	}
	if s.engine.Simulated {
		t.Fatal("the engine was left marked simulated after the run")
	}
	// The station itself does walk the chain, which is what makes this a test.
	candidates := s.candidates()
	if len(candidates) == 0 {
		t.Fatal("no candidates")
	}
	if _, err := s.engine.Materialise(context.Background(), candidates[0]); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("the station's own resolve reached the server %d times, want 1", hits.Load())
	}
}

// ---- against the real tables -------------------------------------------

// forecastChannel is a channel in a real database the way his stood at 08:04
// that Saturday, reduced to what the forecast has to read: Car Talk on air
// until 08:11 (an owed A-tier episode, on its first surfacing), WAN and
// Huberman owed, and a booked hour at 10:00.
type forecastChannel struct {
	db                      *sql.DB
	deps                    Dependencies
	carTalk, wan, huberman  Source
	carTalkRef, wanRef, hub string
	onAir                   *OnAir
}

func newForecastChannel(t *testing.T) forecastChannel {
	t.Helper()
	ctx := context.Background()
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	now := saturdayMorning
	podcast := func(label, podcastID, tier string) Source {
		return mustSource(t, db, "ch1", CreateSourceInput{
			Kind: SourcePodcastSubscription, Label: label, Role: RoleTalk, Enabled: boolPtr(true),
			Config: map[string]any{"podcastId": podcastID, "tier": tier},
		})
	}
	c := forecastChannel{db: db}
	c.wan = podcast("The WAN Show", "p-wan", "S")
	c.carTalk = podcast("The Best of Car Talk", "p-cartalk", "A")
	c.huberman = podcast("Huberman Lab", "p-huberman", "B")
	music := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourceMusicPlaylist, Label: "Explore", Role: RoleMusic, Enabled: boolPtr(true),
		Config: map[string]any{"playlistId": "pl1"},
	})
	waitWait := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourceLiveStream, Label: "Wait Wait Don't Tell Me", Role: RoleShow, Enabled: boolPtr(true),
		Config: map[string]any{"url": "http://example.test/waitwait"},
	})
	plan := Plan{
		Version: PlanVersion,
		Categories: []CategoryDef{
			{ID: "talk", Label: "Talk", Target: 1},
			{ID: "music", Label: "Music", Target: 0},
		},
		Pools: []Pool{
			{ID: "podcasts", Match: &PoolMatch{Kind: SourcePodcastSubscription}},
			{ID: "music", Match: &PoolMatch{Kind: SourceMusicPlaylist}},
			{ID: "waitwait", SourceIDs: []string{waitWait.ID}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true, Pools: []PoolRef{{Pool: "podcasts"}}},
			{ID: "waitwait", Label: "Wait Wait Don't Tell Me",
				Enter: BlockEntry{At: "10:00", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "11:00"},
				Pools: []PoolRef{{Pool: "waitwait"}}},
		},
		LongForm:     LongFormPolicy{Threshold: "2h", Rest: "21d"},
		Freshness:    FreshnessPolicy{Surfacings: map[string]int{"S": 2, "A": 2}},
		ListeningDay: &DaySpec{Start: "08:00", End: "23:00"},
		UnderrunPool: "music",
	}
	if err := SavePlan(ctx, db, "ch1", plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}

	songs := []catalog.MusicTrack{}
	for i := 0; i < 40; i++ {
		songs = append(songs, track("t"+strconv.Itoa(i), "Song "+strconv.Itoa(i), "Artist "+strconv.Itoa(i%15), 180+(i%4)*30))
	}
	cat := &stubCatalog{
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
		episodes: map[string][]catalog.PodcastEpisode{
			"p-wan":      {episode("wan-0925", "WAN Show September 25", now.Add(-9*time.Hour-34*time.Minute), 252)},
			"p-cartalk":  {episode("cartalk-2677", "#2677: Stumps and Chumps", now.Add(-7*time.Hour-4*time.Minute), 36)},
			"p-huberman": {episode("huberman-neuralink", "Neuralink", now.Add(-2*time.Hour-19*time.Minute), 122)},
		},
	}
	c.wanRef, c.carTalkRef, c.hub = wanEpisode, carTalkEpisode, hubermanEpisode

	// What the live engine has already written: the three obligations, the
	// programme state, and the play log with Car Talk's row still open.
	obligations := NewSQLObligations(db, "ch1")
	owe := func(src Source, ref, title string, tier Tier, published time.Time) Obligation {
		return Obligation{
			ChannelID: "ch1", SourceID: src.ID, SourceLabel: src.Label, ItemRef: ref, Title: title,
			Tier: tier, PublishedAt: published, ExpiresAt: published.Add(72 * time.Hour),
			SettleAt: plan.Freshness.SurfacingsFor(tier), State: ObligationPending,
		}
	}
	if err := obligations.Notice(ctx, []Obligation{
		owe(c.wan, wanEpisode, "WAN Show September 25", TierS, now.Add(-9*time.Hour-34*time.Minute)),
		owe(c.carTalk, carTalkEpisode, "#2677: Stumps and Chumps", TierA, now.Add(-7*time.Hour-4*time.Minute)),
		owe(c.huberman, hubermanEpisode, "Neuralink", TierB, now.Add(-2*time.Hour-19*time.Minute)),
	}, now.Add(-time.Hour)); err != nil {
		t.Fatalf("seed obligations: %v", err)
	}
	onAirFrom := saturdayAt(7, 35)
	for index, row := range []struct {
		source Source
		ref    string
		start  time.Time
		ended  string
		secs   int
	}{
		{music, "track:t1", saturdayAt(7, 29), saturdayAt(7, 32).Format(time.RFC3339), 180},
		{music, "track:t2", saturdayAt(7, 32), saturdayAt(7, 35).Format(time.RFC3339), 180},
		{c.carTalk, carTalkEpisode, onAirFrom, "", 36 * 60},
	} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO channel_play_log (id, channel_id, source_id, item_ref, title, kind, category, started_at, ended_at, duration_seconds, exposure)
			VALUES (?, 'ch1', ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
			"cplay_"+strconv.Itoa(index), row.source.ID, row.ref, row.ref, string(row.source.Kind),
			string(SourceCategory(row.source)), row.start.Format(time.RFC3339), row.ended, row.secs,
		); err != nil {
			t.Fatalf("seed play log: %v", err)
		}
	}
	if err := SaveProgramState(ctx, db, "ch1", ProgramState{BlockID: "general", EnteredAt: saturdayAt(7, 0), ItemCount: 3}); err != nil {
		t.Fatalf("seed programme state: %v", err)
	}

	c.deps = Dependencies{
		DB: db, Catalog: cat,
		Skips:  NewSkipRegistry(func() time.Time { return now }),
		Logger: log.New(io.Discard, "", 0),
		Now:    func() time.Time { return now },
	}
	c.onAir = &OnAir{
		Item: PlaybackItem{
			ItemRef: carTalkEpisode, SourceID: c.carTalk.ID, Kind: SourcePodcastSubscription,
			DurationSeconds: 36 * 60, Exposure: 1,
		},
		StartedAt: onAirFrom,
		EndsAt:    onAirFrom.Add(36 * time.Minute),
	}
	return c
}

// tables is everything the forecast could conceivably write, as text.
func (c forecastChannel) tables(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	for _, query := range []string{
		`SELECT id, item_ref, started_at, ended_at, duration_seconds, exposure FROM channel_play_log ORDER BY id`,
		`SELECT item_ref, credit, state, airings, target, expires_at, updated_at FROM channel_obligations ORDER BY item_ref`,
		`SELECT channel_id, state_json FROM channel_program_state ORDER BY channel_id`,
		`SELECT id FROM channel_decisions ORDER BY id`,
	} {
		rows, err := c.db.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(values)
			out.Write(encoded)
			out.WriteByte('\n')
		}
		rows.Close()
	}
	return out.String()
}

// The forecast reads the station as it stands -- plan, programme state, play
// log with its open row, obligations, the item on air and the credit it is
// about to earn -- and writes none of it.
func TestTheForecastReadsTheLiveStationAndWritesNothing(t *testing.T) {
	c := newForecastChannel(t)
	before := c.tables(t)

	forecast, err := NewScheduler(c.deps).Forecast(context.Background(), "ch1", c.onAir)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	if after := c.tables(t); after != before {
		t.Fatalf("the forecast wrote to the live tables:\nbefore\n%s\nafter\n%s", before, after)
	}
	if !forecast.From.Equal(saturdayAt(8, 11)) {
		t.Fatalf("the forecast starts at %s, want 08:11 when Car Talk on air ends", forecast.From.Format("15:04:05"))
	}
	// Car Talk ends at 08:11 with its first surfacing earned. Its second is a
	// heard-once episode's, eight hours of separation away -- not the first
	// thing the forecast plays again because it forgot the airing in progress.
	if again, placed := forecast.Airings[carTalkEpisode]; placed && again.Before(saturdayAt(16, 11)) {
		t.Fatalf("Car Talk forecast again at %s, straight after its own airing", again.Format("15:04:05"))
	}
	wan := forecast.Airings[wanEpisode]
	if wan.Before(saturdayAt(11, 0)) || wan.After(saturdayAt(11, 15)) {
		t.Fatalf("WAN forecast at %s, want after the booked hour at 10:00", wan.Format("15:04:05"))
	}
	if hub := forecast.Airings[hubermanEpisode]; !hub.After(wan) {
		t.Fatalf("Huberman forecast at %s, WAN at %s", hub.Format("15:04"), wan.Format("15:04"))
	}
}

// The owed list says when the station expects to air each episode once a
// forecast has been made, and the first ask never waits for one.
func TestOwedSaysWhenEachEpisodeIsExpectedToAir(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	mustChannel(t, db, "ch1")
	src := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Show", Role: RoleTalk, Enabled: boolPtr(true),
		Config: map[string]any{"podcastId": "p1", "tier": "S"},
	})
	now := time.Now().UTC()
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("new", "New today", now.Add(-time.Hour), 40)},
	}}
	plan := Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}},
		Pools:        []Pool{{ID: "talk", SourceIDs: []string{src.ID}}},
		Blocks:       []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
		ListeningDay: &DaySpec{Start: "00:00", End: "23:59"},
	}
	if err := SavePlan(ctx, db, "ch1", plan); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceOptions{DB: db, Catalog: cat, Logger: log.New(io.Discard, "", 0)})
	t.Cleanup(func() { svc.Close(context.Background()) })

	owed, err := svc.Owed(ctx, "ch1")
	if err != nil || len(owed) != 1 {
		t.Fatalf("owed: %v %+v", err, owed)
	}
	if owed[0].ExpectedAt != nil {
		t.Fatal("the first ask cannot have a forecast yet; it must not wait for one either")
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, ok := svc.Forecasted("ch1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the forecast never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	owed, err = svc.Owed(ctx, "ch1")
	if err != nil || len(owed) != 1 || owed[0].ExpectedAt == nil {
		t.Fatalf("owed after the forecast: %v %+v", err, owed)
	}
	if drift := owed[0].ExpectedAt.Sub(now); drift < -time.Minute || drift > time.Minute {
		t.Fatalf("with nothing on air the new episode is next, at %s; forecast %s", now, owed[0].ExpectedAt)
	}
	body, _ := json.Marshal(owed[0])
	if !strings.Contains(string(body), `"expectedAt":"`) {
		t.Fatalf("the wall reads expectedAt by name: %s", body)
	}
}

// stalledEars never answers until the run asking is cancelled: a forecast
// stuck mid-run, holding the database, for as long as nobody stops it.
type stalledEars struct {
	asked chan struct{}
	once  sync.Once
}

func (e *stalledEars) EpisodeProgress(ctx context.Context, _ []string) (map[string]EpisodeListening, error) {
	e.once.Do(func() { close(e.asked) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// lockedLog is a log destination the forecast's goroutine and the test can
// share.
type lockedLog struct {
	mu  sync.Mutex
	out strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.out.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.out.String()
}

// A forecast runs in the background and reads the database for as long as it
// takes. Close stops it and waits for it, so the caller can close the
// database behind it -- a run left going was what logged "list obligations:
// sql: database is closed" -- and nothing starts another one afterwards.
func TestCloseStopsTheForecastBeforeTheDatabaseGoes(t *testing.T) {
	db := newTestDB(t)
	mustChannel(t, db, "ch1")
	src := mustSource(t, db, "ch1", CreateSourceInput{
		Kind: SourcePodcastSubscription, Label: "Show", Role: RoleTalk, Enabled: boolPtr(true),
		Config: map[string]any{"podcastId": "p1", "tier": "S"},
	})
	now := time.Now().UTC()
	plan := Plan{
		Version:    PlanVersion,
		Categories: []CategoryDef{{ID: "talk", Target: 1}},
		Pools:      []Pool{{ID: "talk", SourceIDs: []string{src.ID}}},
		Blocks:     []Block{{ID: "general", Default: true, Pools: []PoolRef{{Pool: "talk"}}}},
	}
	if err := SavePlan(context.Background(), db, "ch1", plan); err != nil {
		t.Fatal(err)
	}
	cat := &stubCatalog{episodes: map[string][]catalog.PodcastEpisode{
		"p1": {episode("new", "New today", now.Add(-time.Hour), 40)},
	}}
	ears := &stalledEars{asked: make(chan struct{})}
	logs := &lockedLog{}
	svc := NewService(ServiceOptions{DB: db, Catalog: cat, Listened: ears, Logger: log.New(logs, "", 0)})

	running := func() bool {
		svc.forecastMu.Lock()
		defer svc.forecastMu.Unlock()
		return svc.forecasts["ch1"] != nil && svc.forecasts["ch1"].running
	}
	svc.airOrder("ch1", "before", nil)
	select {
	case <-ears.asked:
	case <-time.After(30 * time.Second):
		t.Fatal("the forecast never got as far as a decision")
	}

	closing, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc.Close(closing)
	if closing.Err() != nil {
		t.Fatal("Close gave up waiting for the forecast")
	}
	if running() {
		t.Fatal("a forecast was still running when Close returned")
	}
	svc.airOrder("ch1", "after", nil)
	if running() {
		t.Fatal("a forecast started after Close")
	}
	if strings.Contains(logs.String(), "could not forecast") {
		t.Fatalf("a run Close stopped was reported as a failure:\n%s", logs.String())
	}
}
