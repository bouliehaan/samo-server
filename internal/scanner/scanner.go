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
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// probeFileTimeout caps ffprobe per file so a corrupt file or slow network
// mount cannot stall an entire library scan.
const probeFileTimeout = 90 * time.Second
const technicalProbeTimeout = 45 * time.Second

type Library struct {
	ID        string
	Name      string
	Kind      string
	MediaType string
	Path      string
}

type Options struct {
	Covers              CoverResolver
	FFprobePath         string
	PlaylistImport      PlaylistImporter
	AutoImportPlaylists bool
	ExternalScanner     bool
	// UseFFprobeForScan runs ffprobe per file during library scans. Default is
	// native header/tag parsing, which is faster and does not spawn subprocesses.
	UseFFprobeForScan bool
}

type Scanner struct {
	db                  *sql.DB
	ffprobePath         string
	covers              CoverResolver
	playlistImport      PlaylistImporter
	autoImportPlaylists bool
	externalScanner     bool
	useFFprobeForScan   bool
	activeScan          *scanAccumulator
	onWalkProgress      func(int)
	onActivity          func(string)
	onFileActive        func(path string)
	overrideIndex       *catalog.OverrideIndex
	scanMode            string
	scanSubpaths        []string
	fileIndex           map[string]indexedFile
	trackIDMigrations   map[string]string
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
		db:                  db,
		ffprobePath:         ffprobePath,
		covers:              options.Covers,
		playlistImport:      options.PlaylistImport,
		autoImportPlaylists: options.AutoImportPlaylists,
		externalScanner:     options.ExternalScanner,
		useFFprobeForScan:   options.UseFFprobeForScan,
	}
}

func (s *Scanner) Scan(ctx context.Context, libraries []Library) error {
	_, err := s.ScanWithStats(ctx, libraries)
	return err
}

func LibraryID(kind, mediaType, path string) string {
	return stableID("library", kind, mediaType, path)
}

// scanLibrary runs the Navidrome-style phased pipeline for one library.
// Prefer ScanWithProgress for production scans (progress callbacks, multi-library).
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
	if library.ID == "" {
		library.ID = LibraryID(library.Kind, library.MediaType, root)
	}

	accumulator := s.activeScan
	if accumulator == nil {
		accumulator = newScanAccumulator()
		s.activeScan = accumulator
	}
	fullScan := s.scanMode == ScanModeFull || s.scanMode == ScanModeRepair
	state := newScanState(fullScan, s.scanMode, s.scanSubpaths)
	_, err = s.runLibraryPipeline(ctx, library, accumulator, state)
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
func CountAudioFiles(ctx context.Context, root string) (int, error) {
	files, err := audioFiles(ctx, root, nil)
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

// CountAudioFilesInSubpaths counts audio files under one or more library
// subdirectories. Used for incremental scan progress totals.
func CountAudioFilesInSubpaths(ctx context.Context, root string, subpaths []string) (int, error) {
	return countAudioFilesInSubpaths(ctx, root, subpaths)
}

// PruneOrphanMusic removes music rows no longer referenced by media_files.
func (s *Scanner) PruneOrphanMusic(ctx context.Context) (int, error) {
	return s.pruneOrphanMusic(ctx)
}

func relDepthUnderRoot(root, path string) int {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return 0
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	// "album" => 1, "album/track.flac" => 2
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}

func audioFiles(ctx context.Context, root string, onProgress func(int)) ([]string, error) {
	var paths []string
	err := walkLibraryDir(ctx, root, func(path string, entry os.DirEntry) error {
		if shouldScanAudioFile(path) && isAudioPath(path) {
			paths = append(paths, path)
			if onProgress != nil && (len(paths) == 1 || len(paths)%10 == 0) {
				onProgress(len(paths))
			}
		}
		return nil
	})
	sort.Strings(paths)
	if onProgress != nil && len(paths) > 0 {
		onProgress(len(paths))
	}
	return paths, err
}

func shouldScanAudioFile(path string) bool {
	name := filepath.Base(path)
	// macOS AppleDouble/resource-fork sidecars (._*) often carry audio
	// extensions but are not playable tracks — indexing them duplicates albums
	// and surfaces garbage titles like "._1 - Ultralight Beam".
	if strings.HasPrefix(name, "._") {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	return true
}

func isAudioPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".aac", ".aif", ".aiff", ".alac", ".flac", ".m4a", ".m4b", ".mp3", ".ogg", ".opus", ".wav", ".wma":
		return true
	default:
		return false
	}
}

// probe is used for podcast file scans.
func (s *Scanner) probe(ctx context.Context, path string) (probeInfo, error) {
	info, err := s.probeMedia(ctx, path, false)
	if err != nil {
		return probeInfo{}, err
	}
	return finalizeProbeInfo(info), nil
}

