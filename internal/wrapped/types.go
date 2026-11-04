// Package wrapped builds a listener's year in samo: their top artists, tracks
// and albums, how much they listened, and when.
//
// The year comes from the listener's linked Last.fm account by default, which
// holds everything they scrobbled, from samo or anywhere else. A listener can
// also turn on samo's own history in the player's settings; samo then logs
// the music they play itself, measured by the same engine that decides a
// Last.fm scrobble (internal/scrobble), and adds their audiobooks and
// podcasts to the year. Nothing is logged for anyone who has not turned it on.
package wrapped

import (
	"errors"
	"time"
)

const (
	SourceLastFM = "lastfm"
	SourceSamo   = "samo"
)

var (
	// ErrNoSource means there is nothing to build a year from: no Last.fm
	// account is linked and samo's own history is off.
	ErrNoSource = errors.New("link Last.fm or turn on samo's listening history to get a Wrapped")
	ErrBadYear  = errors.New("year is out of range")
)

// Settings are one listener's Wrapped choices.
type Settings struct {
	// LocalHistory is whether samo keeps its own log of what they play.
	LocalHistory bool       `json:"localHistory"`
	EnabledAt    *time.Time `json:"enabledAt,omitempty"`
	// Listens is how many listens samo's own log holds for them.
	Listens int `json:"listens"`
}

// Report is a listener's Wrapped for one year.
type Report struct {
	Year int `json:"year"`
	// Source is where the music came from: SourceLastFM or SourceSamo.
	Source     string `json:"source"`
	LastFMUser string `json:"lastfmUser,omitempty"`
	// From and Through bound the listening it covers: the year so far, or
	// from when samo's own history was turned on.
	From    time.Time `json:"from"`
	Through time.Time `json:"through"`
	BuiltAt time.Time `json:"builtAt"`
	// Complete is set once the year is over.
	Complete bool `json:"complete"`

	Music      *MusicStats     `json:"music,omitempty"`
	Audiobooks *AudiobookStats `json:"audiobooks,omitempty"`
	Podcasts   *PodcastStats   `json:"podcasts,omitempty"`
	Tier       Tier            `json:"tier"`
}

type MusicStats struct {
	Listens int `json:"listens"`
	Minutes int `json:"minutes"`
	// MinutesEstimated is set when many listens had no known length and an
	// average song length stood in.
	MinutesEstimated bool `json:"minutesEstimated"`
	Artists          int  `json:"artists"`
	Tracks           int  `json:"tracks"`
	// Days is how many days had any music at all.
	Days          int         `json:"days"`
	TopArtists    []Ranked    `json:"topArtists"`
	TopTracks     []Ranked    `json:"topTracks"`
	TopAlbums     []Ranked    `json:"topAlbums"`
	Months        [12]int     `json:"months"` // minutes per month
	TopMonth      int         `json:"topMonth"`
	Hours         [24]int     `json:"hours"` // listens per hour of day
	Clock         Clock       `json:"clock"`
	BusiestDay    DayCount    `json:"busiestDay"`
	LongestStreak int         `json:"longestStreak"`
	OnRepeat      Repeat      `json:"onRepeat"`
	First         FirstListen `json:"first"`
}

type Ranked struct {
	Name    string `json:"name"`
	Artist  string `json:"artist,omitempty"`
	Plays   int    `json:"plays"`
	Minutes int    `json:"minutes"`
	Image   string `json:"image,omitempty"`
	// Library ids, when samo has it, so the page can show samo's own art.
	TrackID  string `json:"trackId,omitempty"`
	AlbumID  string `json:"albumId,omitempty"`
	ArtistID string `json:"artistId,omitempty"`
}

type DayCount struct {
	Date    string `json:"date"`
	Listens int    `json:"listens"`
}

type Repeat struct {
	Title   string `json:"title,omitempty"`
	Artist  string `json:"artist,omitempty"`
	Plays   int    `json:"plays"`
	Date    string `json:"date,omitempty"`
	Image   string `json:"image,omitempty"`
	TrackID string `json:"trackId,omitempty"`
	AlbumID string `json:"albumId,omitempty"`
}

type FirstListen struct {
	Title   string    `json:"title"`
	Artist  string    `json:"artist"`
	At      time.Time `json:"at"`
	Image   string    `json:"image,omitempty"`
	TrackID string    `json:"trackId,omitempty"`
	AlbumID string    `json:"albumId,omitempty"`
}

type Clock struct {
	Name     string `json:"name"`
	Blurb    string `json:"blurb"`
	PeakHour int    `json:"peakHour"`
}

type Tier struct {
	Name  string `json:"name"`
	Blurb string `json:"blurb"`
}

type AudiobookStats struct {
	Minutes  int      `json:"minutes"`
	Books    int      `json:"books"`
	Finished int      `json:"finished"`
	Top      []Ranked `json:"top"`
}

type PodcastStats struct {
	Episodes int      `json:"episodes"`
	Finished int      `json:"finished"`
	Shows    int      `json:"shows"`
	Top      []Ranked `json:"top"`
}
