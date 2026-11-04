package wrapped

import (
	"sort"
	"strings"
	"time"
)

// Listen is one music listen in the year, from either source.
type Listen struct {
	Artist string
	Album  string
	Title  string
	At     time.Time
	// Seconds heard. Zero means unknown (a Last.fm scrobble that matched
	// nothing in the library); it is then estimated.
	Seconds int
	// Image is a cover URL the listen came with (Last.fm's art).
	Image string
	// Library ids, when the listen is of something samo has.
	TrackID  string
	AlbumID  string
	ArtistID string
}

// estimatedTrackSeconds stands in for a listen whose length samo cannot know:
// roughly the length of a pop song, which is what Spotify assumes as well.
const estimatedTrackSeconds = 210

const topN = 5

// BuildMusic turns a year of listens into its Wrapped. Pure: the caller owns
// where the listens come from and the zone hours and days are read in.
func BuildMusic(listens []Listen, zone *time.Location) *MusicStats {
	if len(listens) == 0 {
		return nil
	}
	if zone == nil {
		zone = time.UTC
	}
	sorted := append([]Listen(nil), listens...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })

	stats := &MusicStats{Listens: len(sorted)}
	artists := map[string]*rankedAcc{}
	tracks := map[string]*rankedAcc{}
	albums := map[string]*rankedAcc{}
	days := map[string]int{}
	trackDays := map[string]int{}
	trackDayFirst := map[string]Listen{}
	totalSeconds := 0
	estimated := 0
	var monthSeconds [12]int

	for _, listen := range sorted {
		seconds := listen.Seconds
		if seconds <= 0 {
			seconds = estimatedTrackSeconds
			estimated++
		}
		totalSeconds += seconds
		local := listen.At.In(zone)
		monthSeconds[int(local.Month())-1] += seconds
		stats.Hours[local.Hour()]++
		day := local.Format("2006-01-02")
		days[day]++

		artistKey := fold(listen.Artist)
		accumulate(artists, artistKey, seconds, listen, func(r *Ranked) {
			r.Name = listen.Artist
			r.ArtistID = listen.ArtistID
		})
		trackKey := artistKey + "\x00" + fold(listen.Title)
		accumulate(tracks, trackKey, seconds, listen, func(r *Ranked) {
			r.Name = listen.Title
			r.Artist = listen.Artist
			r.TrackID = listen.TrackID
			r.AlbumID = listen.AlbumID
		})
		if strings.TrimSpace(listen.Album) != "" {
			accumulate(albums, artistKey+"\x00"+fold(listen.Album), seconds, listen, func(r *Ranked) {
				r.Name = listen.Album
				r.Artist = listen.Artist
				r.AlbumID = listen.AlbumID
			})
		}
		repeatKey := day + "\x00" + trackKey
		trackDays[repeatKey]++
		if _, ok := trackDayFirst[repeatKey]; !ok {
			trackDayFirst[repeatKey] = listen
		}
	}

	stats.Minutes = totalSeconds / 60
	for month, seconds := range monthSeconds {
		stats.Months[month] = seconds / 60
	}
	// Called estimated when more than a quarter of the listens had no length.
	stats.MinutesEstimated = estimated*4 > len(sorted)
	stats.Artists = len(artists)
	stats.Tracks = len(tracks)
	stats.TopArtists = top(artists)
	stats.TopTracks = top(tracks)
	stats.TopAlbums = top(albums)

	best := 0
	for month, minutes := range stats.Months {
		if minutes > stats.Months[best] {
			best = month
		}
	}
	stats.TopMonth = best + 1

	for day, count := range days {
		if count > stats.BusiestDay.Listens || (count == stats.BusiestDay.Listens && day < stats.BusiestDay.Date) {
			stats.BusiestDay = DayCount{Date: day, Listens: count}
		}
	}
	stats.Days = len(days)
	stats.LongestStreak = longestStreak(days)

	for key, count := range trackDays {
		first := trackDayFirst[key]
		day := first.At.In(zone).Format("2006-01-02")
		if count > stats.OnRepeat.Plays || (count == stats.OnRepeat.Plays && day < stats.OnRepeat.Date) {
			stats.OnRepeat = Repeat{Title: first.Title, Artist: first.Artist, Plays: count, Date: day, TrackID: first.TrackID, AlbumID: first.AlbumID, Image: first.Image}
		}
	}
	if stats.OnRepeat.Plays < 3 {
		// Twice in a day is not a habit worth a slide.
		stats.OnRepeat = Repeat{}
	}

	first := sorted[0]
	stats.First = FirstListen{Title: first.Title, Artist: first.Artist, At: first.At, TrackID: first.TrackID, AlbumID: first.AlbumID, Image: first.Image}
	stats.Clock = clockFor(stats.Hours)
	return stats
}

