package files

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	db         *sql.DB
	extraRoots []string
}

func New(db *sql.DB, extraRoots ...string) *Service {
	return &Service{db: db, extraRoots: extraRoots}
}

func (s *Service) GetMediaFile(ctx context.Context, id string) (MediaFile, error) {
	if s == nil || s.db == nil {
		return MediaFile{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return MediaFile{}, ErrNotFound
	}

	var (
		item        MediaFile
		itemID      sql.NullString
		trackID     sql.NullString
		episodeID   sql.NullString
		modifiedRaw sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, library_id, item_id, track_id, episode_id, path, relative_path, file_name,
		       mime_type, container, size_bytes, duration_seconds, modified_at
		FROM media_files
		WHERE id = ?`, id).Scan(
		&item.ID, &item.LibraryID, &itemID, &trackID, &episodeID, &item.Path, &item.RelativePath,
		&item.FileName, &item.MimeType, &item.Container, &item.SizeBytes, &item.DurationSeconds, &modifiedRaw,
	)
	if err == sql.ErrNoRows {
		return MediaFile{}, ErrNotFound
	}
	if err != nil {
		return MediaFile{}, fmt.Errorf("load media file: %w", err)
	}
	item.ItemID = itemID.String
	item.TrackID = trackID.String
	item.EpisodeID = episodeID.String
	item.ModifiedAt = parseTimePtr(modifiedRaw)
	return item, nil
}

func (s *Service) ValidateLocalPath(ctx context.Context, path string) (string, os.FileInfo, error) {
	if s == nil || s.db == nil {
		return "", nil, ErrDisabled
	}
	return validateReadablePath(ctx, s.db, s.extraRoots, path)
}

func (s *Service) ServeLocalPath(ctx context.Context, path string, w http.ResponseWriter, r *http.Request) error {
	return s.ServeReadablePathAt(ctx, path, "", 0, 0, w, r)
}

func (s *Service) ServeReadablePathAt(ctx context.Context, path, contentType string, durationSeconds, offsetSeconds int, w http.ResponseWriter, r *http.Request) error {
	absolute, info, err := s.ValidateLocalPath(ctx, path)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = mimeTypeForPath(absolute, "")
	}
	return serveFileAt(w, r, absolute, info, contentType, durationSeconds, offsetSeconds)
}

func (s *Service) ServeMediaFile(ctx context.Context, id string, w http.ResponseWriter, r *http.Request) error {
	return s.ServeMediaFileAt(ctx, id, 0, w, r)
}

// ServeMediaFileAt streams original on-disk bytes without transcoding or loudness processing.
func (s *Service) ServeMediaFileAt(ctx context.Context, id string, offsetSeconds int, w http.ResponseWriter, r *http.Request) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	item, err := s.GetMediaFile(ctx, id)
	if err != nil {
		return err
	}
	absolute, info, err := s.ValidateLocalPath(ctx, item.Path)
	if err != nil {
		return err
	}
	contentType := strings.TrimSpace(item.MimeType)
	if contentType == "" {
		contentType = mimeTypeForPath(absolute, item.Container)
	}
	return serveFileAt(w, r, absolute, info, contentType, item.DurationSeconds, offsetSeconds)
}

func serveFile(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, contentType string) error {
	return serveFileAt(w, r, path, info, contentType, 0, 0)
}

func serveFileAt(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, contentType string, durationSeconds, offsetSeconds int) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrMissing
		}
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	startByte := int64(0)
	if offsetSeconds > 0 {
		startByte = byteOffsetForSeconds(info.Size(), durationSeconds, offsetSeconds)
		if startByte > 0 {
			w.Header().Set("X-Samo-Stream-Offset-Seconds", strconv.Itoa(offsetSeconds))
		}
	}

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, max-age=3600")

	if startByte > 0 {
		remaining := info.Size() - startByte
		if remaining < 0 {
			remaining = 0
		}
		if _, err := file.Seek(startByte, io.SeekStart); err != nil {
			return fmt.Errorf("seek file: %w", err)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(remaining, 10))
		if r.Method == http.MethodHead {
			return nil
		}
		_, err = io.Copy(w, io.LimitReader(file, remaining))
		return err
	}

	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
	return nil
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

func mimeTypeForPath(path, container string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".m4b":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".wav":
		return "audio/wav"
	case ".aac":
		return "audio/aac"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		switch strings.ToLower(strings.TrimSpace(container)) {
		case "mp3":
			return "audio/mpeg"
		case "flac":
			return "audio/flac"
		default:
			return "application/octet-stream"
		}
	}
}

func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value.String))
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}
