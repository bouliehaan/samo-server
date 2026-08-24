package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

/*
What a station is airing right now, for anything that has to put it on a screen.

The ICY probe next door answers a different question. It is a health check —
"is this stream alive, and what did it say when we last looked" — and it pays
for the answer by opening a stream connection every ten minutes. Read as
now-playing that is wrong in both directions: too slow to catch a three-minute
song, and too expensive to speed up, because for a metered relay the audio pull
IS the cost. Turning the probe into a metadata poller would have made a health
check into the noisiest thing in the setup.

So this is the other half: for a station that publishes a plain JSON document
saying what is on, ask that instead. It costs the origin a local read, it can be
asked as often as anybody actually looks, and it never touches the audio path.
Stations without one keep the probe's cached line, which is what they had.
*/

// liveMetadataTTL is how long an answer stands before it is asked again.
//
// Matched to how often anybody looks: the radio daemon and the wall each poll
// now-playing every ten seconds, so a shorter TTL would fetch on every single
// poll and a much longer one would show the previous song. Refresh is demand
// driven — nothing here runs on a timer — so a station nobody is listening to
// costs nothing at all.
const liveMetadataTTL = 10 * time.Second

// liveMetadataTimeout bounds one fetch. The endpoint is a local read on the
// other end; if it has not answered in three seconds the card is better off
// with the previous line than with a stalled poll behind it.
const liveMetadataTimeout = 3 * time.Second

// liveMetadataMaxBytes caps the document. Now-playing JSON is a few hundred
// bytes; anything approaching this is not the endpoint we were promised.
const liveMetadataMaxBytes = 64 * 1024

// LiveNowPlaying is what a station is putting out this second.
//
// ArtworkURL is either absolute (somebody else's server, or a bridge re-serving
// the current cover) or a samo-relative path like "/api/v1/media/covers/x/image".
// Both shapes reach clients as-is: the ones that render it already have a base
// URL to join a relative path to, and joining here would mean this package
// inventing the server's own public address.
type LiveNowPlaying struct {
	Title      string
	Artist     string
	Album      string
	ArtworkURL string
	// Live is false when this came from the probe's cached line rather than a
	// metadata endpoint, so a caller can tell "this is current" from "this is
	// the last thing we happened to see".
	Live      bool
	UpdatedAt time.Time
}

// Empty reports whether there is nothing here worth showing.
func (l LiveNowPlaying) Empty() bool {
	return l.Title == "" && l.Artist == "" && l.Album == "" && l.ArtworkURL == ""
}

type liveMetadataEntry struct {
	mu        sync.RWMutex
	value     LiveNowPlaying
	ok        bool
	fetchedAt time.Time
	// fetching serialises refreshes for one station, so the two pollers that
	// arrive together produce one request rather than two.
	fetching sync.Mutex
}

// liveMetadataCache holds one entry per station asked about.
//
// Bounded by the number of stations that are actually aired, which is a handful
// — a station nobody tunes never gets an entry, and an entry is two short
// strings and a timestamp.
type liveMetadataCache struct {
	mu      sync.Mutex
	entries map[string]*liveMetadataEntry
}

func (c *liveMetadataCache) entry(stationID string) *liveMetadataEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*liveMetadataEntry{}
	}
	if found, ok := c.entries[stationID]; ok {
		return found
	}
	created := &liveMetadataEntry{}
	c.entries[stationID] = created
	return created
}

// CachedStationMetadata returns the last known answer without any I/O.
//
// For the callers that must not block — the list endpoint that reports what
// every channel is airing in one round trip, and anything else that would turn
// a page load into one HTTP request per station.
func (s *Service) CachedStationMetadata(stationID string) (LiveNowPlaying, bool) {
	if s == nil {
		return LiveNowPlaying{}, false
	}
	stationID = strings.TrimSpace(stationID)
	if stationID == "" {
		return LiveNowPlaying{}, false
	}
	entry := s.liveMeta.entry(stationID)
	entry.mu.RLock()
	defer entry.mu.RUnlock()
	if !entry.ok {
		return LiveNowPlaying{}, false
	}
	return entry.value, true
}

