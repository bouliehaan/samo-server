package covers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const (
	defaultRemoteCoverMaxBytes = 5 << 20
	defaultRemoteCoverTimeout  = 20 * time.Second
)

// RemoteOptions tunes the remote cover downloader. AllowPrivateHosts is only
// intended for tests; production deployments should leave it false so the
// covers service refuses to fetch from internal networks.
type RemoteOptions struct {
	HTTPClient        *http.Client
	MaxBytes          int64
	AllowPrivateHosts bool
}

// SetRemoteOptions installs the HTTP client and policy used by
// DownloadFromURL. Calling with nil resets to defaults.
func (s *Service) SetRemoteOptions(options RemoteOptions) {
	if s == nil {
		return
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultRemoteCoverTimeout}
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultRemoteCoverMaxBytes
	}
	s.httpClient = client
	s.remoteMaxBytes = maxBytes
	s.allowPrivate = options.AllowPrivateHosts
}

// DownloadFromURL fetches an image at rawURL, validates its content type and
// size, stores it under the cover cache directory, and persists an
// `extracted_covers` row keyed by URL hash. Repeated calls with the same URL
// return the existing cache row without re-downloading.
func (s *Service) DownloadFromURL(ctx context.Context, rawURL string) (*catalog.Image, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, ErrInvalidURL
	}
	parsed, err := parseImageURL(rawURL)
	if err != nil {
		return nil, err
	}
	if !s.allowPrivate && forbiddenHost(parsed.Hostname()) {
		return nil, ErrForbiddenHost
	}

	urlKey := normalizeImageURLForKey(parsed)
	id := remoteCoverID(urlKey)

	if existing, err := s.loadByID(ctx, id); err == nil && fileExists(existing.path) {
		image := existing.image
		image.URL = rawURL
		return &image, nil
	}

	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: defaultRemoteCoverTimeout}
	}
	maxBytes := s.remoteMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultRemoteCoverMaxBytes
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Samo Server/0.1 CoverFetch")
	req.Header.Set("Accept", "image/*, */*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch cover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch cover: status %d", resp.StatusCode)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return nil, ErrUnsupportedType
	}
	if resp.ContentLength > 0 && resp.ContentLength > maxBytes {
		return nil, ErrTooLarge
	}

	extension := extensionForContentType(contentType, parsed)
	destPath := filepath.Join(s.coverDir, id+extension)
	tempPath := destPath + ".tmp"
	out, err := os.Create(tempPath)
	if err != nil {
		return nil, fmt.Errorf("create cover file: %w", err)
	}

	hasher := sha256.New()
	reader := io.LimitReader(resp.Body, maxBytes+1)
	written, err := io.Copy(io.MultiWriter(out, hasher), reader)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("download cover: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("flush cover: %w", closeErr)
	}
	if written > maxBytes {
		_ = os.Remove(tempPath)
		return nil, ErrTooLarge
	}
	if written == 0 {
		_ = os.Remove(tempPath)
		return nil, errors.New("cover response was empty")
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("install cover file: %w", err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	image := catalog.Image{
		ID:       id,
		Path:     destPath,
		URL:      rawURL,
		MimeType: cleanMimeType(contentType),
	}
	if err := s.upsert(ctx, urlKey, checksum, image); err != nil {
		return nil, err
	}
	return &image, nil
}

func parseImageURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalidURL
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, ErrInvalidURL
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, ErrInvalidURL
	}
	parsed.Fragment = ""
	return parsed, nil
}

func normalizeImageURLForKey(parsed *url.URL) string {
	clone := *parsed
	clone.Scheme = strings.ToLower(clone.Scheme)
	clone.Host = strings.ToLower(clone.Host)
	clone.Fragment = ""
	return clone.String()
}

func remoteCoverID(urlKey string) string {
	hash := sha256.New()
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(urlKey))))
	return "cover_" + hex.EncodeToString(hash.Sum(nil)[:12])
}

func extensionForContentType(contentType string, parsed *url.URL) string {
	mime := strings.ToLower(contentType)
	if idx := strings.Index(mime, ";"); idx > 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	}
	if parsed != nil {
		ext := strings.ToLower(filepath.Ext(parsed.Path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif":
			return ext
		}
	}
	return ".bin"
}

func cleanMimeType(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.Index(value, ";"); idx > 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
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
