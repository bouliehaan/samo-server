package sources

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultProbeTimeout      = 12 * time.Second
	defaultProbeReadBudget   = 256 << 10
	icyMetadataMaxFrameBytes = 16 * 255
)

// IcyProbeResult holds the metadata captured from a single probe of an
// Icecast/Shoutcast stream. All fields are best-effort: missing values stay
// zero.
type IcyProbeResult struct {
	StationName string
	Description string
	Genre       string
	HomepageURL string
	ContentType string
	Codec       string
	Bitrate     int
	NowPlaying  string
	Title       string
	Artist      string
	Tags        []string
	ProbedAt    time.Time
}

// ProbeIcyStream issues an Icy-MetaData request against streamURL, captures
// ICY headers, and reads enough of the body to extract one metadata frame if
// the server advertises one. The probe always tears down the connection
// before returning.
func ProbeIcyStream(ctx context.Context, client *http.Client, streamURL string) (IcyProbeResult, error) {
	streamURL = strings.TrimSpace(streamURL)
	if streamURL == "" {
		return IcyProbeResult{}, ErrInvalidURL
	}
	parsed, err := url.Parse(streamURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return IcyProbeResult{}, ErrInvalidURL
	}

	if client == nil {
		client = &http.Client{Timeout: defaultProbeTimeout}
	}

	result, err := probeViaHTTP(ctx, client, streamURL)
	if err == nil {
		result.ProbedAt = time.Now().UTC()
		return result, nil
	}

	// Some legacy Shoutcast v1 servers respond with "ICY 200 OK" instead of an
	// HTTP/1.x status line, which Go's http.Client rejects. Fall back to a raw
	// HTTP/1.0 dial in that case.
	if isShoutcastStatusError(err) || isProtocolError(err) {
		legacy, legacyErr := probeViaRawDial(ctx, parsed)
		if legacyErr == nil {
			legacy.ProbedAt = time.Now().UTC()
			return legacy, nil
		}
		return IcyProbeResult{}, fmt.Errorf("probe stream (http: %v; raw: %v)", err, legacyErr)
	}
	return IcyProbeResult{}, err
}

func probeViaHTTP(ctx context.Context, client *http.Client, streamURL string) (IcyProbeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return IcyProbeResult{}, err
	}
	req.Header.Set("Icy-MetaData", "1")
	req.Header.Set("User-Agent", "Samo Server/0.1 IcyProbe")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return IcyProbeResult{}, fmt.Errorf("dial stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return IcyProbeResult{}, fmt.Errorf("stream status %d", resp.StatusCode)
	}

	result := resultFromHeaders(resp.Header)
	metaInt := metadataInterval(resp.Header)
	if metaInt > 0 {
		nowPlaying, title, artist, err := readIcyMetadataFrame(resp.Body, metaInt)
		if err == nil && nowPlaying != "" {
			result.NowPlaying = nowPlaying
			result.Title = title
			result.Artist = artist
		}
	}
	return result, nil
}

