package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RecentListen is one scrobble as user.getRecentTracks reports it: every
// listen on the account, from samo or anything else that scrobbles to it.
type RecentListen struct {
	Artist string
	Album  string
	Track  string
	At     time.Time
	// Image is Last.fm's album art for the scrobble, when it has some.
	Image string
}

// RecentTracksPage is one page of a user's scrobbles in [from, to).
type RecentTracksPage struct {
	Listens    []RecentListen
	Page       int
	TotalPages int
	Total      int
}

// GetRecentTracks reads one page (up to 200) of a user's scrobbles between
// from and to. It needs only the application's API key: listening history is
// public on Last.fm, and no session is involved.
func (c *Client) GetRecentTracks(ctx context.Context, username string, from, to time.Time, page int) (RecentTracksPage, error) {
	if !c.APIKeyConfigured() {
		return RecentTracksPage{}, ErrDisabled
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return RecentTracksPage{}, ErrNotConnected
	}
	if page < 1 {
		page = 1
	}
	values := url.Values{}
	values.Set("method", "user.getRecentTracks")
	values.Set("user", username)
	values.Set("api_key", c.apiKey)
	values.Set("format", "json")
	values.Set("limit", "200")
	values.Set("page", strconv.Itoa(page))
	if !from.IsZero() {
		values.Set("from", strconv.FormatInt(from.Unix(), 10))
	}
	if !to.IsZero() {
		values.Set("to", strconv.FormatInt(to.Unix(), 10))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.requestBaseURL()+"?"+values.Encode(), nil)
	if err != nil {
		return RecentTracksPage{}, err
	}
	req.Header.Set("User-Agent", "Samo Server/0.1 Wrapped")
	client := c.http
	if client == nil {
		client = fallbackHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return RecentTracksPage{}, fmt.Errorf("last.fm user.getRecentTracks: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return RecentTracksPage{}, err
	}
	var envelope recentTracksEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return RecentTracksPage{}, fmt.Errorf("last.fm user.getRecentTracks http %d", resp.StatusCode)
		}
		return RecentTracksPage{}, fmt.Errorf("decode last.fm user.getRecentTracks: %w", err)
	}
	if envelope.Error != 0 {
		return RecentTracksPage{}, wrapAPIError(envelope.Error, envelope.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RecentTracksPage{}, fmt.Errorf("last.fm user.getRecentTracks http %d", resp.StatusCode)
	}

	out := RecentTracksPage{
		Page:       atoiOr(envelope.RecentTracks.Attr.Page, page),
		TotalPages: atoiOr(envelope.RecentTracks.Attr.TotalPages, 0),
		Total:      atoiOr(envelope.RecentTracks.Attr.Total, 0),
	}
	for _, entry := range envelope.RecentTracks.Track {
		// The track playing right now is listed first with no date; it has
		// not been scrobbled yet.
		if entry.Attr.NowPlaying == "true" || entry.Date.UTS == "" {
			continue
		}
		uts, err := strconv.ParseInt(entry.Date.UTS, 10, 64)
		if err != nil || uts <= 0 {
			continue
		}
		listen := RecentListen{
			Artist: strings.TrimSpace(entry.Artist.Text),
			Album:  strings.TrimSpace(entry.Album.Text),
			Track:  strings.TrimSpace(entry.Name),
			At:     time.Unix(uts, 0).UTC(),
			Image:  pickLastFMArtistImageURL(entry.Image),
		}
		if listen.Artist == "" || listen.Track == "" {
			continue
		}
		out.Listens = append(out.Listens, listen)
	}
	return out, nil
}

type recentTracksEnvelope struct {
	Error        int    `json:"error"`
	Message      string `json:"message"`
	RecentTracks struct {
		Track recentTrackList `json:"track"`
		Attr  struct {
			Page       string `json:"page"`
			TotalPages string `json:"totalPages"`
			Total      string `json:"total"`
		} `json:"@attr"`
	} `json:"recenttracks"`
}

type recentTrackEntry struct {
	Name   string `json:"name"`
	Artist struct {
		Text string `json:"#text"`
	} `json:"artist"`
	Album struct {
		Text string `json:"#text"`
	} `json:"album"`
	Date struct {
		UTS string `json:"uts"`
	} `json:"date"`
	Image []struct {
		Text string `json:"#text"`
		Size string `json:"size"`
	} `json:"image"`
	Attr struct {
		NowPlaying string `json:"nowplaying"`
	} `json:"@attr"`
}

// recentTrackList accepts Last.fm's habit of sending a lone result as an
// object rather than a one-element array.
type recentTrackList []recentTrackEntry

func (l *recentTrackList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var one recentTrackEntry
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		*l = recentTrackList{one}
		return nil
	}
	var many []recentTrackEntry
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

func atoiOr(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

// LinkedUsername is the Last.fm account a samo user has linked.
func (s *Service) LinkedUsername(ctx context.Context, userID string) (string, bool) {
	if s == nil || !s.Enabled() {
		return "", false
	}
	session, err := loadSession(ctx, s.db, userID)
	if err != nil || strings.TrimSpace(session.Username) == "" {
		return "", false
	}
	return session.Username, true
}

// maxRecentTrackPages bounds one read at 50,000 scrobbles: a very full year.
const maxRecentTrackPages = 250

// UserListens reads every scrobble a samo user's linked Last.fm account made
// between from and to, oldest first, calling progress after each page.
// Last.fm asks for no more than about five requests a second.
func (s *Service) UserListens(ctx context.Context, userID string, from, to time.Time, progress func(page, pages int)) ([]RecentListen, error) {
	username, ok := s.LinkedUsername(ctx, userID)
	if !ok {
		return nil, ErrNotConnected
	}
	client, ok := s.ActiveClient()
	if !ok {
		return nil, ErrDisabled
	}
	var listens []RecentListen
	for page := 1; page <= maxRecentTrackPages; page++ {
		result, err := client.GetRecentTracks(ctx, username, from, to, page)
		if err != nil {
			return nil, err
		}
		listens = append(listens, result.Listens...)
		if progress != nil {
			progress(page, result.TotalPages)
		}
		if result.TotalPages <= page || len(result.Listens) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(220 * time.Millisecond):
		}
	}
	// Last.fm lists newest first.
	for i, j := 0, len(listens)-1; i < j; i, j = i+1, j-1 {
		listens[i], listens[j] = listens[j], listens[i]
	}
	return listens, nil
}
