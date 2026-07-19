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

// deezerSearchURL is a var so tests can point it at an httptest server.
var deezerSearchURL = "https://api.deezer.com/search/album"

// deezerTrackSearchURL is the song-level search endpoint (see
// lookupDeezerTrackCover), separately overridable in tests.
var deezerTrackSearchURL = "https://api.deezer.com/search/track"

// deezerMinInterval keeps the cover fallback far under Deezer's published 50
// requests / 5 seconds quota. Last rung of the chain, so volume is minimal.
const deezerMinInterval = 500 * time.Millisecond

// lookupDeezerAlbumCover searches Deezer's public album search for a cover.
// Same contract as the iTunes rung: both names must loosely match or it
// returns nothing, "" means a definitive miss, an error means retry later.
func lookupDeezerAlbumCover(ctx context.Context, client *http.Client, artist, album string) (string, error) {
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if artist == "" || album == "" {
		return "", nil
	}
	query := fmt.Sprintf(`artist:"%s" album:"%s"`, artist, album)
	return deezerAlbumCoverForQuery(ctx, client, deezerSearchURL, query, artist, album)
}

// lookupDeezerTrackCover searches Deezer at the SONG level and returns the
// matched track's album artwork — the compilation-proof rung for classic
// hits, mirroring lookupITunesTrackCover.
func lookupDeezerTrackCover(ctx context.Context, client *http.Client, artist, title string) (string, error) {
	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	if artist == "" || title == "" {
		return "", nil
	}
	query := fmt.Sprintf(`artist:"%s" track:"%s"`, artist, title)
	values := url.Values{
		"q":     {query},
		"limit": {"5"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deezerTrackSearchURL+"?"+values.Encode(), nil)
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
		return "", fmt.Errorf("deezer track search http %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Title  string `json:"title"`
			Artist struct {
				Name string `json:"name"`
			} `json:"artist"`
			Album struct {
				CoverXL  string `json:"cover_xl"`
				CoverBig string `json:"cover_big"`
			} `json:"album"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode deezer response: %w", err)
	}
	for _, result := range payload.Data {
		cover := result.Album.CoverXL
		if cover == "" {
			cover = result.Album.CoverBig
		}
		if cover == "" {
			continue
		}
		if !coverNamesMatch(result.Artist.Name, artist) || !coverNamesMatch(result.Title, title) {
			continue
		}
		return cover, nil
	}
	return "", nil
}

func deezerAlbumCoverForQuery(ctx context.Context, client *http.Client, endpoint, query, artist, album string) (string, error) {
	values := url.Values{
		"q":     {query},
		"limit": {"5"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
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
		return "", fmt.Errorf("deezer search http %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			Title    string `json:"title"`
			CoverXL  string `json:"cover_xl"`
			CoverBig string `json:"cover_big"`
			Artist   struct {
				Name string `json:"name"`
			} `json:"artist"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode deezer response: %w", err)
	}
	for _, result := range payload.Data {
		cover := result.CoverXL
		if cover == "" {
			cover = result.CoverBig
		}
		if cover == "" {
			continue
		}
		if !coverNamesMatch(result.Artist.Name, artist) || !coverNamesMatch(result.Title, album) {
			continue
		}
		return cover, nil
	}
	return "", nil
}