// probeAudiobook reads tags natively, supplements technical fields via ffprobe,
// and loads embedded chapter markers from .m4b/.m4a when present.
func (s *Scanner) probeAudiobook(ctx context.Context, path string) (probeInfo, error) {
	info, err := s.probeMediaHybrid(ctx, path, true)
	if err != nil {
		return probeInfo{}, err
	}
	if len(info.Chapters) == 0 {
		ff, ffErr := s.probeMediaFFprobeWithTimeout(ctx, path, true, "32M", "10M", probeFileTimeout)
		if ffErr == nil {
			if len(ff.Chapters) > 0 {
				info.Chapters = ff.Chapters
			}
			info = mergeProbeInfo(info, ff, true)
		} else {
			log.Printf("scanner: audiobook chapter probe failed for %q: %v", path, ffErr)
		}
	}
	return finalizeProbeInfo(info), nil
}

// probeMusic probes a music track without parsing chapters. Chapter tables on
// some files are huge or malformed and can make ffprobe appear hung; music
// scanning does not use chapter metadata anyway.
func (s *Scanner) probeMusic(ctx context.Context, path string) (probeInfo, error) {
	return s.probeMedia(ctx, path, false)
}

func (s *Scanner) probeMedia(ctx context.Context, path string, includeChapters bool) (probeInfo, error) {
	if s.useFFprobeForScan {
		return s.probeMediaFFprobe(ctx, path, includeChapters)
	}
	return s.probeMediaHybrid(ctx, path, includeChapters)
}

// probeMediaHybrid reads tags natively, then calls ffprobe when duration or other
// technical fields cannot be determined from headers/tags alone.
func (s *Scanner) probeMediaHybrid(ctx context.Context, path string, includeChapters bool) (probeInfo, error) {
	nativeCtx, cancel := context.WithTimeout(ctx, nativeProbeTimeout)
	defer cancel()

	native, nativeErr := probeNative(nativeCtx, path, includeChapters)
	if nativeErr != nil {
		logFFprobeFallback(path, "native tags: "+nativeErr.Error())
		ff, err := s.probeMediaFFprobe(ctx, path, includeChapters)
		if err != nil {
			return probeInfo{}, err
		}
		return finalizeProbeInfo(ff), nil
	}
	if !probeNeedsTechnicalSupplement(native) {
		return finalizeProbeInfo(native), nil
	}

	logFFprobeFallback(path, "incomplete technical metadata")
	ff, err := s.probeMediaFFprobeTechnical(ctx, path, includeChapters)
	if err != nil {
		log.Printf("scanner: ffprobe technical probe failed for %q: %v (using native metadata)", path, err)
		return finalizeProbeInfo(native), nil
	}
	merged := mergeProbeInfo(native, ff, includeChapters)
	if merged.AudioFile.DurationSeconds <= 0 {
		log.Printf("scanner: ffprobe returned no duration for %q (using native metadata)", path)
		return finalizeProbeInfo(native), nil
	}
	return merged, nil
}

func (s *Scanner) probeMediaFFprobeTechnical(ctx context.Context, path string, includeChapters bool) (probeInfo, error) {
	if includeChapters {
		return s.probeMediaFFprobeWithTimeout(ctx, path, true, "32M", "10M", probeFileTimeout)
	}
	return s.probeMediaFFprobeWithTimeout(ctx, path, false, "256k", "1M", technicalProbeTimeout)
}

func (s *Scanner) probeMediaFFprobeWithTimeout(ctx context.Context, path string, includeChapters bool, probeSize, analyzeDuration string, timeout time.Duration) (probeInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.probeMediaFFprobeWithLimits(probeCtx, path, includeChapters, probeSize, analyzeDuration)
}

func (s *Scanner) probeMediaFFprobe(ctx context.Context, path string, includeChapters bool) (probeInfo, error) {
	return s.probeMediaFFprobeWithTimeout(ctx, path, includeChapters, "32M", "10M", probeFileTimeout)
}

func (s *Scanner) probeMediaFFprobeWithLimits(ctx context.Context, path string, includeChapters bool, probeSize, analyzeDuration string) (probeInfo, error) {
	args := []string{
		"-v", "error",
		"-probesize", probeSize,
		"-analyzeduration", analyzeDuration,
		"-print_format", "json",
		"-show_format", "-show_streams",
	}
	if includeChapters {
		args = append(args, "-show_chapters")
	}
	args = append(args, path)

	cmd := exec.CommandContext(ctx, s.ffprobePath, args...)
	cmd.Stdin = nil
	output, err := runCommandOutputWithTimeout(ctx, cmd)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return probeInfo{}, fmt.Errorf("ffprobe %q: timed out: %w", path, ctx.Err())
		}
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

func runCommandOutputWithTimeout(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := cmd.Output()
		done <- result{out: out, err: err}
	}()
	select {
	case <-ctx.Done():
		// Best effort kill. Do not wait here; waiting can deadlock when the child
		// is stuck in uninterruptible I/O on network storage.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, ctx.Err()
	case res := <-done:
		return res.out, res.err
	}
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
