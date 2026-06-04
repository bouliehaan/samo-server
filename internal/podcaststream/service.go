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

const (
	maxRedirects          = 5
	defaultUserAgent      = "Mozilla/5.0 (compatible; Samo-Server/1.0)"
	dialTimeout           = 15 * time.Second
	tlsTimeout            = 15 * time.Second
	responseHeaderTimeout = 30 * time.Second
	streamRequestTimeout  = 2 * time.Hour
)

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
	service.client = newEnclosureHTTPClient(service, streamRequestTimeout)
	return service
}

func newEnclosureHTTPClient(service *Service, requestTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Prefer IPv4; broken IPv6 routes on home servers otherwise hang for minutes.
			if network == "tcp" {
				network = "tcp4"
			}
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
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

	sizeBytes := enc.SizeBytes
	if enc.OffsetSeconds > 0 && sizeBytes <= 0 {
		if probed, err := s.probeContentLength(ctx, parsed.String()); err == nil && probed > 0 {
			sizeBytes = probed
		}
	}

	durationSeconds := effectiveDurationSeconds(enc.DurationSeconds, sizeBytes)
	var resumeStartByte int64
	upstreamMethod := http.MethodGet
	if r.Method == http.MethodHead {
		upstreamMethod = http.MethodHead
	}
	upstream, err := http.NewRequestWithContext(ctx, upstreamMethod, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("build upstream request: %w", err)
	}
	rangeHeader := strings.TrimSpace(r.Header.Get("Range"))
	if rangeHeader == "" && enc.OffsetSeconds > 0 {
		resumeStartByte = byteOffsetForSeconds(sizeBytes, durationSeconds, enc.OffsetSeconds)
		if resumeStartByte > 0 {
			rangeHeader = fmt.Sprintf("bytes=%d-", resumeStartByte)
		}
	}
	if rangeHeader != "" {
		upstream.Header.Set("Range", rangeHeader)
	}
	upstream.Header.Set("User-Agent", defaultUserAgent)

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
	w.Header().Set("Accept-Ranges", "bytes")

	skipLeading := int64(0)
	if resumeStartByte > 0 && resp.StatusCode == http.StatusOK {
		// Publisher ignored Range and returned the full file; trim leading bytes ourselves.
		skipLeading = resumeStartByte
		w.Header().Del("Content-Length")
		w.Header().Del("Content-Range")
	}
	w.WriteHeader(resp.StatusCode)

	if r.Method == http.MethodHead {
		return nil
	}
	if skipLeading > 0 {
		if _, err := io.CopyN(io.Discard, resp.Body, skipLeading); err != nil {
			return fmt.Errorf("skip to resume offset: %w", err)
		}
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
	req.Header.Set("User-Agent", defaultUserAgent)
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
		return &maxBytesReadCloser{ReadCloser: resp.Body, remaining: maxBytes}, contentType, nil
	}
	return resp.Body, contentType, nil
}

type maxBytesReadCloser struct {
	io.ReadCloser
	remaining int64
}

func (r *maxBytesReadCloser) Read(p []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(p)) > r.remaining {
			p = p[:int(r.remaining)]
		}
		n, err := r.ReadCloser.Read(p)
		r.remaining -= int64(n)
		return n, err
	}
	var probe [1]byte
	n, err := r.ReadCloser.Read(probe[:])
	if n > 0 {
		return 0, fmt.Errorf("%w: enclosure exceeds max cache file size", ErrUpstream)
	}
	return 0, err
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

// effectiveDurationSeconds fills in RSS episodes that have byte size but no duration.
func effectiveDurationSeconds(durationSeconds int, sizeBytes int64) int {
	if durationSeconds > 0 {
		return durationSeconds
	}
	if sizeBytes <= 0 {
		return 0
	}
	// ~128kbps MP3 — rough enough for byte-range resume when publishers omit duration.
	estimate := int(sizeBytes / 16_000)
	if estimate < 1 {
		return 0
	}
	return estimate
}

// probeContentLength learns the enclosure size when RSS metadata omitted length.
func (s *Service) probeContentLength(ctx context.Context, rawURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := s.client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.ContentLength > 0 {
			return resp.ContentLength, nil
		}
	}

	// Some publishers reject HEAD; a tiny ranged GET still exposes Content-Range.
	rangeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	rangeReq.Header.Set("Range", "bytes=0-0")
	rangeReq.Header.Set("User-Agent", defaultUserAgent)

	rangeResp, err := s.client.Do(rangeReq)
	if err != nil {
		return 0, err
	}
	defer rangeResp.Body.Close()
	_, _ = io.Copy(io.Discard, rangeResp.Body)

	if rangeResp.ContentLength > 0 {
		return rangeResp.ContentLength, nil
	}
	if total, ok := parseContentRangeTotal(rangeResp.Header.Get("Content-Range")); ok {
		return total, nil
	}
	return 0, fmt.Errorf("upstream did not report enclosure size")
}

func parseContentRangeTotal(header string) (int64, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, false
	}
	parts := strings.Split(header, "/")
	if len(parts) != 2 {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}
