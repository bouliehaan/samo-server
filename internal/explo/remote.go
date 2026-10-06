package explo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Remote connects to the optional samo-explo acquisition API. Credentials stay
// on the server; redirects are forbidden so they cannot forward the token.
type Remote struct {
	baseURL, token string
	client         *http.Client
}
type RemoteStatus struct {
	Service    string   `json:"service"`
	Version    int      `json:"version"`
	Configured bool     `json:"configured"`
	Providers  []string `json:"providers"`
	// AlbumProviders is the order an album's tracks try the providers in,
	// which is not the songs' (Soulseek first, for whole FLAC albums). An
	// older Explo leaves it out.
	AlbumProviders []string `json:"albumProviders,omitempty"`
	// Albums is true when Explo can search and download whole albums.
	Albums bool `json:"albums"`
	// Artists is true when Explo can search artists and list what they
	// released, to add an album from.
	Artists bool `json:"artists"`
	// Playlists is true when Explo can read YouTube Music playlists.
	Playlists bool `json:"playlists"`
}
type Song struct {
	// ID is a MusicBrainz recording, or deezer-<n> for a track only Deezer
	// lists (see catalog_ids.go). Source says which catalog.
	ID         string `json:"id"`
	Source     string `json:"source,omitempty"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album,omitempty"`
	DurationMS int    `json:"durationMs,omitempty"`
	// AlbumID is the album the song is shown with, for its cover: a
	// MusicBrainz release group or deezer-<n>.
	AlbumID string `json:"albumId,omitempty"`
	// ISRC identifies the recording across stores, when the catalog has it.
	ISRC string `json:"isrc,omitempty"`
	// AlbumTrack places the song on an album requested whole. Explo sets it
	// only on an album's own track list, so a song asked for on its own is
	// never filed as part of an album it merely appears on.
	AlbumTrack *AlbumTrack `json:"albumTrack,omitempty"`
}
type SongResults struct {
	Songs []Song `json:"songs"`
}

// AlbumTrack is where a track of a whole-album request goes: the album, by
// its MusicBrainz release group, and the track's place on it.
type AlbumTrack struct {
	AlbumID        string `json:"albumId"`
	AlbumTitle     string `json:"albumTitle"`
	AlbumArtist    string `json:"albumArtist"`
	ReleaseID      string `json:"releaseId,omitempty"`
	ReleaseTrackID string `json:"releaseTrackId,omitempty"`
	Number         int    `json:"number"`
	TrackTotal     int    `json:"trackTotal,omitempty"`
	Disc           int    `json:"disc"`
	DiscTotal      int    `json:"discTotal,omitempty"`
	Year           int    `json:"year,omitempty"`
}

// Album is a MusicBrainz release group as Explo's album search reports it.
// Tracks is filled only by the album lookup.
type Album struct {
	// ID is a MusicBrainz release group or deezer-<n>.
	ID             string   `json:"id"`
	Source         string   `json:"source,omitempty"`
	Title          string   `json:"title"`
	Artist         string   `json:"artist"`
	Type           string   `json:"type,omitempty"`
	SecondaryTypes []string `json:"secondaryTypes,omitempty"`
	Year           int      `json:"year,omitempty"`
	ReleaseID      string   `json:"releaseId,omitempty"`
	Tracks         []Song   `json:"tracks,omitempty"`
}
type AlbumResults struct {
	Albums []Album `json:"albums"`
}

// Artist is an artist as Explo finds them: MusicBrainz's, or deezer-<n> for
// one only Deezer lists.
type Artist struct {
	ID             string `json:"id"`
	Source         string `json:"source,omitempty"`
	Name           string `json:"name"`
	Disambiguation string `json:"disambiguation,omitempty"`
	Type           string `json:"type,omitempty"`
	Country        string `json:"country,omitempty"`
}
type ArtistResults struct {
	Artists []Artist `json:"artists"`
}

// Discography is what an artist released, studio albums first, each an album
// that can be opened and added like an album search result.
type Discography struct {
	Artist Artist  `json:"artist"`
	Albums []Album `json:"albums"`
}

