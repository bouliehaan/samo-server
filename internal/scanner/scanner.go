package scanner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Library struct {
	ID        string
	Name      string
	Kind      string
	MediaType string
	Path      string
}

type Options struct {
	Covers      CoverResolver
	FFprobePath string
}

type Scanner struct {
	db          *sql.DB
	ffprobePath string
	covers      CoverResolver
	activeScan  *scanAccumulator
}

func New(db *sql.DB) *Scanner {
	return NewWithOptions(db, Options{})
}

func NewWithOptions(db *sql.DB, options Options) *Scanner {
	ffprobePath := strings.TrimSpace(options.FFprobePath)
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}
	return &Scanner{
		db:          db,
		ffprobePath: ffprobePath,
		covers:      options.Covers,
	}
}

func (s *Scanner) Scan(ctx context.Context, libraries []Library) error {
	_, err := s.ScanWithStats(ctx, libraries)
	return err
}

func LibraryID(kind, mediaType, path string) string {
	return stableID("library", kind, mediaType, path)
}

func (s *Scanner) scanLibrary(ctx context.Context, library Library) error {
	root, err := filepath.Abs(strings.TrimSpace(library.Path))
	if err != nil {
		return fmt.Errorf("resolve library path %q: %w", library.Path, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat library %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("library path %q is not a directory", root)
	}

	library.Path = root
	if library.Name == "" {
		library.Name = filepath.Base(root)
	}
	library.ID = LibraryID(library.Kind, library.MediaType, root)

	if err := s.upsertLibrary(ctx, library); err != nil {
		return err
	}

	files, err := audioFiles(root)
	if err != nil {
		return err
	}

	switch {
	case library.Kind == "music":
		for _, path := range files {
			if err := s.scanMusicFile(ctx, library, root, path); err != nil {
				return err
			}
		}
	case library.Kind == "shelf" && library.MediaType == "book":
		return s.scanAudiobookLibrary(ctx, library, root, files)
	case library.Kind == "shelf" && library.MediaType == "podcast":
		return s.scanPodcastLibrary(ctx, library, root, files)
	default:
		return fmt.Errorf("unsupported library kind %q media type %q", library.Kind, library.MediaType)
	}

	_, err = s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	return err
}

func audioFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if isAudioPath(path) {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func isAudioPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".alac", ".flac", ".m4a", ".m4b", ".mp3", ".ogg", ".opus", ".wav", ".wma":
		return true
	default:
		return false
	}
}

func (s *Scanner) probe(ctx context.Context, path string) (probeInfo, error) {
	cmd := exec.CommandContext(ctx, s.ffprobePath, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", path)
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return probeInfo{}, fmt.Errorf("ffprobe %q: %s", path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return probeInfo{}, fmt.Errorf("ffprobe %q: %w", path, err)
	}

	var raw ffprobeResult
	if err := json.Unmarshal(output, &raw); err != nil {
		return probeInfo{}, fmt.Errorf("parse ffprobe output for %q: %w", path, err)
	}

	return raw.toProbeInfo(path), nil
}

func stableID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(strings.ToLower(strings.TrimSpace(part))))
		hash.Write([]byte{0})
	}
	sum := hash.Sum(nil)
	return prefix + "_" + hex.EncodeToString(sum[:12])
}

func jsonText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s *Scanner) upsertLibrary(ctx context.Context, library Library) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  kind = excluded.kind,
		  media_type = excluded.media_type,
		  path = excluded.path,
		  updated_at = CURRENT_TIMESTAMP`,
		library.ID, library.Name, library.Kind, library.MediaType, library.Path)
	if err != nil {
		return fmt.Errorf("upsert library %q: %w", library.Path, err)
	}
	return nil
}
