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