// Playlist is a YouTube Music playlist as Explo reads it. Each track's ID is
// youtube-<video>; Album is set only on a track YouTube Music lists as a song,
// whose names are the label's (see listingSource).
type Playlist struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Tracks []Song `json:"tracks"`
	// Unavailable is how many tracks YouTube Music lists but cannot play.
	Unavailable int `json:"unavailable,omitempty"`
}
type DownloadJob struct {
	ID        string            `json:"id"`
	Song      Song              `json:"song"`
	State     string            `json:"state"`
	Message   string            `json:"message,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Phase     string            `json:"phase,omitempty"`
	Progress  float64           `json:"progress,omitempty"`
	Attempts  []DownloadAttempt `json:"attempts,omitempty"`
	UpdatedAt time.Time         `json:"updatedAt"`
	// File is where Explo staged the download, relative to its drop folder.
	// Samo uses it to find the track; it is not passed on to browsers.
	File string `json:"file,omitempty"`
	// Library is samo's side of the request once the download is staged:
	// identification and the keep into the library. Never sent by Explo.
	Library *SongRequest `json:"library,omitempty"`
}
type DownloadAttempt struct {
	Provider string `json:"provider"`
	Phase    string `json:"phase"`
	Message  string `json:"message"`
}
type RemoteError struct {
	Status  int
	Message string
}

func (e *RemoteError) Error() string { return e.Message }
func NewRemote(baseURL, token string) *Remote {
	baseURL, token = strings.TrimRight(strings.TrimSpace(baseURL), "/"), strings.TrimSpace(token)
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || token == "" {
		return nil
	}
	return &Remote{baseURL: baseURL, token: token, client: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Remote) call(ctx context.Context, method, path string, input, output any) error {
	if c == nil {
		return ErrDisabled
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api/samo/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return &RemoteError{503, "Explo is disconnected. Please try again later."}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Never relay upstream diagnostics, which might include credentials/paths.
		code, message := http.StatusBadGateway, "Explo could not complete this request."
		switch resp.StatusCode {
		case 400:
			code, message = 400, "Invalid song search or download request."
		case 404:
			code, message = 404, "This search or download expired. Search again or check your library."
		case 429:
			code, message = 429, "Explo is busy. Please try again later."
		case 401, 403, 503:
			code, message = 503, "Explo is unavailable or not configured."
		}
		return &RemoteError{code, message}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(output); err != nil {
		return &RemoteError{502, "Invalid response from Explo."}
	}
	return nil
}
func (c *Remote) Status(ctx context.Context) (RemoteStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var status RemoteStatus
	err := c.call(ctx, "GET", "status", nil, &status)
	if err == nil && (status.Service != "samo-explo" || status.Version != 1) {
		err = fmt.Errorf("unsupported Explo API")
	}
	return status, err
}
func (c *Remote) Search(ctx context.Context, q string) (SongResults, error) {
	result := SongResults{Songs: []Song{}}
	err := c.call(ctx, "GET", "search?q="+url.QueryEscape(q), nil, &result)
	return result, err
}
func (c *Remote) Add(ctx context.Context, id string, provider ...string) (DownloadJob, error) {
	var selected string
	if len(provider) > 0 {
		selected = provider[0]
	}
	return c.AddAlbumTrack(ctx, id, selected, "")
}

// AddAlbumTrack queues a track of an album opened with Album, so Explo
// downloads and tags it as that album's track. An empty album is Add.
func (c *Remote) AddAlbumTrack(ctx context.Context, id, provider, album string) (DownloadJob, error) {
	return c.addDownload(ctx, map[string]string{"id": id, "provider": provider, "album": album})
}

// AddPlaylistTrack queues a track of a YouTube Music playlist read with
// Playlist. Explo reads the playlist again if it has forgotten it.
func (c *Remote) AddPlaylistTrack(ctx context.Context, id, playlist string) (DownloadJob, error) {
	return c.addDownload(ctx, map[string]string{"id": id, "playlist": playlist})
}

func (c *Remote) addDownload(ctx context.Context, input map[string]string) (DownloadJob, error) {
	for key, value := range input {
		if value == "" && key != "id" {
			delete(input, key)
		}
	}
	var job DownloadJob
	err := c.call(ctx, "POST", "downloads", input, &job)
	return job, err
}

// Playlist reads a public YouTube Music playlist by its id (the list=
// parameter of its link).
func (c *Remote) Playlist(ctx context.Context, id string) (Playlist, error) {
	var playlist Playlist
	err := c.call(ctx, "GET", "playlists/"+url.PathEscape(id), nil, &playlist)
	return playlist, err
}

func (c *Remote) SearchAlbums(ctx context.Context, q string) (AlbumResults, error) {
	result := AlbumResults{Albums: []Album{}}
	err := c.call(ctx, "GET", "albums?q="+url.QueryEscape(q), nil, &result)
	return result, err
}

func (c *Remote) SearchArtists(ctx context.Context, q string) (ArtistResults, error) {
	result := ArtistResults{Artists: []Artist{}}
	err := c.call(ctx, "GET", "artists?q="+url.QueryEscape(q), nil, &result)
	return result, err
}

// Artist lists what an artist released.
func (c *Remote) Artist(ctx context.Context, id string) (Discography, error) {
	result := Discography{Albums: []Album{}}
	err := c.call(ctx, "GET", "artists/"+url.PathEscape(id), nil, &result)
	return result, err
}

// Album looks up the track list of an album from a recent album search.
func (c *Remote) Album(ctx context.Context, id string) (Album, error) {
	var album Album
	err := c.call(ctx, "GET", "albums/"+url.PathEscape(id), nil, &album)
	return album, err
}
func (c *Remote) Job(ctx context.Context, id string) (DownloadJob, error) {
	var job DownloadJob
	err := c.call(ctx, "GET", "downloads/"+url.PathEscape(id), nil, &job)
	return job, err
}
