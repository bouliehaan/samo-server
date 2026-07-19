package explo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// itunesSearchURL is a var so tests can point it at an httptest server.
var itunesSearchURL = "https://itunes.apple.com/search"

// itunesMinInterval keeps the cover fallback well under Apple's documented
// ~20 calls/minute guidance for the unauthenticated Search API. Only reached
// when both Cover Art Archive rungs missed, so real volume is a handful of
// requests per weekly drop.
const itunesMinInterval = 3500 * time.Millisecond

// lookupITunesAlbumCover searches the iTunes Search API for an album cover.
// It returns a URL only when a result's artist AND album name loosely match
// what identification produced - a wrong cover is worse than the placeholder,
// so ambiguity means no match. The 100x100 thumbnail URL Apple returns is
// rewritten to the 600x600 rendition (a stable, documented URL pattern).
// Returns "" with no error when nothing matched; errors are transient
// (network/API) and mean "retry later".
func lookupITunesAlbumCover(ctx context.Context, client *http.Client, artist, album string) (string, error) {
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if artist == "" || album == "" {
		return "", nil
	}
	values := url.Values{
		"term":   {artist + " " + album},
		"entity": {"album"},
		"limit":  {"5"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, itunesSearchURL+"?"+values.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("itunes search http %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			ArtistName     string `json:"artistName"`
			CollectionName string `json:"collectionName"`
			ArtworkURL100  string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode itunes response: %w", err)
	}
	for _, result := range payload.Results {
		if result.ArtworkURL100 == "" {
			continue
		}
		if !coverNamesMatch(result.ArtistName, artist) || !coverNamesMatch(result.CollectionName, album) {
			continue
		}
		return strings.Replace(result.ArtworkURL100, "100x100", "600x600", 1), nil
	}
	return "", nil
}

// lookupITunesTrackCover searches the iTunes Search API at the SONG level and
// returns the matching track's artwork. This is the rung that rescues classic
// hits: their MusicBrainz recordings often sit only on compilation release
// groups (so every album-identity rung yields sampler art or nothing), but a
// song search by artist + title returns the canonical release's artwork
// directly. Same trust gate as the album rung: both names must loosely match.
func lookupITunesTrackCover(ctx context.Context, client *http.Client, artist, title string) (string, error) {
	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	if artist == "" || title == "" {
		return "", nil
	}
	values := url.Values{
		"term":   {artist + " " + title},
		"entity": {"song"},
		"limit":  {"5"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, itunesSearchURL+"?"+values.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("itunes song search http %d", resp.StatusCode)
	}
	var payload struct {
		Results []struct {
			ArtistName    string `json:"artistName"`
			TrackName     string `json:"trackName"`
			ArtworkURL100 string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode itunes response: %w", err)
	}
	for _, result := range payload.Results {
		if result.ArtworkURL100 == "" {
			continue
		}
		if !coverNamesMatch(result.ArtistName, artist) || !coverNamesMatch(result.TrackName, title) {
			continue
		}
		return strings.Replace(result.ArtworkURL100, "100x100", "600x600", 1), nil
	}
	return "", nil
}

// coverNamesMatch is the loose equality gate for text-searched cover sources:
// case/punctuation-insensitive, and tolerant of decorations one side adds
// ("Album (Deluxe Edition)" matches "Album") via containment either way.
// Deliberately NOT fuzzy - containment of the full normalized name is the
// weakest match that still can't confuse two different records.
func coverNamesMatch(a, b string) bool {
	na, nb := normalizeCoverName(a), normalizeCoverName(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb || strings.Contains(na, nb) || strings.Contains(nb, na)
}

func normalizeCoverName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'à' && r <= 'ž':
			// Keep accented letters rather than dropping them: two names that
			// differ only in accents still normalize equal to themselves, and
			// dropping them would over-merge short names.
			b.WriteRune(r)
		}
	}
	return b.String()
}
