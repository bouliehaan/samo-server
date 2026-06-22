package artistmeta

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	deezerSearchArtistURL = "https://api.deezer.com/search/artist"
	deezerArtistBaseURL   = "https://api.deezer.com/artist"
)

// deezerSimilarArtists returns related artists (name + picture URL) from Deezer
// (no API key required): search the artist by name to get its Deezer id, then
// read that artist's /related list. Deezer carries real artist pictures, so its
// results power the EXTERNAL tiles (artists not in this library). Returns nil on
// any failure — callers treat an empty result as "no similar from this provider".
func deezerSimilarArtists(ctx context.Context, client *http.Client, name string) []similarCandidate {
	if client == nil {
		client = http.DefaultClient
	}
	deezerID, err := deezerArtistID(ctx, client, name)
	if err != nil || deezerID == "" {
		return nil
	}

	endpoint := deezerArtistBaseURL + "/" + url.PathEscape(deezerID) + "/related?limit=20"
	var payload struct {
		Data []struct {
			Name          string `json:"name"`
			Picture       string `json:"picture"`
			PictureMedium string `json:"picture_medium"`
			PictureBig    string `json:"picture_big"`
			PictureXL     string `json:"picture_xl"`
		} `json:"data"`
	}
	if err := deezerGetJSON(ctx, client, endpoint, &payload); err != nil {
		return nil
	}
	candidates := make([]similarCandidate, 0, len(payload.Data))
	for _, entry := range payload.Data {
		trimmed := strings.TrimSpace(entry.Name)
		if trimmed == "" {
			continue
		}
		candidates = append(candidates, similarCandidate{
			Name:     trimmed,
			ImageURL: firstNonEmpty(entry.PictureXL, entry.PictureBig, entry.PictureMedium, entry.Picture),
		})
	}
	return candidates
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func deezerArtistID(ctx context.Context, client *http.Client, name string) (string, error) {
	endpoint := deezerSearchArtistURL + "?limit=1&q=" + url.QueryEscape(strings.TrimSpace(name))
	var payload struct {
		Data []struct {
			ID json.Number `json:"id"`
		} `json:"data"`
	}
	if err := deezerGetJSON(ctx, client, endpoint, &payload); err != nil {
		return "", err
	}
	if len(payload.Data) == 0 {
		return "", nil
	}
	return payload.Data[0].ID.String(), nil
}

func deezerGetJSON(ctx context.Context, client *http.Client, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Samo Server/0.1 ArtistMeta")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpStatusError{status: resp.StatusCode}
	}
	return json.Unmarshal(body, dst)
}

type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return "deezer http " + http.StatusText(e.status) }
