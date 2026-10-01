// Package heroes ranks the cards a client's Home leads with.
//
// A hero is the answer to "what should I play right now": one card, one tap,
// no browsing. Both clients render the same ranked list, so the ranking lives
// here — on the side that has the play log, the feed timestamps and every
// user's playback state — rather than being guessed twice, differently, by
// two apps.
//
// Every card has to EARN its place. There is no filler: a candidate with
// nothing to say (a show with no new episode, a season that is months away)
// produces no card, and a Home with nothing fresh shows the drop alone.
package heroes

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type Kind string

const (
	// KindExplore is the weekly Explore drop.
	KindExplore Kind = "explore"
	// KindEpisode is a new episode of a show the listener finishes.
	KindEpisode Kind = "episode"
	// KindSeason is the listener's own playlist for the time of year.
	KindSeason     Kind = "season"
	KindResume     Kind = "resume"
	KindRediscover Kind = "rediscover"
	KindLibrary    Kind = "library"
	KindPlaylist   Kind = "playlist"
)

// Action is what tapping the card's primary control does.
type Action string

const (
	ActionShuffle Action = "shuffle"
	ActionPlay    Action = "play"
)

// Sleeve is one cover on the card. ID names a catalog image the media route
// can serve; URL is the picture's address, samo-relative unless it lives
// elsewhere. A client with an image pipeline keyed by id uses the id; one
// without loads the URL.
type Sleeve struct {
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
}

