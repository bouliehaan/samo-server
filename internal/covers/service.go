package covers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

const coverProbeTimeout = 45 * time.Second
const coverExtractTimeout = 90 * time.Second

type Service struct {
	db             *sql.DB
	coverDir       string
	ffmpegPath     string
	ffprobePath    string
	httpClient     *http.Client
	remoteMaxBytes int64
	allowPrivate   bool
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

func (s *Service) LookupCached(ctx context.Context, audioPath, sourceChecksum string) (*catalog.Image, error) {
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
	existing, err := s.loadBySource(ctx, absolute)
	if err != nil {
		return nil, err
	}
	if existing.sourceChecksum != sourceChecksum || !fileExists(existing.path) {
		return nil, ErrNotFound
	}
	image := existing.image
	return &image, nil
}

func (s *Service) ResolveForAudio(ctx context.Context, audioPath, sourceChecksum string, embeddedKnown *bool) (*catalog.Image, error) {
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

	if embeddedKnown != nil && !*embeddedKnown {
		return nil, ErrNoArtwork
	}
	if embeddedKnown == nil {
		probeCtx, probeCancel := context.WithTimeout(ctx, coverProbeTimeout)
		defer probeCancel()
		if !hasEmbeddedCover(probeCtx, s.ffprobePath, absolute) {
			return nil, ErrNoArtwork
		}
	}

	extractCtx, extractCancel := context.WithTimeout(ctx, coverExtractTimeout)
	defer extractCancel()
	image, err := s.extract(extractCtx, absolute, sourceChecksum)
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

	strategies := [][]string{
		{"-map", "0:v:0", "-frames:v", "1", "-f", "image2"},
		{"-an", "-vcodec", "copy"},
	}
	for _, args := range strategies {
		cmd := exec.CommandContext(ctx, s.ffmpegPath,
			append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", sourcePath}, args...)...,
		)
		cmd.Args = append(cmd.Args, dest)
		if output, err := cmd.CombinedOutput(); err != nil {
			if strings.Contains(strings.ToLower(string(output)), "stream map") ||
				strings.Contains(string(output), "does not exist") {
				continue
			}
			continue
		}
		image, err := s.finalizeExtract(ctx, sourcePath, sourceChecksum, id, dest)
		if err == nil {
			return image, nil
		}
		_ = os.Remove(dest)
	}
	return nil, ErrNoArtwork
}

func (s *Service) finalizeExtract(ctx context.Context, sourcePath, sourceChecksum, id, dest string) (*catalog.Image, error) {
	info, err := os.Stat(dest)
	if err != nil {
		return nil, fmt.Errorf("stat extracted cover: %w", err)
	}
	if info.Size() == 0 {
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

type ffprobeCoverStreams struct {
	Streams []struct {
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Tags map[string]string `json:"tags"`
	} `json:"format"`
}

func hasEmbeddedCover(ctx context.Context, ffprobePath, audioPath string) bool {
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-nostdin",
		"-show_entries", "stream=codec_name,codec_type:stream_tags=METADATA_BLOCK_PICTURE:stream_disposition=attached_pic:format_tags=METADATA_BLOCK_PICTURE",
		"-of", "json",
		audioPath,
	)
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	var parsed ffprobeCoverStreams
	if err := json.Unmarshal(output, &parsed); err != nil {
		return false
	}
	for _, stream := range parsed.Streams {
		if stream.Disposition.AttachedPic == 1 {
			return true
		}
		if strings.TrimSpace(stream.Tags["METADATA_BLOCK_PICTURE"]) != "" {
			return true
		}
		if stream.CodecType == "video" &&
			(stream.CodecName == "mjpeg" || stream.CodecName == "png" || stream.CodecName == "apng") {
			return true
		}
	}
	if strings.TrimSpace(parsed.Format.Tags["METADATA_BLOCK_PICTURE"]) != "" {
		return true
	}
	return false
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
