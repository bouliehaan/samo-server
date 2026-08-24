package explo

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// KeepResult is the per-track outcome of a keep request. Failures are reported
// per track rather than aborting the batch: someone selecting twenty tracks in
// the Explore playlist should not lose nineteen because one had a name the
// filesystem dislikes.
type KeepResult struct {
	TrackID string `json:"trackId"`
	Title   string `json:"title,omitempty"`
	Path    string `json:"path,omitempty"`
	// AlreadyInLibrary means only that the COPY was unnecessary — the file was
	// already at the destination. It is a success, not a no-op: LibraryTrackID
	// is still resolved, so a caller adding the song to a playlist gets the
	// existing library track rather than the drop-folder original. Named for
	// what happened, because "skipped" reads as "nothing happened".
	AlreadyInLibrary bool   `json:"alreadyInLibrary,omitempty"`
	Error            string `json:"error,omitempty"`
	// LibraryTrackID is the catalog id of the COPY, which is a different track
	// from the drop-folder original. Anything that outlives the week — adding
	// the song to a playlist, above all — has to reference this one: the
	// original's file is deleted by the next rotation, and a playlist pointing
	// at it silently loses the entry.
	//
	// Empty if the scan had not caught up before the timeout. That is not a
	// failure of the copy (the file is on disk and will be picked up), only of
	// naming it immediately.
	LibraryTrackID string `json:"libraryTrackId,omitempty"`
}

// Keep copies explo drops into the music library proper.
//
// The drop folder is a rotating queue — `--clean-downloads` empties it on every
// weekly run — so anything worth keeping has to leave it. This COPIES rather
// than moves: the original stays in Explore for the rest of the week and the
// next rotation collects it, which keeps the playlist stable while you triage.
//
// The copy is remuxed rather than duplicated byte-for-byte, because samo holds
// the good metadata (from AcoustID/MusicBrainz) as database overrides and the
// file itself still carries whatever the original sharer typed. Writing the
// effective tags into the file is what makes the kept copy correct in any
// player, and it also pins samo's own identity for the new file: track IDs are
// derived from tags and fall back to the file path only when tags are too thin
// to be useful.
func (s *Service) Keep(ctx context.Context, trackIDs []string) ([]KeepResult, error) {
	if s == nil || s.db == nil {
		return nil, ErrDisabled
	}
	if s.trackByID == nil {
		return nil, fmt.Errorf("keep: no catalog lookup configured")
	}
	if s.ffmpegPath == "" {
		return nil, fmt.Errorf("keep: ffmpeg is not available")
	}

	root, err := s.musicLibraryRoot(ctx)
	if err != nil {
		return nil, err
	}
	// Fail the whole batch with one clear sentence rather than the same
	// permission error repeated per track. samo mounts media read-only by
	// default — sensible for a server that otherwise only reads — and Keep is
	// the first feature that needs to write.
	if err := checkWritable(root); err != nil {
		return nil, err
	}

	dirs := s.effectiveDirs()
	results := make([]KeepResult, 0, len(trackIDs))
	touched := map[string]struct{}{}

	for _, id := range trackIDs {
		res := KeepResult{TrackID: id}
		dest, err := s.keepOne(ctx, id, root, dirs, &res)
		switch {
		case err != nil:
			res.Error = err.Error()
		case dest != "":
			res.Path = dest
			touched[filepath.Dir(dest)] = struct{}{}
		}
		results = append(results, res)
	}

	// One scan covering every directory written to, so the kept copies appear
	// without waiting for the next full sweep.
	if len(touched) > 0 && s.scanSubpaths != nil {
		paths := make([]string, 0, len(touched))
		for p := range touched {
			paths = append(paths, p)
		}
		if err := s.scanSubpaths(ctx, paths); err != nil {
			s.logger("explo: keep: rescan failed: %v", err)
		}
	}

	// Runs even when nothing was newly copied. A track already in the library
	// has no scan to wait for, and that is precisely
	// the case where a caller re-adds it to a playlist — it still needs the
	// library copy's id, or it would fall back to the drop-folder original
	// that rotation deletes. Returns immediately when there is nothing to look
	// up.
	s.resolveKeptTrackIDs(ctx, results)
	return results, nil
}

func (s *Service) keepOne(ctx context.Context, id, root string, dirs []string, res *KeepResult) (string, error) {
	track, err := s.trackByID(id)
	if err != nil {
		return "", fmt.Errorf("track not found")
	}
	res.Title = track.Title

	source := ""
	for _, file := range track.AudioFiles {
		if file.Path != "" {
			source = file.Path
			break
		}
	}
	if source == "" {
		return "", fmt.Errorf("track has no audio file")
	}

	// Only drop-folder content is keepable. Without this the endpoint would
	// happily duplicate any track in the library to a second path.
	if !underAnyDir(source, dirs) {
		return "", fmt.Errorf("track is not in an explo folder")
	}
	if _, err := os.Stat(source); err != nil {
		return "", fmt.Errorf("source file is unreadable: %w", err)
	}

	dest := keepDestination(root, track, filepath.Ext(source))
	if _, err := os.Stat(dest); err == nil {
		// Not an error and not a no-op: Path is set so the id resolver below
		// still hands back the existing library track.
		res.AlreadyInLibrary = true
		res.Path = dest
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("could not create %s: %w", filepath.Dir(dest), err)
	}

	// Write to a temp name in the destination directory so a failure or a
	// crash never leaves a half-copied file where the scanner will find it,
	// and so the final step is an atomic rename on the same filesystem.
	tmp := dest + ".samo-keep-tmp" + filepath.Ext(dest)
	defer func() { _ = os.Remove(tmp) }()

	if err := s.remuxWithTags(ctx, source, tmp, track); err != nil {
		return "", err
	}
	info, err := os.Stat(tmp)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("remux produced no output")
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("could not place %s: %w", filepath.Base(dest), err)
	}
	return dest, nil
}

