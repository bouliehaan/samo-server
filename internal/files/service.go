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

func (s *Service) ListMediaFilesForEpisode(ctx context.Context, episodeID string) ([]MediaFile, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil, ErrNotFound
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, library_id, audiobook_id, podcast_id, track_id, episode_id, path, relative_path, file_name,
		       mime_type, container, size_bytes, duration_seconds, modified_at
		FROM media_files
		WHERE episode_id = ?
		ORDER BY relative_path, file_name, id`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("list episode media files: %w", err)
	}
	defer rows.Close()

	var items []MediaFile
	for rows.Next() {
		var (
			item        MediaFile
			audiobookID sql.NullString
			podcastID   sql.NullString
			trackID     sql.NullString
			rowEpisode  sql.NullString
			modifiedRaw sql.NullString
		)
		if err := rows.Scan(
			&item.ID, &item.LibraryID, &audiobookID, &podcastID, &trackID, &rowEpisode,
			&item.Path, &item.RelativePath, &item.FileName, &item.MimeType, &item.Container,
			&item.SizeBytes, &item.DurationSeconds, &modifiedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan episode media file: %w", err)
		}
		item.AudiobookID = audiobookID.String
		item.PodcastID = podcastID.String
		item.TrackID = trackID.String
		item.EpisodeID = rowEpisode.String
		item.ModifiedAt = parseTimePtr(modifiedRaw)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items, nil
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
		audiobookID sql.NullString
		podcastID   sql.NullString
		trackID     sql.NullString
		episodeID   sql.NullString
		modifiedRaw sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, library_id, audiobook_id, podcast_id, track_id, episode_id, path, relative_path, file_name,
		       mime_type, container, size_bytes, duration_seconds, modified_at
		FROM media_files
		WHERE id = ?`, id).Scan(
		&item.ID, &item.LibraryID, &audiobookID, &podcastID, &trackID, &episodeID, &item.Path, &item.RelativePath,
		&item.FileName, &item.MimeType, &item.Container, &item.SizeBytes, &item.DurationSeconds, &modifiedRaw,
	)
	if err == sql.ErrNoRows {
		return MediaFile{}, ErrNotFound
	}
	if err != nil {
		return MediaFile{}, fmt.Errorf("load media file: %w", err)
	}
	item.AudiobookID = audiobookID.String
	item.PodcastID = podcastID.String
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

// ServeMediaFileAtSeconds streams a media file beginning at a precise playback
// second. For MP3 it resolves a frame-accurate byte offset by parsing the frame
// headers, rather than the size*sec/duration estimate that lands VBR seeks
// 20-70s off mid-sentence; other containers (M4B/MP4/AAC) keep whole-file
// serving and let the player seek via their own sample tables. The exact frame
// start is reported in X-Samo-Stream-Offset-Ms so the client can baseline its
// book-global position.
func (s *Service) ServeMediaFileAtSeconds(ctx context.Context, id string, seconds float64, w http.ResponseWriter, r *http.Request) error {
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
	// A sub-second target is effectively the start; serving from a mid-header byte
	// would drop the ID3/Xing tag for no benefit, so fall through to whole-file.
	// (A normal multi-file play resolves to fileAt≈0 and must keep the whole file.)
	if seconds > 0.5 && isMP3(contentType, absolute) {
		if startByte, startMs, ok := mp3ByteForSeconds(absolute, seconds); ok && startByte > 0 {
			return serveFileFromByte(w, r, absolute, info, contentType, startByte, startMs)
		}
	}
	return serveFileAt(w, r, absolute, info, contentType, item.DurationSeconds, 0)
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
		startByte = byteOffsetForSeconds(info.Size(), effectiveDurationSeconds(durationSeconds, info.Size()), offsetSeconds)
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
		section := io.NewSectionReader(file, startByte, remaining)
		http.ServeContent(w, r, filepath.Base(path), info.ModTime(), section)
		return nil
	}

	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
	return nil
}

// serveFileFromByte streams a file from a precomputed byte offset as a
// standalone resource — the frame-accurate counterpart to serveFileAt's
// second-based (and VBR-inaccurate) path. The section is presented as its own
// stream so the player decodes from the first frame at startByte.
func serveFileFromByte(w http.ResponseWriter, r *http.Request, path string, info os.FileInfo, contentType string, startByte, startMs int64) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrMissing
		}
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Samo-Stream-Offset-Ms", strconv.FormatInt(startMs, 10))

	remaining := info.Size() - startByte
	if remaining < 0 {
		remaining = 0
	}
	section := io.NewSectionReader(file, startByte, remaining)
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), section)
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

func effectiveDurationSeconds(durationSeconds int, sizeBytes int64) int {
	if durationSeconds > 0 {
		return durationSeconds
	}
	if sizeBytes <= 0 {
		return 0
	}
	estimate := int(sizeBytes / 16_000)
	if estimate < 1 {
		return 0
	}
	return estimate
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