// LiveStationMetadata reports what the station is airing, refreshing when the
// last answer has gone stale.
//
// Never returns a half-answer: a station whose endpoint is unreachable, or
// which is between tracks and saying nothing, reports ok=false so the caller
// falls back to the station's own name rather than rendering a blank line.
func (s *Service) LiveStationMetadata(ctx context.Context, stationID string) (LiveNowPlaying, bool) {
	if s == nil || s.db == nil {
		return LiveNowPlaying{}, false
	}
	stationID = strings.TrimSpace(stationID)
	if stationID == "" {
		return LiveNowPlaying{}, false
	}
	entry := s.liveMeta.entry(stationID)

	entry.mu.RLock()
	fresh := entry.ok && time.Since(entry.fetchedAt) < liveMetadataTTL
	cached := entry.value
	entry.mu.RUnlock()
	if fresh {
		return cached, true
	}

	// One refresh at a time per station. Whoever loses the race re-reads the
	// winner's answer rather than repeating the request.
	entry.fetching.Lock()
	defer entry.fetching.Unlock()

	entry.mu.RLock()
	fresh = entry.ok && time.Since(entry.fetchedAt) < liveMetadataTTL
	cached = entry.value
	entry.mu.RUnlock()
	if fresh {
		return cached, true
	}

	station, err := s.GetInternetRadioStation(ctx, stationID)
	if err != nil {
		return s.keepPrevious(entry)
	}
	resolved, ok := s.resolveLiveMetadata(ctx, station)
	if !ok {
		return s.keepPrevious(entry)
	}

	entry.mu.Lock()
	entry.value = resolved
	entry.ok = true
	entry.fetchedAt = time.Now()
	entry.mu.Unlock()
	return resolved, true
}

// keepPrevious holds the last good answer through a failed refresh.
//
// A station that drops one poll has not stopped playing, and blanking the card
// for a single timeout is a visible flicker on a wall for something nobody can
// act on. The entry's timestamp is left alone so the next call retries.
func (s *Service) keepPrevious(entry *liveMetadataEntry) (LiveNowPlaying, bool) {
	entry.mu.RLock()
	defer entry.mu.RUnlock()
	if !entry.ok {
		return LiveNowPlaying{}, false
	}
	return entry.value, true
}

// resolveLiveMetadata turns a station row into what it is airing.
//
// Order matters: the metadata endpoint is asked first because it is the only
// source here that is actually CURRENT. The probe's cached line is a fallback
// for stations with no endpoint, and artwork degrades separately from text —
// losing the picture must not cost the title.
func (s *Service) resolveLiveMetadata(ctx context.Context, station InternetRadioStation) (LiveNowPlaying, bool) {
	now := LiveNowPlaying{}

	if endpoint := strings.TrimSpace(station.MetadataURL); endpoint != "" {
		if fetched, err := s.fetchLiveMetadata(ctx, endpoint); err == nil {
			now.Title = fetched.Title
			now.Artist = fetched.Artist
			now.Album = fetched.Album
			now.ArtworkURL = fetched.ArtworkURL
			now.Live = true
		}
	}

	// No endpoint, or it told us nothing: the probe's line is what we have.
	if now.Title == "" && now.Artist == "" && station.NowPlaying != nil {
		now.Title = blankSentinel(station.NowPlaying.Title)
		now.Artist = blankSentinel(station.NowPlaying.Artist)
		if now.Title == "" {
			now.Title = blankSentinel(station.NowPlaying.Raw)
		}
		if station.NowPlaying.UpdatedAt != nil {
			now.UpdatedAt = *station.NowPlaying.UpdatedAt
		}
	}

	if now.ArtworkURL == "" {
		now.ArtworkURL = stationArtwork(station, now.Title, now.Artist)
	}
	if now.Empty() {
		return LiveNowPlaying{}, false
	}
	if now.UpdatedAt.IsZero() {
		now.UpdatedAt = time.Now()
	}
	return now, true
}

