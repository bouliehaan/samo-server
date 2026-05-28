package artistimages

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const deezerAPIBase = "https://api.deezer.com/search/artist"

type deezerSearchResponse struct {
	Data []struct {
		Name      string `json:"name"`
		PictureXL string `json:"picture_xl"`
		Picture   string `json:"picture"`
	} `json:"data"`
}

func deezerArtistPictureURL(ctx context.Context, client *http.Client, names ...string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	seen := map[string]struct{}{}
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
			continue
		}
		if picture != "" {
			return picture, nil
		}
	}
	return "", fmt.Errorf("no deezer artist picture for %q", strings.Join(names, ", "))
}

func deezerSearchArtistPicture(ctx context.Context, client *http.Client, name string) (string, error) {
	endpoint := deezerAPIBase + "?limit=1&q=" + url.QueryEscape(name)
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
		return "", fmt.Errorf("deezer search http %d", resp.StatusCode)
	}

	var payload deezerSearchResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if len(payload.Data) == 0 {
		return "", nil
	}
	picture := strings.TrimSpace(payload.Data[0].PictureXL)
	if picture == "" {
		picture = strings.TrimSpace(payload.Data[0].Picture)
	}
	return picture, nil
}
