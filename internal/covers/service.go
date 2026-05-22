package covers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type Service struct {
	db          *sql.DB
	coverDir    string
	ffmpegPath  string
	ffprobePath string
}

type Options struct {
	CoverDir    string
	FFmpegPath  string
	FFprobePath string
}

func New(db *sql.DB, options Options) (*Service, error) {
	coverDir := strings.TrimSpace(options.CoverDir)
	if coverDir == "" {
		return nil, errors.New("cover directory is required")
	}
	absolute, err := filepath.Abs(coverDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("create cover directory: %w", err)
	}

	ffmpeg := strings.TrimSpace(options.FFmpegPath)
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	ffprobe := strings.TrimSpace(options.FFprobePath)
	if ffprobe == "" {
		ffprobe = "ffprobe"
	}

	return &Service{
		db:          db,
		coverDir:    absolute,
		ffmpegPath:  ffmpeg,
		ffprobePath: ffprobe,
	}, nil
}

func (s *Service) CoverDir() string {
	if s == nil {
		return ""
	}
	return s.coverDir
}

func (s *Service) ResolveForAudio(ctx context.Context, audioPath, sourceChecksum string) (*catalog.Image, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	audioPath = strings.TrimSpace(audioPath)
	if audioPath == "" {
		return nil, ErrInvalidPath
	}
	absolute, err := filepath.Abs(audioPath)
	if err != nil {
		return nil, err
	}

	if existing, err := s.loadBySource(ctx, absolute); err == nil {
		if existing.sourceChecksum == sourceChecksum && fileExists(existing.path) {
			image := existing.image
			return &image, nil
		}
	}

	if !hasEmbeddedCover(ctx, s.ffprobePath, absolute) {
		return nil, ErrNoArtwork
	}

	image, err := s.extract(ctx, absolute, sourceChecksum)
	if err != nil {
		return nil, err
	}
	return image, nil
}

func (s *Service) Get(ctx context.Context, id string) (catalog.Image, error) {
	if s == nil || s.db == nil {
		return catalog.Image{}, ErrDisabled
	}
	row, err := s.loadByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return catalog.Image{}, err
	}
	return row.image, nil
}

type coverRow struct {
	image          catalog.Image
	path           string
	sourceChecksum string
}

func (s *Service) loadByID(ctx context.Context, id string) (coverRow, error) {
	var row coverRow
	var mimeType string
	var width, height int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, path, mime_type, width, height, source_checksum
		FROM extracted_covers
		WHERE id = ?`, id).Scan(&row.image.ID, &row.path, &mimeType, &width, &height, &row.sourceChecksum)
	if err == sql.ErrNoRows {
		return coverRow{}, ErrNotFound
	}
	if err != nil {
		return coverRow{}, fmt.Errorf("load cover: %w", err)
	}
	row.image.Path = row.path
	row.image.MimeType = mimeType
	row.image.Width = width
	row.image.Height = height
	return row, nil
}

func (s *Service) loadBySource(ctx context.Context, sourcePath string) (coverRow, error) {
	var row coverRow
	var mimeType string
	var width, height int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, path, mime_type, width, height, source_checksum
		FROM extracted_covers
		WHERE source_path = ?`, sourcePath).Scan(&row.image.ID, &row.path, &mimeType, &width, &height, &row.sourceChecksum)
	if err == sql.ErrNoRows {
		return coverRow{}, ErrNotFound
	}
	if err != nil {
		return coverRow{}, fmt.Errorf("load cover by source: %w", err)
	}
	row.image.Path = row.path
	row.image.MimeType = mimeType
	row.image.Width = width
	row.image.Height = height
	return row, nil
}

func (s *Service) extract(ctx context.Context, sourcePath, sourceChecksum string) (*catalog.Image, error) {
	id := coverID(sourcePath)
	dest := filepath.Join(s.coverDir, id+".jpg")

	cmd := exec.CommandContext(ctx, s.ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", sourcePath,
		"-map", "0:v:0",
		"-frames:v", "1",
		"-f", "image2",
		dest,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(strings.ToLower(string(output)), "stream map") || strings.Contains(string(output), "does not exist") {
			return nil, ErrNoArtwork
		}
		return nil, fmt.Errorf("extract embedded cover from %q: %s: %w", sourcePath, strings.TrimSpace(string(output)), err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		return nil, fmt.Errorf("stat extracted cover: %w", err)
	}
	if info.Size() == 0 {
		_ = os.Remove(dest)
		return nil, ErrNoArtwork
	}

	image := catalog.Image{
		ID:       id,
		Path:     dest,
		MimeType: "image/jpeg",
	}
	if err := s.upsert(ctx, sourcePath, sourceChecksum, image); err != nil {
		return nil, err
	}
	return &image, nil
}

func (s *Service) upsert(ctx context.Context, sourcePath, sourceChecksum string, image catalog.Image) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO extracted_covers (id, source_path, source_checksum, path, mime_type, width, height, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  source_path = excluded.source_path,
		  source_checksum = excluded.source_checksum,
		  path = excluded.path,
		  mime_type = excluded.mime_type,
		  width = excluded.width,
		  height = excluded.height,
		  updated_at = CURRENT_TIMESTAMP`,
		image.ID, sourcePath, sourceChecksum, image.Path, image.MimeType, image.Width, image.Height)
	if err != nil {
		return fmt.Errorf("upsert extracted cover: %w", err)
	}
	return nil
}

func hasEmbeddedCover(ctx context.Context, ffprobePath, audioPath string) bool {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-select_streams", "v",
		"-show_entries", "stream=codec_type,disposition:attached_pic",
		"-of", "json",
		audioPath,
	)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), `"attached_pic": 1`) ||
		strings.Contains(string(output), `"attached_pic":1`)
}

func coverID(sourcePath string) string {
	hash := sha256.New()
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(sourcePath))))
	hash.Write([]byte{0})
	return "cover_" + hex.EncodeToString(hash.Sum(nil)[:12])
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}