type rankedAcc struct {
	ranked Ranked
	first  time.Time
}

func accumulate(into map[string]*rankedAcc, key string, seconds int, listen Listen, describe func(*Ranked)) {
	acc, ok := into[key]
	if !ok {
		acc = &rankedAcc{first: listen.At}
		describe(&acc.ranked)
		into[key] = acc
	}
	acc.ranked.Plays++
	acc.ranked.Minutes += seconds
	if acc.ranked.Image == "" && listen.Image != "" {
		acc.ranked.Image = listen.Image
	}
	// Library ids fill in from any listen that has them.
	if acc.ranked.TrackID == "" {
		acc.ranked.TrackID = listen.TrackID
	}
	if acc.ranked.AlbumID == "" {
		acc.ranked.AlbumID = listen.AlbumID
	}
	if acc.ranked.ArtistID == "" {
		acc.ranked.ArtistID = listen.ArtistID
	}
}

// top ranks by plays, then minutes, then whichever was first heard.
func top(from map[string]*rankedAcc) []Ranked {
	list := make([]*rankedAcc, 0, len(from))
	for _, acc := range from {
		list = append(list, acc)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.ranked.Plays != b.ranked.Plays {
			return a.ranked.Plays > b.ranked.Plays
		}
		if a.ranked.Minutes != b.ranked.Minutes {
			return a.ranked.Minutes > b.ranked.Minutes
		}
		return a.first.Before(b.first)
	})
	out := make([]Ranked, 0, topN)
	for i := 0; i < len(list) && i < topN; i++ {
		ranked := list[i].ranked
		// Accumulated as seconds; reported as minutes.
		ranked.Minutes /= 60
		out = append(out, ranked)
	}
	return out
}

func longestStreak(days map[string]int) int {
	dates := make([]time.Time, 0, len(days))
	for day := range days {
		if t, err := time.Parse("2006-01-02", day); err == nil {
			dates = append(dates, t)
		}
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	best, run := 0, 0
	for i, date := range dates {
		if i > 0 && date.Sub(dates[i-1]) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		if run > best {
			best = run
		}
	}
	return best
}

// clockFor names when in the day someone listens most.
func clockFor(hours [24]int) Clock {
	windows := []struct {
		name, blurb string
		from, to    int // [from, to) in hours, wrapping past midnight
	}{
		{"Night owl", "Most of your listening happened after 10pm.", 22, 4},
		{"Early bird", "You were up with the music, before 9am.", 4, 9},
		{"Nine-to-fiver", "Your music kept you company through the working day.", 9, 17},
		{"Evening regular", "Evenings were when the music came on.", 17, 22},
	}
	best, bestCount := 0, -1
	for i, w := range windows {
		count := 0
		for h := w.from; h != w.to; h = (h + 1) % 24 {
			count += hours[h]
		}
		if count > bestCount {
			best, bestCount = i, count
		}
	}
	peak := 0
	for h, count := range hours {
		if count > hours[peak] {
			peak = h
		}
	}
	return Clock{Name: windows[best].name, Blurb: windows[best].blurb, PeakHour: peak}
}

// TierFor is the year in one word, by minutes per day of the year covered so
// a Wrapped opened in March, or from a history switched on in June, is not
// judged against a whole year.
func TierFor(minutes int, coveredDays int) Tier {
	if coveredDays < 1 {
		coveredDays = 1
	}
	perDay := float64(minutes) / float64(coveredDays)
	switch {
	case perDay >= 90:
		return Tier{Name: "Baller", Blurb: "Music all day, every day. The headphones might as well be surgically attached."}
	case perDay >= 30:
		return Tier{Name: "Shot caller", Blurb: "A daily habit and the final say on what plays next."}
	default:
		return Tier{Name: "Not-at-aller", Blurb: "You dip in when the mood strikes. Quality over quantity."}
	}
}

func fold(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}
