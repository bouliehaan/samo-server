package sources

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxRadioPlaylistBytes = 512 << 10
	maxRadioPlaylistDepth = 3
)

// ResolveInternetRadioStreamURL follows common radio playlist files
// (.m3u/.pls/.xspf) to the actual stream URL. HLS playlists are left alone:
// they are themselves the playable stream manifest.
func ResolveInternetRadioStreamURL(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	streamURL, err := normalizeHTTPURL(rawURL)
	if err != nil {
		return "", err
	}
	if !looksLikeRadioPlaylistURL(streamURL) {
		return streamURL, nil
	}
	return resolveInternetRadioStreamURL(ctx, client, streamURL, 0)
}

func resolveInternetRadioStreamURL(ctx context.Context, client *http.Client, streamURL string, depth int) (string, error) {
	if depth >= maxRadioPlaylistDepth || !looksLikeRadioPlaylistURL(streamURL) {
		return streamURL, nil
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "audio/x-mpegurl, application/vnd.apple.mpegurl, audio/mpegurl, audio/x-scpls, application/xspf+xml, */*;q=0.5")
	req.Header.Set("User-Agent", "Samo Server/0.1 RadioResolver")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch radio playlist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch radio playlist: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRadioPlaylistBytes+1))
	if err != nil {
		return "", fmt.Errorf("read radio playlist: %w", err)
	}
	if len(body) > maxRadioPlaylistBytes {
		return "", fmt.Errorf("radio playlist exceeds %d bytes", maxRadioPlaylistBytes)
	}
	baseURL := resp.Request.URL
	candidates := parseRadioPlaylistURLs(body, baseURL)
	if len(candidates) == 0 {
		return streamURL, nil
	}
	next := candidates[0]
	if looksLikeRadioPlaylistURL(next) {
		return resolveInternetRadioStreamURL(ctx, client, next, depth+1)
	}
	return next, nil
}

func looksLikeRadioPlaylistURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".m3u", ".pls", ".xspf":
		return true
	case ".m3u8":
		return true
	default:
		return false
	}
}

func parseRadioPlaylistURLs(body []byte, base *url.URL) []string {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf}))
	if len(trimmed) == 0 {
		return nil
	}
	lower := strings.ToLower(string(trimmed))
	switch {
	case strings.HasPrefix(lower, "<"):
		return parseXSPFPlaylistURLs(trimmed, base)
	case strings.Contains(lower, "[playlist]") || strings.Contains(lower, "\nfile1="):
		return parsePLSPlaylistURLs(trimmed, base)
	default:
		return parseM3UPlaylistURLs(trimmed, base)
	}
}

func parseM3UPlaylistURLs(body []byte, base *url.URL) []string {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if strings.Contains(strings.ToUpper(text), "#EXT-X-") {
		return nil
	}
	var urls []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if resolved := resolvePlaylistURL(line, base); resolved != "" {
			urls = append(urls, resolved)
		}
	}
	return urls
}

func parsePLSPlaylistURLs(body []byte, base *url.URL) []string {
	entries := map[int]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if !strings.HasPrefix(key, "file") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(key, "file"))
		if err != nil || index <= 0 {
			continue
		}
		if resolved := resolvePlaylistURL(strings.TrimSpace(value), base); resolved != "" {
			entries[index] = resolved
		}
	}
	if len(entries) == 0 {
		return nil
	}
	keys := make([]int, 0, len(entries))
	for index := range entries {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	urls := make([]string, 0, len(keys))
	for _, index := range keys {
		urls = append(urls, entries[index])
	}
	return urls
}

type xspfPlaylist struct {
	Tracks []xspfTrack `xml:"trackList>track"`
}

type xspfTrack struct {
	Location string `xml:"location"`
}

func parseXSPFPlaylistURLs(body []byte, base *url.URL) []string {
	var playlist xspfPlaylist
	if err := xml.Unmarshal(body, &playlist); err != nil {
		return nil
	}
	urls := make([]string, 0, len(playlist.Tracks))
	for _, track := range playlist.Tracks {
		if resolved := resolvePlaylistURL(track.Location, base); resolved != "" {
			urls = append(urls, resolved)
		}
	}
	return urls
}

func resolvePlaylistURL(raw string, base *url.URL) string {
	raw = strings.Trim(strings.TrimSpace(raw), `"'`)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "icy://") {
		raw = "http://" + raw[len("icy://"):]
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		parsed.Fragment = ""
		return parsed.String()
	default:
		return ""
	}
}