type Target struct {
	// Type is playlist, episode, album, or audiobook.
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Hero is one card. The copy is decided here, in words, so the two clients
// say the same thing about the same drop.
type Hero struct {
	// ID is stable for as long as the card means the same thing: the drop
	// with these arrivals, this episode. A client can remember it as seen.
	ID       string   `json:"id"`
	Kind     Kind     `json:"kind"`
	Eyebrow  string   `json:"eyebrow"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Meta     string   `json:"meta,omitempty"`
	Sleeves  []Sleeve `json:"sleeves,omitempty"`
	Target   Target   `json:"target"`
	Action   Action   `json:"action"`
	// Score orders the strip; higher first. Exposed so a client can tell a
	// card that only just made it from one that owns the top.
	Score float64 `json:"score"`
	// FreshAt is the instant that earned the card its place — the newest
	// arrival, the episode's publish time — for clients that track what the
	// listener has already seen.
	FreshAt *time.Time `json:"freshAt,omitempty"`
}

// Input is everything the ranking looks at, gathered by the caller so this
// package stays pure: no database, no clock, no HTTP.
type Input struct {
	// Playlists visible to the user, with the tracks of any the ranking may
	// feature resolved through PlaylistTracks.
	Playlists      []catalog.MusicPlaylist
	PlaylistTracks func(playlistID string) []catalog.MusicTrack
	// PlaylistStates is the user's playback state per playlist id.
	PlaylistStates map[string]catalog.PlaybackState
	Podcasts       []catalog.PodcastItem
	Episodes       []catalog.PodcastEpisode
	// EpisodeStates is the user's playback state per episode id.
	EpisodeStates map[string]catalog.PlaybackState
	TrackStates   map[string]catalog.PlaybackState
	// Radio airings suppress recommendations only; they never change personal progress.
	RadioEpisodeStates map[string]catalog.PlaybackState
	ShowStates         map[string]catalog.PlaybackState
	Albums             []catalog.MusicAlbum
	AlbumStates        map[string]catalog.PlaybackState
	Books              []catalog.AudiobookItem
	BookStates         map[string]catalog.PlaybackState
	// Stable within one visit, varied on the next. Seen keys are target type:id.
	SessionKey    string
	RecentlyShown []string
}

// Sleeves in the fan; the clients blur the same ones into the backdrop.
const sleeveCount = 4

// Artists named in a subtitle before "and more".
const namedArtists = 3

// How recent an arrival still counts as "new this week".
const thisWeek = 7 * 24 * time.Hour

// How long a new episode keeps its card if nobody plays it.
const episodeShelfLife = 48 * time.Hour

// A show whose episodes the listener finishes at least this often, at least
// this many times, is one whose new episode is worth the top of the page.
const (
	sTierMinCompleted     = 3
	sTierMinCompletedRate = 0.5
)

// Rank builds the cards Home leads with, best first. `now` is the caller's
// clock so the ranking is reproducible in tests.
func Rank(in Input, now time.Time) []Hero {
	heroes := make([]Hero, 0, 4)
	if hero, ok := exploreHero(in, now); ok {
		heroes = append(heroes, hero)
	}
	heroes = append(heroes, episodeHeroes(in, now)...)
	if hero, ok := seasonHero(in, now); ok {
		heroes = append(heroes, hero)
	}
	heroes = append(heroes, listeningHeroes(in, now)...)
	heroes = append(heroes, musicHeroes(in, now)...)
	for i := range heroes {
		hero := &heroes[i]
		if in.SessionKey != "" {
			hash := fnv.New64a()
			_, _ = hash.Write([]byte(in.SessionKey + ":" + hero.ID))
			// Variation only among candidates that already passed relevance gates.
			hero.Score *= 0.8 + 0.4*float64(hash.Sum64()%10000)/10000
		}
		for seenIndex, key := range in.RecentlyShown {
			if key == hero.Target.Type+":"+hero.Target.ID {
				if seenIndex == 0 {
					hero.Score *= 0.15
				} else {
					hero.Score *= 0.45
				}
				break
			}
		}
	}
	sort.Slice(heroes, func(i, j int) bool {
		if heroes[i].Score == heroes[j].Score {
			return heroes[i].ID < heroes[j].ID
		}
		return heroes[i].Score > heroes[j].Score
	})
	// Keep the alternatives varied as well as the lead; a large album library
	// must not crowd every other kind out of the response.
	counts := map[Kind]int{}
	selected := make([]Hero, 0, 12)
	for _, hero := range heroes {
		if counts[hero.Kind] >= 2 {
			continue
		}
		counts[hero.Kind]++
		selected = append(selected, hero)
		if len(selected) == 12 {
			break
		}
	}
	return selected
}

// -- Explore ----------------------------------------------------------------

// dropFacts is what a playlist's tracks say about it: which sleeves to fan
// out, who is in it, how much of it is new, how long it runs.
type dropFacts struct {
	AddedThisWeek int
	Artists       []string
	MoreArtists   bool
	Sleeves       []Sleeve
	Duration      time.Duration
	NewestAddedAt time.Time
}

// describeDrop reads the facts off a playlist's tracks. Sleeves are distinct
// in playlist order — three songs from one album still fan out as one sleeve.
// Freshness is a COUNT of recent arrivals rather than the newest date,
// because a drop folder accumulates: one straggler identified this morning
// must not relabel a fifty-track batch from last Tuesday as today's drop.
func describeDrop(tracks []catalog.MusicTrack, now time.Time) dropFacts {
	facts := dropFacts{}
	seenSleeves := map[string]bool{}
	seenArtists := map[string]bool{}
	artists := []string{}
	for _, track := range tracks {
		if sleeve, key, ok := trackSleeve(track); ok && !seenSleeves[key] && len(facts.Sleeves) < sleeveCount {
			seenSleeves[key] = true
			facts.Sleeves = append(facts.Sleeves, sleeve)
		}
		if artist := strings.TrimSpace(trackArtist(track)); artist != "" && !seenArtists[artist] {
			seenArtists[artist] = true
			artists = append(artists, artist)
		}
		if track.AddedAt != nil && !track.AddedAt.IsZero() {
			if track.AddedAt.After(facts.NewestAddedAt) {
				facts.NewestAddedAt = *track.AddedAt
			}
			if !track.AddedAt.After(now) && now.Sub(*track.AddedAt) <= thisWeek {
				facts.AddedThisWeek++
			}
		}
		facts.Duration += time.Duration(track.DurationSeconds) * time.Second
	}
	if len(artists) > namedArtists {
		facts.MoreArtists = true
		artists = artists[:namedArtists]
	}
	facts.Artists = artists
	return facts
}

func trackArtist(track catalog.MusicTrack) string {
	if track.DisplayArtist != "" {
		return track.DisplayArtist
	}
	if len(track.ArtistNames) > 0 {
		return track.ArtistNames[0]
	}
	return ""
}

// trackSleeve is the track's cover as a sleeve, keyed so one album's tracks
// share a key. A track with no picture contributes no sleeve.
func trackSleeve(track catalog.MusicTrack) (Sleeve, string, bool) {
	for _, image := range track.Images {
		if image.ID != "" {
			return Sleeve{ID: image.ID, URL: "/api/v1/media/images/" + image.ID + "/image"}, image.ID, true
		}
		if image.URL != "" {
			return Sleeve{URL: image.URL}, image.URL, true
		}
	}
	if track.AlbumID != "" {
		return Sleeve{URL: "/api/v1/music/albums/" + track.AlbumID + "/cover"}, "album:" + track.AlbumID, true
	}
	return Sleeve{}, "", false
}

func artistsLine(lead string, facts dropFacts) string {
	if len(facts.Artists) == 0 {
		return lead
	}
	line := lead + " — " + strings.Join(facts.Artists, ", ")
	if facts.MoreArtists {
		line += " and more"
	}
	return line
}

func countLine(trackCount int, duration time.Duration) string {
	unit := "tracks"
	if trackCount == 1 {
		unit = "track"
	}
	line := fmt.Sprintf("%d %s", trackCount, unit)
	if clock := shortDuration(duration); clock != "" {
		line += " · " + clock
	}
	return line
}

// shortDuration: "6h 12m", "42m", "" under a minute.
func shortDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes <= 0 {
		return ""
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}

func shortDate(t time.Time) string {
	return t.Format("Jan 2")
}

// exploreHero is the drop. It is always a candidate — a hundred songs you
// have not worn out is the best zero-thought shuffle on the page whatever day
// it is — but it only owns the top while it holds arrivals the listener has
// not played through yet.
func exploreHero(in Input, now time.Time) (Hero, bool) {
	var drop *catalog.MusicPlaylist
	for i := range in.Playlists {
		if in.Playlists[i].System {
			drop = &in.Playlists[i]
			break
		}
	}
	if drop == nil || in.PlaylistTracks == nil {
		return Hero{}, false
	}
	tracks := in.PlaylistTracks(drop.ID)
	if len(tracks) == 0 {
		return Hero{}, false
	}
	facts := describeDrop(tracks, now)

	eyebrow := "Fresh drop"
	switch {
	case facts.AddedThisWeek > 0:
		eyebrow = fmt.Sprintf("Fresh drop · %d new this week", facts.AddedThisWeek)
	case !facts.NewestAddedAt.IsZero():
		eyebrow = "Fresh drop · updated " + shortDate(facts.NewestAddedAt)
	}

	// Fresh while the newest arrival postdates the last play: something in
	// here has not been heard. Played since, and it is still the drop, just
	// not news.
	state := in.PlaylistStates[drop.ID]
	unheard := !facts.NewestAddedAt.IsZero() &&
		(state.LastPlayedAt == nil || facts.NewestAddedAt.After(*state.LastPlayedAt))
	if in.TrackStates != nil {
		unheard = false
		for _, track := range tracks {
			if track.AddedAt == nil || track.AddedAt.After(now) || now.Sub(*track.AddedAt) > thisWeek {
				continue
			}
			played := in.TrackStates[track.ID].LastPlayedAt
			if played == nil || track.AddedAt.After(*played) {
				unheard = true
				break
			}
		}
	}
	score := 0.5
	if unheard && facts.AddedThisWeek > 0 {
		score = 0.9
	}

	hero := Hero{
		ID:       "explore:" + drop.ID + ":" + facts.NewestAddedAt.UTC().Format(time.RFC3339),
		Kind:     KindExplore,
		Eyebrow:  eyebrow,
		Title:    drop.Name,
		Subtitle: artistsLine("New music found for you", facts),
		Meta:     countLine(len(tracks), facts.Duration),
		Sleeves:  facts.Sleeves,
		Target:   Target{Type: "playlist", ID: drop.ID},
		Action:   ActionShuffle,
		Score:    score,
	}
	if !facts.NewestAddedAt.IsZero() {
		fresh := facts.NewestAddedAt
		hero.FreshAt = &fresh
	}
	return hero, true
}

// -- New episode ------------------------------------------------------------

// showAffinity is how reliably the listener finishes a show: episodes
// completed over episodes touched. A subscription says nothing about whether
// anyone listens; a completion rate does.
func showAffinity(episodes []catalog.PodcastEpisode, states map[string]catalog.PlaybackState) map[string]float64 {
	completed := map[string]int{}
	touched := map[string]int{}
	for _, episode := range episodes {
		state, ok := states[episode.ID]
		if !ok {
			continue
		}
		if state.Completed || state.PlayCount > 0 || state.ProgressSeconds > 0 {
			touched[episode.PodcastID]++
		}
		if state.Completed {
			completed[episode.PodcastID]++
		}
	}
	affinity := map[string]float64{}
	for showID, done := range completed {
		if done < sTierMinCompleted {
			continue
		}
		rate := float64(done) / float64(touched[showID])
		if rate >= sTierMinCompletedRate {
			affinity[showID] = rate
		}
	}
	return affinity
}

// episodeHeroes: one card per unstarted episode published within the last
// week by a show the listener finishes, newest first. Two new episodes of
// the same show collapse to the newest — the second is on the show's page.
func episodeHeroes(in Input, now time.Time) []Hero {
	affinity := showAffinity(in.Episodes, in.EpisodeStates)
	for showID, state := range in.ShowStates {
		if state.Favorite || state.Starred {
			affinity[showID] = 1
		}
	}
	if len(affinity) == 0 {
		return nil
	}
	shows := map[string]catalog.PodcastItem{}
	for _, show := range in.Podcasts {
		shows[show.ID] = show
	}
	newest := map[string]catalog.PodcastEpisode{}
	for _, episode := range in.Episodes {
		if _, liked := affinity[episode.PodcastID]; !liked {
			continue
		}
		if episode.PublishedAt == nil || episode.PublishedAt.After(now) || now.Sub(*episode.PublishedAt) > episodeShelfLife {
			continue
		}
		state := in.EpisodeStates[episode.ID]
		if in.RadioEpisodeStates[episode.ID].Completed {
			continue
		}
		if state.Completed || state.PlayCount > 0 || state.ProgressSeconds > 0 {
			continue
		}
		if current, ok := newest[episode.PodcastID]; !ok || episode.PublishedAt.After(*current.PublishedAt) {
			newest[episode.PodcastID] = episode
		}
	}
	heroes := make([]Hero, 0, len(newest))
	for showID, episode := range newest {
		show := shows[showID]
		title := episode.PodcastTitle
		if title == "" && show.Podcast != nil {
			title = show.Podcast.Title
		}
		age := now.Sub(*episode.PublishedAt)
		fresh := *episode.PublishedAt
		var sleeves []Sleeve
		if show.ID != "" {
			sleeves = []Sleeve{{URL: "/api/v1/podcasts/shows/" + show.ID + "/cover"}}
		}
		heroes = append(heroes, Hero{
			ID:       "episode:" + episode.ID,
			Kind:     KindEpisode,
			Eyebrow:  "New episode · " + relativeAge(age),
			Title:    episode.Title,
			Subtitle: title,
			Meta:     shortDuration(time.Duration(episode.DurationSeconds) * time.Second),
			Sleeves:  sleeves,
			Target:   Target{Type: "episode", ID: episode.ID},
			Action:   ActionPlay,
			Score:    0.8 * episodeRecency(age) * (0.8 + 0.2*affinity[showID]),
			FreshAt:  &fresh,
		})
	}
	sort.Slice(heroes, func(i, j int) bool { return heroes[i].Score > heroes[j].Score })
	return heroes
}

func episodeRecency(age time.Duration) float64 {
	switch {
	case age < 24*time.Hour:
		return 1
	case age < 72*time.Hour:
		return 0.85
	default:
		return 0.7
	}
}

func relativeAge(age time.Duration) string {
	switch {
	case age < time.Hour:
		return "just now"
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	case age < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}

// -- Season -----------------------------------------------------------------

// A season is a window on the calendar and the names a listener gives the
// playlist for it. The playlist is theirs; the calendar just knows when to
// bring it forward.
type season struct {
	name        string
	eyebrowNear string
	eyebrowOn   string
	pattern     *regexp.Regexp
	// The window is startMonth/startDay through endMonth/endDay inclusive;
	// onMonth/onDay is the day itself, when the eyebrow stops saying "almost".
	startMonth time.Month
	startDay   int
	endMonth   time.Month
	endDay     int
	onMonth    time.Month
	onDay      int
}

var seasons = []season{
	{
		name:        "Christmas",
		eyebrowNear: "Almost Christmas",
		eyebrowOn:   "Merry Christmas",
		pattern:     regexp.MustCompile(`(?i)christmas|xmas|holiday`),
		startMonth:  time.November, startDay: 20,
		endMonth: time.December, endDay: 26,
		onMonth: time.December, onDay: 24,
	},
	{
		name:        "Halloween",
		eyebrowNear: "Almost Halloween",
		eyebrowOn:   "Happy Halloween",
		pattern:     regexp.MustCompile(`(?i)halloween|spooky`),
		startMonth:  time.October, startDay: 15,
		endMonth: time.October, endDay: 31,
		onMonth: time.October, onDay: 31,
	},
}

func (s season) window(now time.Time) (open bool, on bool) {
	year := now.Year()
	start := time.Date(year, s.startMonth, s.startDay, 0, 0, 0, 0, now.Location())
	end := time.Date(year, s.endMonth, s.endDay, 23, 59, 59, 0, now.Location())
	if now.Before(start) || now.After(end) {
		return false, false
	}
	return true, now.Month() == s.onMonth && now.Day() >= s.onDay
}

// seasonHero brings forward the listener's own playlist for the time of
// year: the one whose name says Christmas, from late November. The most
// recently updated one wins when there are several.
func seasonHero(in Input, now time.Time) (Hero, bool) {
	for _, s := range seasons {
		open, on := s.window(now)
		if !open {
			continue
		}
		var match *catalog.MusicPlaylist
		for i := range in.Playlists {
			playlist := &in.Playlists[i]
			if playlist.System || playlist.TrackCount == 0 || !s.pattern.MatchString(playlist.Name) {
				continue
			}
			if match == nil || later(playlist.UpdatedAt, match.UpdatedAt) {
				match = playlist
			}
		}
		if match == nil || in.PlaylistTracks == nil {
			continue
		}
		tracks := in.PlaylistTracks(match.ID)
		if len(tracks) == 0 {
			continue
		}
		facts := describeDrop(tracks, now)
		eyebrow := s.eyebrowNear
		if on {
			eyebrow = s.eyebrowOn
		}
		return Hero{
			ID:       "season:" + match.ID + ":" + fmt.Sprint(now.Year()),
			Kind:     KindSeason,
			Eyebrow:  eyebrow,
			Title:    match.Name,
			Subtitle: artistsLine("Your "+s.name+" playlist", facts),
			Meta:     countLine(len(tracks), facts.Duration),
			Sleeves:  facts.Sleeves,
			Target:   Target{Type: "playlist", ID: match.ID},
			Action:   ActionShuffle,
			Score:    0.7,
		}, true
	}
	return Hero{}, false
}

func later(a, b *time.Time) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	return a.After(*b)
}

// Long-form resumes require meaningful, recent progress. An abandoned item
// months ago does not deserve a permanent reminder at the top of Home.
func canResume(state catalog.PlaybackState, duration int, now time.Time) bool {
	return !state.Completed && state.ProgressSeconds >= 60 && duration > state.ProgressSeconds &&
		float64(state.ProgressSeconds)/float64(duration) < 0.95 &&
		recentlyPlayed(state, now, 14*24*time.Hour)
}

func recentlyPlayed(state catalog.PlaybackState, now time.Time, window time.Duration) bool {
	last := state.LastPlayedAt
	if later(state.LastPositionAt, last) {
		last = state.LastPositionAt
	}
	return last != nil && !last.After(now) && now.Sub(*last) <= window
}

func listeningHeroes(in Input, now time.Time) []Hero {
	var result []Hero
	for _, book := range in.Books {
		state := in.BookStates[book.ID]
		if book.Missing || book.Invalid || book.Book == nil || !canResume(state, book.DurationSeconds, now) {
			continue
		}
		authors := []string{}
		for _, author := range book.Book.Authors {
			authors = append(authors, author.Name)
		}
		result = append(result, Hero{
			ID: "resume:book:" + book.ID, Kind: KindResume, Eyebrow: "Pick up where you left off",
			Title: book.Book.Title, Subtitle: strings.Join(authors, ", "),
			Meta:    shortDuration(time.Duration(book.DurationSeconds-state.ProgressSeconds)*time.Second) + " left",
			Sleeves: []Sleeve{{URL: "/api/v1/audiobooks/" + book.ID + "/cover"}},
			Target:  Target{Type: "audiobook", ID: book.ID}, Action: ActionPlay, Score: 0.82,
		})
	}
	for _, episode := range in.Episodes {
		state := in.EpisodeStates[episode.ID]
		// News and topical podcasts have a shorter useful life than books.
		if episode.PublishedAt == nil || episode.PublishedAt.After(now) || now.Sub(*episode.PublishedAt) > 7*24*time.Hour ||
			in.RadioEpisodeStates[episode.ID].Completed || !canResume(state, episode.DurationSeconds, now) {
			continue
		}
		result = append(result, Hero{
			ID: "resume:episode:" + episode.ID, Kind: KindResume, Eyebrow: "Finish your episode",
			Title: episode.Title, Subtitle: episode.PodcastTitle,
			Meta:    shortDuration(time.Duration(episode.DurationSeconds-state.ProgressSeconds)*time.Second) + " left",
			Sleeves: []Sleeve{{URL: "/api/v1/podcasts/shows/" + episode.PodcastID + "/cover"}},
			Target:  Target{Type: "episode", ID: episode.ID}, Action: ActionPlay, Score: 0.76,
		})
	}
	return result
}

func musicHeroes(in Input, now time.Time) []Hero {
	var result []Hero
	for _, album := range in.Albums {
		state := in.AlbumStates[album.ID]
		if album.TrackCount == 0 || album.Title == "" || album.HiddenFromRecentlyAdded || recentlyPlayed(state, now, 24*time.Hour) {
			continue
		}
		kind, eyebrow, score := KindLibrary, "An album to spend time with", 0.55
		switch {
		case (state.Favorite || state.Starred || state.PlayCount >= 3) && !recentlyPlayed(state, now, 14*24*time.Hour):
			kind, eyebrow, score = KindRediscover, "Back in rotation", 0.72
		case state.PlayCount == 0 && state.LastPlayedAt == nil && album.AddedAt != nil &&
			!album.AddedAt.After(now) && now.Sub(*album.AddedAt) < 14*24*time.Hour:
			eyebrow, score = "New in your library", 0.74
		case state.PlayCount > 0 || state.LastPlayedAt != nil:
			continue
		}
		result = append(result, Hero{
			ID: string(kind) + ":album:" + album.ID, Kind: kind, Eyebrow: eyebrow,
			Title: album.Title, Subtitle: album.DisplayArtist, Meta: countLine(album.TrackCount, time.Duration(album.DurationSeconds)*time.Second),
			Sleeves: []Sleeve{{URL: "/api/v1/music/albums/" + album.ID + "/cover"}},
			Target:  Target{Type: "album", ID: album.ID}, Action: ActionPlay, Score: score,
		})
	}
	for _, playlist := range in.Playlists {
		state := in.PlaylistStates[playlist.ID]
		if playlist.System || playlist.TrackCount == 0 || in.PlaylistTracks == nil || recentlyPlayed(state, now, 24*time.Hour) {
			continue
		}
		// Seasonal playlists stay seasonal, even when they are favorites.
		seasonal := false
		for _, season := range seasons {
			if season.pattern.MatchString(playlist.Name) {
				seasonal = true
				break
			}
		}
		if seasonal {
			continue
		}
		if !state.Favorite && !state.Starred && state.PlayCount == 0 && state.LastPlayedAt == nil {
			continue
		}
		tracks := in.PlaylistTracks(playlist.ID)
		if len(tracks) == 0 {
			continue
		}
		facts := describeDrop(tracks, now)
		result = append(result, Hero{
			ID: "playlist:" + playlist.ID, Kind: KindPlaylist, Eyebrow: "One of your mixes",
			Title: playlist.Name, Subtitle: strings.Join(facts.Artists, ", "),
			Meta: countLine(len(tracks), facts.Duration), Sleeves: facts.Sleeves,
			Target: Target{Type: "playlist", ID: playlist.ID}, Action: ActionShuffle, Score: 0.68,
		})
	}
	return result
}