func probeViaRawDial(ctx context.Context, parsed *url.URL) (IcyProbeResult, error) {
	if parsed.Scheme != "http" {
		return IcyProbeResult{}, errors.New("legacy ICY probe requires http scheme")
	}
	host := parsed.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}

	dialer := &net.Dialer{Timeout: defaultProbeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return IcyProbeResult{}, fmt.Errorf("dial legacy icy: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(defaultProbeTimeout))

	path := parsed.RequestURI()
	if path == "" {
		path = "/"
	}
	requestLine := fmt.Sprintf(
		"GET %s HTTP/1.0\r\nHost: %s\r\nUser-Agent: Samo Server/0.1 IcyProbe\r\nIcy-MetaData: 1\r\nAccept: */*\r\nConnection: close\r\n\r\n",
		path, parsed.Host,
	)
	if _, err := io.WriteString(conn, requestLine); err != nil {
		return IcyProbeResult{}, fmt.Errorf("write legacy icy request: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 8192)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return IcyProbeResult{}, fmt.Errorf("read legacy icy status: %w", err)
	}
	statusLine = strings.TrimRight(statusLine, "\r\n")
	if !strings.HasPrefix(statusLine, "ICY ") && !strings.HasPrefix(statusLine, "HTTP/") {
		return IcyProbeResult{}, fmt.Errorf("unexpected legacy icy status: %q", statusLine)
	}

	header := http.Header{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return IcyProbeResult{}, fmt.Errorf("read legacy icy header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		header.Add(key, value)
	}

	result := resultFromHeaders(header)
	metaInt := metadataInterval(header)
	if metaInt > 0 {
		nowPlaying, title, artist, err := readIcyMetadataFrame(reader, metaInt)
		if err == nil && nowPlaying != "" {
			result.NowPlaying = nowPlaying
			result.Title = title
			result.Artist = artist
		}
	}
	return result, nil
}

func resultFromHeaders(header http.Header) IcyProbeResult {
	result := IcyProbeResult{
		StationName: firstHeader(header, "icy-name", "ice-name"),
		Description: firstHeader(header, "icy-description", "ice-description"),
		Genre:       firstHeader(header, "icy-genre", "ice-genre"),
		HomepageURL: firstHeader(header, "icy-url", "ice-url"),
		ContentType: strings.TrimSpace(header.Get("Content-Type")),
	}
	if rate := strings.TrimSpace(header.Get("icy-br")); rate != "" {
		if parsed, err := strconv.Atoi(rate); err == nil && parsed > 0 {
			result.Bitrate = parsed
		}
	}
	if result.ContentType != "" {
		result.Codec = codecFromContentType(result.ContentType)
	}
	if result.Genre != "" {
		result.Tags = splitGenre(result.Genre)
	}
	return result
}

func firstHeader(header http.Header, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func metadataInterval(header http.Header) int {
	raw := strings.TrimSpace(header.Get("icy-metaint"))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	if value > defaultProbeReadBudget {
		return 0
	}
	return value
}

func readIcyMetadataFrame(reader io.Reader, interval int) (string, string, string, error) {
	if interval <= 0 || interval > defaultProbeReadBudget {
		return "", "", "", errors.New("metadata interval out of range")
	}
	if _, err := io.CopyN(io.Discard, reader, int64(interval)); err != nil {
		return "", "", "", fmt.Errorf("skip audio bytes: %w", err)
	}

	lengthByte := make([]byte, 1)
	if _, err := io.ReadFull(reader, lengthByte); err != nil {
		return "", "", "", fmt.Errorf("read metadata length: %w", err)
	}
	frameLength := int(lengthByte[0]) * 16
	if frameLength == 0 {
		return "", "", "", nil
	}
	if frameLength > icyMetadataMaxFrameBytes {
		return "", "", "", fmt.Errorf("metadata frame too large: %d", frameLength)
	}

	frame := make([]byte, frameLength)
	if _, err := io.ReadFull(reader, frame); err != nil {
		return "", "", "", fmt.Errorf("read metadata frame: %w", err)
	}

	nowPlaying, title, artist := parseStreamTitle(frame)
	return nowPlaying, title, artist, nil
}

// parseStreamTitle decodes an Icecast metadata frame body. The format is a
// list of `Key='value';` pairs. The StreamTitle entry typically holds an
// "Artist - Title" string but some stations send only a title.
func parseStreamTitle(raw []byte) (string, string, string) {
	trimmed := strings.TrimRight(string(raw), "\x00")
	for _, part := range strings.Split(trimmed, ";") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(part), "streamtitle=") {
			continue
		}
		value := strings.TrimPrefix(part[len("streamtitle="):], "")
		value = strings.Trim(value, "'\"")
		value = strings.TrimSpace(value)
		if value == "" {
			return "", "", ""
		}
		if idx := strings.Index(value, " - "); idx > 0 {
			artist := strings.TrimSpace(value[:idx])
			title := strings.TrimSpace(value[idx+3:])
			return value, title, artist
		}
		return value, value, ""
	}
	return "", "", ""
}

func codecFromContentType(contentType string) string {
	lowered := strings.ToLower(contentType)
	if idx := strings.Index(lowered, ";"); idx >= 0 {
		lowered = lowered[:idx]
	}
	switch strings.TrimSpace(lowered) {
	case "audio/mpeg":
		return "mp3"
	case "audio/aac", "audio/aacp":
		return "aac"
	case "audio/ogg", "application/ogg":
		return "ogg"
	case "audio/flac":
		return "flac"
	case "audio/opus":
		return "opus"
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/x-mpegurl", "application/vnd.apple.mpegurl":
		return "hls"
	default:
		return ""
	}
}

func splitGenre(genre string) []string {
	separators := func(r rune) bool {
		return r == ',' || r == '/' || r == ';' || r == '|'
	}
	parts := strings.FieldsFunc(genre, separators)
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return cleaned
}

func isShoutcastStatusError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "icy 200 ok") ||
		strings.Contains(msg, "malformed http response") ||
		strings.Contains(msg, "invalid http status")
}

func isProtocolError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "http: invalid") ||
		strings.Contains(msg, "unexpected http")
}
