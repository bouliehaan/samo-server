package playlists

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const youtubeBrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var (
	youtubeInnertubeAPIKeyRE    = regexp.MustCompile(`"INNERTUBE_API_KEY":"([^"]+)"`)
	youtubeWebClientVersionRE   = regexp.MustCompile(`"clientVersion":"(2\.[^"]+)"`)
	youtubeMusicClientVersionRE = regexp.MustCompile(`"INNERTUBE_CLIENT_VERSION":"([^"]+)"`)
	youtubeInitialDataScriptRE  = regexp.MustCompile(`(?s)<script id="ytInitialData" type="application/json">(.*?)</script>`)
)

type youtubeInnertubeConfig struct {
	APIKey        string
	ClientName    string
	ClientVersion string
	Host          string
}

func fetchYouTubePlaylistContent(ctx context.Context, rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid import url", ErrInvalidInput)
	}
	playlistID := youtubePlaylistID(parsed)
	if playlistID == "" {
		return "", fmt.Errorf("%w: youtube playlist url must include a list= parameter", ErrInvalidInput)
	}

	fetchURL := normalizeYouTubePlaylistFetchURL(playlistID)
	html, err := fetchYouTubePage(ctx, fetchURL)
	if err != nil {
		return "", err
	}

	if data := extractYouTubeInitialData(html); data != "" {
		return data, nil
	}

	jsonBody, err := fetchYouTubeInnertubeBrowseJSON(ctx, html, playlistID, fetchURL)
	if err != nil {
		return "", err
	}
	if jsonBody == "" {
		return "", fmt.Errorf("%w: could not find playlist metadata in youtube page", ErrInvalidInput)
	}
	return jsonBody, nil
}

func normalizeYouTubePlaylistFetchURL(playlistID string) string {
	return "https://www.youtube.com/playlist?list=" + url.QueryEscape(playlistID)
}

func youtubePlaylistID(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("list"))
}

func fetchYouTubePage(ctx context.Context, fetchURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return "", err
	}
	setYouTubeFetchHeaders(req)

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch youtube playlist page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: youtube playlist page returned http %d", ErrInvalidInput, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImportBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxImportBytes {
		return "", fmt.Errorf("%w: import content is too large", ErrInvalidInput)
	}
	return string(body), nil
}

func setYouTubeFetchHeaders(req *http.Request) {
	req.Header.Set("User-Agent", youtubeBrowserUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
}

func fetchYouTubeInnertubeBrowseJSON(ctx context.Context, html, playlistID, referer string) (string, error) {
	cfg := extractYouTubeInnertubeConfig(html)
	if cfg.APIKey == "" {
		return "", fmt.Errorf("%w: could not find youtube innertube config", ErrInvalidInput)
	}

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    cfg.ClientName,
				"clientVersion": cfg.ClientVersion,
				"hl":            "en",
				"gl":            "US",
			},
		},
		"browseId": "VL" + playlistID,
	}
	raw, err := postYouTubeInnertube(ctx, cfg, referer, payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func extractYouTubeInnertubeConfig(html string) youtubeInnertubeConfig {
	cfg := youtubeInnertubeConfig{
		Host:       "https://www.youtube.com",
		ClientName: "WEB",
	}
	if match := youtubeInnertubeAPIKeyRE.FindStringSubmatch(html); len(match) > 1 {
		cfg.APIKey = match[1]
	}
	if match := youtubeWebClientVersionRE.FindStringSubmatch(html); len(match) > 1 {
		cfg.ClientVersion = match[1]
	} else if match := youtubeMusicClientVersionRE.FindStringSubmatch(html); len(match) > 1 {
		cfg.ClientVersion = match[1]
		cfg.ClientName = "WEB_REMIX"
		cfg.Host = "https://music.youtube.com"
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = "2.20260521.00.00"
	}
	return cfg
}

func postYouTubeInnertube(ctx context.Context, cfg youtubeInnertubeConfig, referer string, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(cfg.Host, "/") + "/youtubei/v1/browse?key=" + url.QueryEscape(cfg.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", youtubeBrowserUserAgent)
	req.Header.Set("Origin", cfg.Host)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch youtube innertube browse: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxImportBytes {
		return nil, fmt.Errorf("%w: youtube playlist response is too large", ErrInvalidInput)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: youtube innertube browse returned http %d", ErrInvalidInput, resp.StatusCode)
	}
	if bytes.Contains(raw, []byte(`"status":"NOT_FOUND"`)) {
		return nil, fmt.Errorf("%w: youtube playlist was not found or is not public", ErrInvalidInput)
	}
	return raw, nil
}

func extractYouTubeInitialData(content string) string {
	if match := youtubeInitialDataScriptRE.FindStringSubmatch(content); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	for _, marker := range []string{
		"var ytInitialData = ",
		`window["ytInitialData"] = `,
		"ytInitialData = ",
	} {
		index := strings.Index(content, marker)
		if index < 0 {
			continue
		}
		start := strings.Index(content[index:], "{")
		if start < 0 {
			continue
		}
		start += index
		if end := matchingJSONEnd(content, start); end > start {
			return content[start:end]
		}
	}
	return ""
}