// stationArtwork picks the best picture available for a station.
//
// The per-track URL first — a bridge re-serving the cover of whatever is on air
// says more than a logo — then the cover uploaded into samo, then the logo the
// directory supplied. The last two are fixed pictures of the station itself,
// which is the right answer when nothing knows the track.
func stationArtwork(station InternetRadioStation, title, artist string) string {
	if perTrack := strings.TrimSpace(station.MetadataArtworkURL); perTrack != "" {
		return bustCache(perTrack, title, artist)
	}
	if coverID := strings.TrimSpace(station.CoverID); coverID != "" {
		return "/api/v1/media/covers/" + coverID + "/image"
	}
	return strings.TrimSpace(station.ImageURL)
}

// bustCache makes a fixed artwork URL change when the track does.
//
// The per-track endpoint serves a DIFFERENT picture from the same address every
// few minutes, which is exactly what every cache between here and a screen is
// built to defeat. Tagging the URL with the identity of what is playing means
// the address changes when the picture does, so a stale cover cannot outlive
// its song — and an unchanged track keeps hitting the same cached entry.
func bustCache(rawURL, title, artist string) string {
	identity := strings.TrimSpace(title) + "\x00" + strings.TrimSpace(artist)
	if strings.TrimSpace(identity) == "\x00" {
		return rawURL
	}
	digest := fnv.New32a()
	_, _ = digest.Write([]byte(identity))
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%sv=%08x", rawURL, separator, digest.Sum32())
}

// liveMetadataDocument is the shape a metadata endpoint may answer with.
//
// Deliberately permissive about names. There is no standard here — every
// bridge and every streaming panel spells these differently — and accepting the
// handful of spellings in circulation is what keeps this generic instead of
// built around one particular upstream. Unknown fields are ignored.
type liveMetadataDocument struct {
	Title       string `json:"title"`
	Song        string `json:"song"`
	Track       string `json:"track"`
	StreamTitle string `json:"streamTitle"`

	Artist     string `json:"artist"`
	ArtistName string `json:"artistName"`

	Album string `json:"album"`

	ArtworkURL string `json:"artworkUrl"`
	CoverURL   string `json:"coverUrl"`
	Cover      string `json:"cover"`
	Image      string `json:"image"`
	ImageURL   string `json:"imageUrl"`
}

func (d liveMetadataDocument) resolve() LiveNowPlaying {
	return LiveNowPlaying{
		Title:      firstMeaningful(d.Title, d.Song, d.Track, d.StreamTitle),
		Artist:     firstMeaningful(d.Artist, d.ArtistName),
		Album:      firstMeaningful(d.Album),
		ArtworkURL: firstMeaningful(d.ArtworkURL, d.CoverURL, d.Cover, d.Image, d.ImageURL),
	}
}

// fetchLiveMetadata reads one now-playing document.
func (s *Service) fetchLiveMetadata(ctx context.Context, endpoint string) (LiveNowPlaying, error) {
	ctx, cancel := context.WithTimeout(ctx, liveMetadataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return LiveNowPlaying{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Samo Server/0.1 NowPlaying")

	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return LiveNowPlaying{}, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LiveNowPlaying{}, fmt.Errorf("metadata endpoint status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, liveMetadataMaxBytes))
	if err != nil {
		return LiveNowPlaying{}, err
	}
	var document liveMetadataDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return LiveNowPlaying{}, fmt.Errorf("metadata endpoint is not JSON: %w", err)
	}
	return document.resolve(), nil
}

// blankSentinel treats the ways an endpoint says "nothing" as nothing.
//
// A bridge with no channel selected answers "-" in every field rather than
// omitting them, and a card reading "- by -" is worse than one that admits it
// does not know. Only whole-field sentinels count: a song really called "N/A"
// is not a thing, but a dash inside a title certainly is.
func blankSentinel(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "-", "--", "---", "n/a", "na", "unknown", "null", "none":
		return ""
	}
	return trimmed
}

func firstMeaningful(values ...string) string {
	for _, value := range values {
		if cleaned := blankSentinel(value); cleaned != "" {
			return cleaned
		}
	}
	return ""
}