// remuxWithTags copies the audio stream untouched and rewrites the tags around
// it. `-c copy` means no re-encode, so this is lossless and fast; `-map 0`
// carries embedded cover art across.
func (s *Service) remuxWithTags(ctx context.Context, source, dest string, track catalog.MusicTrack) error {
	args := []string{"-nostdin", "-y", "-loglevel", "error", "-i", source, "-map", "0", "-c", "copy"}

	add := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, "-metadata", key+"="+value)
		}
	}
	add("title", track.Title)
	add("artist", track.DisplayArtist)
	add("album", track.AlbumTitle)
	add("album_artist", firstNonEmpty(track.AlbumArtistNames))
	if track.TrackNumber > 0 {
		add("track", fmt.Sprintf("%d", track.TrackNumber))
	}
	if track.DiscNumber > 0 {
		add("disc", fmt.Sprintf("%d", track.DiscNumber))
	}
	if track.ReleaseYear > 0 {
		add("date", fmt.Sprintf("%d", track.ReleaseYear))
	}
	if len(track.Genres) > 0 {
		add("genre", track.Genres[0])
	}
	if track.ExternalIDs.MusicBrainzRecordingID != "" {
		add("musicbrainz_trackid", track.ExternalIDs.MusicBrainzRecordingID)
	}
	args = append(args, dest)

	cmd := exec.CommandContext(ctx, s.ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return fmt.Errorf("remux failed: %s", detail)
	}
	return nil
}

// keepDestination builds <root>/<album artist>/<album>/<NN> - <title>.<ext>,
// matching the layout the rest of the library already uses.
func keepDestination(root string, track catalog.MusicTrack, ext string) string {
	artist := firstNonEmpty(track.AlbumArtistNames)
	if artist == "" {
		artist = track.DisplayArtist
	}
	if artist == "" && len(track.ArtistNames) > 0 {
		artist = track.ArtistNames[0]
	}

	name := safeComponent(track.Title, "Untitled")
	if track.TrackNumber > 0 {
		name = fmt.Sprintf("%02d - %s", track.TrackNumber, name)
	}
	return filepath.Join(
		root,
		safeComponent(artist, "Unknown Artist"),
		safeComponent(track.AlbumTitle, "Unknown Album"),
		name+ext,
	)
}

// safeComponent reduces a metadata value to something usable as one path
// segment: no separators, no traversal, no trailing dots or spaces (which
// Windows shares silently mangle), and never empty.
func safeComponent(value, fallback string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		if r < 0x20 || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, value)
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.Trim(cleaned, ".")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return fallback
	}
	if len(cleaned) > 120 {
		cleaned = strings.TrimSpace(cleaned[:120])
	}
	return cleaned
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func underAnyDir(path string, dirs []string) bool {
	clean := filepath.Clean(path)
	for _, dir := range dirs {
		d := strings.TrimRight(filepath.Clean(dir), string(filepath.Separator))
		if d == "" {
			continue
		}
		if strings.HasPrefix(clean, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// musicLibraryRoot is where kept files land. Reading it per request rather than
// caching keeps a library added or repathed after startup working.
func (s *Service) musicLibraryRoot(ctx context.Context) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx,
		`SELECT path FROM libraries WHERE kind = 'music' ORDER BY created_at LIMIT 1`).Scan(&path)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no music library configured")
	}
	if err != nil {
		return "", fmt.Errorf("could not resolve the music library: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("the music library has no path")
	}
	return path, nil
}

// checkWritable verifies the library root can actually be written to, so the
// common misconfiguration reports itself instead of arriving as N identical
// per-track failures.
func checkWritable(root string) error {
	probe := filepath.Join(root, ".samo-keep-writable")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf(
			"the music library at %s is not writable by samo (%v) — Keep needs it mounted read-write",
			root, err)
	}
	_ = file.Close()
	_ = os.Remove(probe)
	return nil
}

// resolveKeptTrackIDs waits for the scanner to catalogue each copy and fills in
// its track id. Polls rather than hooking the scan job because the row
// appearing is the thing callers actually need; a finished job that somehow
// skipped the file would still leave them with nothing usable.
func (s *Service) resolveKeptTrackIDs(ctx context.Context, results []KeepResult) {
	const (
		timeout  = 30 * time.Second
		interval = 500 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)

	pending := func() []int {
		var idx []int
		for i, res := range results {
			if res.Path != "" && res.Error == "" && res.LibraryTrackID == "" {
				idx = append(idx, i)
			}
		}
		return idx
	}

	for {
		remaining := pending()
		if len(remaining) == 0 {
			return
		}
		for _, i := range remaining {
			var trackID string
			err := s.db.QueryRowContext(ctx,
				`SELECT track_id FROM media_files WHERE path = ? AND track_id IS NOT NULL LIMIT 1`,
				results[i].Path).Scan(&trackID)
			if err == nil && strings.TrimSpace(trackID) != "" {
				results[i].LibraryTrackID = trackID
			}
		}
		if len(pending()) == 0 || time.Now().After(deadline) {
			if left := len(pending()); left > 0 {
				s.logger("explo: keep: %d copy/copies not catalogued within %s", left, timeout)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
