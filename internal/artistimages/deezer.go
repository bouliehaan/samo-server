package artistimages

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/covers"
)

// fallbackHTTPClient backs the nil-client path. http.DefaultClient must never
// be used here: it has no timeout, so an upstream that accepts the connection
// and then goes silent parks the calling goroutine forever.
var fallbackHTTPClient = &http.Client{Timeout: 20 * time.Second}

const deezerAPIBase = "https://api.deezer.com/search/artist"

// deezerBlankPictureHash is the MD5 of the empty string, which Deezer puts in
// the image path of every artist it holds no photograph for. The URL is live
// and answers 200 with a 1000x1000 grey silhouette, so checking only for a
// non-empty field and a successful download stores the silhouette as the
// artist's photo and counts it a success.
const deezerBlankPictureHash = "d41d8cd98f00b204e9800998ecf8427e"

// deezerSearchLimit is deliberately more than one. Deezer's index carries
// duplicate and tribute entries under well-known names — a search for
// "Radiohead" can answer with a photoless duplicate ahead of the actual band —
// so the first hit is not reliably the artist that was asked about.
const deezerSearchLimit = 5

// exactNameScore outranks any follower count, so an exact name match always
// beats a more popular near-match. A tribute act with more listeners than the
// band it covers is still the wrong face.
const exactNameScore = int64(1) << 40

type deezerArtist struct {
	Name          string `json:"name"`
	PictureXL     string `json:"picture_xl"`
	PictureBig    string `json:"picture_big"`
	PictureMedium string `json:"picture_medium"`
	NbFan         int64  `json:"nb_fan"`
}

type deezerSearchResponse struct {
	Data []deezerArtist `json:"data"`
	// Deezer reports quota and service faults in-band, with HTTP 200 and an
	// error object in place of results. Kept raw because the same field is an
	// empty array on some endpoints, which would not decode into a struct.
	Error json.RawMessage `json:"error"`
}

// deezerArtistPictureURL returns the best artist photo Deezer holds for any of
// the supplied name spellings. The error it returns distinguishes a refusal or
// outage from an artist Deezer simply does not have, so a caller can avoid
// recording the former as a permanent absence.
func deezerArtistPictureURL(ctx context.Context, client *http.Client, names ...string) (string, error) {
	if client == nil {
		client = fallbackHTTPClient
	}
	seen := map[string]struct{}{}
	var blocked error
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}

		picture, err := deezerSearchArtistPicture(ctx, client, name)
		if err != nil {
			// Hold the first refusal but keep trying the other spellings: one
			// blocked request is not proof the next one fails.
			if blocked == nil && isTransientLookupError(err) {
				blocked = err
			}
			continue
		}
		if picture != "" {
			return picture, nil
		}
	}
	if blocked != nil {
		return "", blocked
	}
	return "", fmt.Errorf("no deezer artist picture for %q", strings.Join(names, ", "))
}

func deezerSearchArtistPicture(ctx context.Context, client *http.Client, name string) (string, error) {
	endpoint := fmt.Sprintf("%s?limit=%d&q=%s", deezerAPIBase, deezerSearchLimit, url.QueryEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Samo Server/0.1 ArtistImage")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &covers.HTTPStatusError{StatusCode: resp.StatusCode, URL: endpoint}
	}

	var payload deezerSearchResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if message, ok := deezerInBandError(payload.Error); ok {
		// Quota and service faults arrive with HTTP 200, so they have to be
		// promoted here or they read as "this artist does not exist".
		return "", &deezerAPIError{Message: message}
	}
	return pickDeezerArtistPicture(name, payload.Data), nil
}

// deezerInBandError reports the message from Deezer's HTTP 200 error object,
// if the field holds one.
func deezerInBandError(raw json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "[]" || trimmed == "{}" {
		return "", false
	}
	var detail struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return strings.TrimSpace(trimmed), true
	}
	message := strings.TrimSpace(detail.Message)
	if message == "" {
		message = strings.TrimSpace(detail.Type)
	}
	if message == "" {
		message = fmt.Sprintf("deezer error %d", detail.Code)
	}
	return message, true
}

// pickDeezerArtistPicture chooses the best usable photo among the search hits,
// preferring an exact name match and then the larger following. Hits whose only
// image is the blank silhouette are passed over entirely.
func pickDeezerArtistPicture(query string, items []deezerArtist) string {
	want := normalizeArtistKey(query)
	best := ""
	bestScore := int64(-1)
	for _, item := range items {
		picture := deezerPictureURL(item)
		if picture == "" {
			continue
		}
		score := item.NbFan
		if normalizeArtistKey(item.Name) == want {
			score += exactNameScore
		}
		if score > bestScore {
			bestScore = score
			best = picture
		}
	}
	return best
}

// deezerPictureURL returns the largest real photo on a search hit. Only the CDN
// sizes are considered: the bare `picture` field is an api.deezer.com redirect,
// which hides the blank-silhouette hash behind a 302 and so cannot be screened.
func deezerPictureURL(item deezerArtist) string {
	for _, candidate := range []string{item.PictureXL, item.PictureBig, item.PictureMedium} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || isDeezerBlankPicture(candidate) {
			continue
		}
		return candidate
	}
	return ""
}

func isDeezerBlankPicture(rawURL string) bool {
	return strings.Contains(strings.ToLower(rawURL), deezerBlankPictureHash)
}

func normalizeArtistKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}
