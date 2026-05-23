package podcaststream

import (
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

var (
	ErrDisabled           = errors.New("podcast stream service is disabled")
	ErrInvalidEnclosure   = errors.New("invalid enclosure url")
	ErrForbiddenEnclosure = errors.New("enclosure url is not allowed")
	ErrUpstream           = errors.New("upstream enclosure request failed")
)

const maxRedirects = 5

type Service struct {
	client            *http.Client
	allowPrivateHosts bool
}

type ServiceOptions struct {
	AllowPrivateHosts bool
}

func New(options ...ServiceOptions) *Service {
	var opts ServiceOptions
	if len(options) > 0 {
		opts = options[0]
	}
	service := &Service{allowPrivateHosts: opts.AllowPrivateHosts}
	service.client = &http.Client{
		Timeout: 2 * time.Hour,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("too many redirects")
			}
			if _, err := service.validateURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	return service
}

type Enclosure struct {
	URL             string
	ContentType     string
	SizeBytes       int64
	DurationSeconds int
	OffsetSeconds   int
}

func (s *Service) ServeEnclosure(ctx context.Context, enc Enclosure, w http.ResponseWriter, r *http.Request) error {
	if s == nil || s.client == nil {
		return ErrDisabled
	}
	parsed, err := s.validateURL(enc.URL)
	if err != nil {
		return err
	}

	upstream, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}
	rangeHeader := strings.TrimSpace(r.Header.Get("Range"))
	if rangeHeader == "" && enc.OffsetSeconds > 0 {
		startByte := byteOffsetForSeconds(enc.SizeBytes, enc.DurationSeconds, enc.OffsetSeconds)
		if startByte > 0 {
			rangeHeader = fmt.Sprintf("bytes=%d-", startByte)
		}
	}
	if rangeHeader != "" {
		upstream.Header.Set("Range", rangeHeader)
	}
	upstream.Header.Set("User-Agent", "Samo-Server/1.0")

	resp, err := s.client.Do(upstream)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
	}

	copyResponseHeaders(w, resp)
	if enc.ContentType != "" {
		w.Header().Set("Content-Type", enc.ContentType)
	}
	if enc.OffsetSeconds > 0 {
		w.Header().Set("X-Samo-Stream-Offset-Seconds", strconv.Itoa(enc.OffsetSeconds))
	}
	w.Header().Set("X-Samo-Stream-Source", "enclosure")
	w.WriteHeader(resp.StatusCode)

	if r.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// FetchEnclosure downloads an enclosure body for caching. The caller must close the returned reader.
func (s *Service) FetchEnclosure(ctx context.Context, rawURL string, maxBytes int64) (io.ReadCloser, string, error) {
	if s == nil || s.client == nil {
		return nil, "", ErrDisabled
	}
	parsed, err := s.validateURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("build upstream request: %w", err)
	}
	req.Header.Set("User-Agent", "Samo-Server/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		resp.Body.Close()
		return nil, "", fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
	}
	if maxBytes > 0 && resp.ContentLength > maxBytes {
		resp.Body.Close()
		return nil, "", fmt.Errorf("%w: enclosure exceeds max cache file size", ErrUpstream)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if maxBytes > 0 {
		return io.NopCloser(io.LimitReader(resp.Body, maxBytes)), contentType, nil
	}
	return resp.Body, contentType, nil
}

func validateEnclosureURL(raw string) (*url.URL, error) {
	parsed, err := parseEnclosureURL(raw)
	if err != nil {
		return nil, err
	}
	if forbiddenHost(parsed.Hostname()) {
		return nil, ErrForbiddenEnclosure
	}
	return parsed, nil
}

func (s *Service) validateURL(raw string) (*url.URL, error) {
	parsed, err := parseEnclosureURL(raw)
	if err != nil {
		return nil, err
	}
	if !s.allowPrivateHosts && forbiddenHost(parsed.Hostname()) {
		return nil, ErrForbiddenEnclosure
	}
	return parsed, nil
}

func parseEnclosureURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidEnclosure
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalidEnclosure
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, ErrInvalidEnclosure
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, ErrInvalidEnclosure
	}
	return parsed, nil
}

func forbiddenHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "localhost" {
		return true
	}
	if strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	return false
}

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for _, key := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control"} {
		if value := strings.TrimSpace(resp.Header.Get(key)); value != "" {
			w.Header().Set(key, value)
		}
	}
}

func byteOffsetForSeconds(size int64, durationSeconds, offsetSeconds int) int64 {
	if size <= 0 || durationSeconds <= 0 || offsetSeconds <= 0 {
		return 0
	}
	if offsetSeconds >= durationSeconds {
		return 0
	}
	return size * int64(offsetSeconds) / int64(durationSeconds)
}
