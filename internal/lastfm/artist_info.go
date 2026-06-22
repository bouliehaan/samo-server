package lastfm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const lastFMPlaceholderImageFragment = "2a96cbd8b46e442fc41c2b86b821562f"

type ArtistInfo struct {
	Name  string
	MBID  string
	Image string
}

func (c *Client) APIKeyConfigured() bool {
	return c != nil && strings.TrimSpace(c.apiKey) != ""
}

func (c *Client) GetArtistInfo(ctx context.Context, artistName, mbid string) (ArtistInfo, error) {
	if !c.APIKeyConfigured() {
		return ArtistInfo{}, ErrDisabled
	}
	artistName = strings.TrimSpace(artistName)
	mbid = strings.TrimSpace(mbid)
	if artistName == "" && mbid == "" {
		return ArtistInfo{}, ErrMissingMetadata
	}

	values := url.Values{}
	values.Set("method", "artist.getInfo")
	values.Set("api_key", c.apiKey)
	values.Set("format", "json")
	if mbid != "" {
		values.Set("mbid", mbid)
	} else {
		values.Set("artist", artistName)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.requestBaseURL()+"?"+values.Encode(),
		nil,
	)
	if err != nil {
		return ArtistInfo{}, err
	}
	req.Header.Set("User-Agent", "Samo Server/0.1 ArtistImage")

	client := c.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ArtistInfo{}, fmt.Errorf("last.fm artist.getInfo: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ArtistInfo{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ArtistInfo{}, fmt.Errorf("last.fm artist.getInfo http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
		Artist  struct {
			Name  string `json:"name"`
			MBID  string `json:"mbid"`
			Image []struct {
				Text string `json:"#text"`
				Size string `json:"size"`
			} `json:"image"`
		} `json:"artist"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ArtistInfo{}, fmt.Errorf("decode last.fm artist.getInfo: %w", err)
	}
	if envelope.Error != 0 {
		if envelope.Message == "" {
			envelope.Message = fmt.Sprintf("last.fm error %d", envelope.Error)
		}
		return ArtistInfo{}, fmt.Errorf("%s", envelope.Message)
	}

	info := ArtistInfo{
		Name:  strings.TrimSpace(envelope.Artist.Name),
		MBID:  strings.TrimSpace(envelope.Artist.MBID),
		Image: pickLastFMArtistImageURL(envelope.Artist.Image),
	}
	if info.Name == "" {
		info.Name = artistName
	}
	return info, nil
}

// ArtistMeta is the bio + similar-artist payload from artist.getInfo. Names are
// raw Last.fm display names; the caller resolves them against the local catalog.
type ArtistMeta struct {
	Biography    string
	SimilarNames []string
}

// GetArtistMeta returns the artist's biography and similar-artist names from a
// single artist.getInfo call (the same endpoint the image lookup uses). Empty
// fields are normal — not every artist has a bio or similar list on Last.fm.
func (c *Client) GetArtistMeta(ctx context.Context, artistName, mbid string) (ArtistMeta, error) {
	if !c.APIKeyConfigured() {
		return ArtistMeta{}, ErrDisabled
	}
	artistName = strings.TrimSpace(artistName)
	mbid = strings.TrimSpace(mbid)
	if artistName == "" && mbid == "" {
		return ArtistMeta{}, ErrMissingMetadata
	}

	values := url.Values{}
	values.Set("method", "artist.getInfo")
	values.Set("api_key", c.apiKey)
	values.Set("format", "json")
	values.Set("autocorrect", "1")
	if mbid != "" {
		values.Set("mbid", mbid)
	} else {
		values.Set("artist", artistName)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.requestBaseURL()+"?"+values.Encode(),
		nil,
	)
	if err != nil {
		return ArtistMeta{}, err
	}
	req.Header.Set("User-Agent", "Samo Server/0.1 ArtistMeta")

	client := c.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ArtistMeta{}, fmt.Errorf("last.fm artist.getInfo: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ArtistMeta{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ArtistMeta{}, fmt.Errorf("last.fm artist.getInfo http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
		Artist  struct {
			Bio struct {
				Content string `json:"content"`
				Summary string `json:"summary"`
			} `json:"bio"`
			Similar struct {
				Artist []struct {
					Name string `json:"name"`
				} `json:"artist"`
			} `json:"similar"`
		} `json:"artist"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ArtistMeta{}, fmt.Errorf("decode last.fm artist.getInfo: %w", err)
	}
	if envelope.Error != 0 {
		if envelope.Message == "" {
			envelope.Message = fmt.Sprintf("last.fm error %d", envelope.Error)
		}
		return ArtistMeta{}, fmt.Errorf("%s", envelope.Message)
	}

	meta := ArtistMeta{Biography: cleanLastFMBio(envelope.Artist.Bio.Content)}
	if meta.Biography == "" {
		meta.Biography = cleanLastFMBio(envelope.Artist.Bio.Summary)
	}
	for _, similar := range envelope.Artist.Similar.Artist {
		if name := strings.TrimSpace(similar.Name); name != "" {
			meta.SimilarNames = append(meta.SimilarNames, name)
		}
	}
	return meta, nil
}

// cleanLastFMBio strips the boilerplate "Read more on Last.fm" attribution link
// Last.fm appends to every bio, and trims surrounding whitespace. Last.fm's
// licence requires showing the link only when displaying their bio verbatim; we
// surface plain prose, so the trailing anchor is removed.
func cleanLastFMBio(raw string) string {
	text := strings.TrimSpace(raw)
	if idx := strings.Index(text, "<a href=\"https://www.last.fm"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	return text
}

func pickLastFMArtistImageURL(images []struct {
	Text string `json:"#text"`
	Size string `json:"size"`
}) string {
	if len(images) == 0 {
		return ""
	}

	sizeRank := map[string]int{
		"mega":       6,
		"extralarge": 5,
		"large":      4,
		"medium":     3,
		"small":      2,
		"":           1,
	}
	bestURL := ""
	bestRank := -1
	for _, image := range images {
		candidate := strings.TrimSpace(image.Text)
		if candidate == "" || isLastFMPlaceholderArtistImage(candidate) {
			continue
		}
		rank := sizeRank[strings.ToLower(strings.TrimSpace(image.Size))]
		if rank == 0 {
			rank = 1
		}
		if rank > bestRank {
			bestRank = rank
			bestURL = candidate
		}
	}
	return bestURL
}

func isLastFMPlaceholderArtistImage(rawURL string) bool {
	rawURL = strings.ToLower(strings.TrimSpace(rawURL))
	if rawURL == "" {
		return true
	}
	return strings.Contains(rawURL, lastFMPlaceholderImageFragment)
}
