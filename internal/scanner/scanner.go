package scanner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
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
	db            *sql.DB
	ffprobePath   string
	covers        CoverResolver
	activeScan    *scanAccumulator
	overrideIndex *catalog.OverrideIndex
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
	// Only compute a fresh ID when the caller didn't provide one. Recomputing
	// would diverge from the row already stored in the DB whenever the kind,
	// media_type, or path normalization differs from what produced the
	// original ID — which silently broke item_count refresh because
	// media_files/audiobooks/podcasts kept referencing the original row.
	if library.ID == "" {
		library.ID = LibraryID(library.Kind, library.MediaType, root)
	}

	if err := s.upsertLibrary(ctx, library); err != nil {
		return err
	}

	files, err := audioFiles(root)
	if err != nil {
		return err
	}

	// Library kinds are now first-class enum values, not (kind, mediaType)
	// tuples. The "shelf" umbrella is gone — audiobook libraries and podcast
	// libraries each have their own kind and their own scanner entry point.
	// Normalize before dispatch so a stray "Podcast" / "PODCAST" / " podcast"
	// (from a hand-rolled API call or an old DB row) still routes correctly
	// instead of falling into the default "unsupported" branch.
	kind := strings.ToLower(strings.TrimSpace(library.Kind))
	log.Printf("scanner: scanning library %q kind=%q path=%q with %d audio files", library.Name, kind, library.Path, len(files))
	switch kind {
	case "music":
		for _, path := range files {
			if err := s.scanMusicFile(ctx, library, root, path); err != nil {
				return err
			}
		}
	case "audiobook":
		return s.scanAudiobookLibrary(ctx, library, root, files)
	case "podcast":
		return s.scanPodcastLibrary(ctx, library, root, files)
	case "mixed":
		return s.scanMixedLibrary(ctx, library, root, files)
	default:
		return fmt.Errorf("unsupported library kind %q", library.Kind)
	}

	_, err = s.db.ExecContext(ctx, `UPDATE libraries SET last_scan_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, library.ID)
	return err
}

// CountAudioFiles walks a library root and counts the audio files the
// scanner will eventually visit. Used by the libraries service to seed
// scan_jobs.files_total so the dashboard can render real progress
// ("1200 of 1500") instead of an ever-climbing files_seen counter.
//
// Walks the same tree audioFiles() walks, with the same extension filter
// and dotfile-folder skipping, so the count matches what the scan will
// actually probe.
func CountAudioFiles(root string) (int, error) {
	files, err := audioFiles(root)
	if err != nil {
		return 0, err
	}
	return len(files), nil
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
	// ON CONFLICT(id) handles same-row reupsert; ON CONFLICT(path) handles the
	// case where the row exists with a different id (e.g. created via API
	// then re-synced via env vars, or migrated by 016 from a shelf-prefixed
	// hash). The path-conflict branch preserves the existing id so data
	// linked to it stays connected.
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
	if err == nil {
		return nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fmt.Errorf("upsert library %q: %w", library.Path, err)
	}
	// Path UNIQUE collision — preserve the existing row's id but update
	// kind/name to whatever the caller intends.
	_, err = s.db.ExecContext(ctx, `
		UPDATE libraries
		SET name = ?, kind = ?, media_type = ?, updated_at = CURRENT_TIMESTAMP
		WHERE path = ?`,
		library.Name, library.Kind, library.MediaType, library.Path)
	if err != nil {
		return fmt.Errorf("update library by path %q: %w", library.Path, err)
	}
	return nil
}
