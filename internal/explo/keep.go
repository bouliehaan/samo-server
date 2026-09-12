package explo

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
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

	// Identity check BEFORE the path check. The path check below can only see
	// a copy filed under the exact name this call would compute, and that name
	// is built from mutable, unnormalized metadata — so a library copy under
	// any other spelling is invisible to it. "Outlandos d\u2019Amour/3 - Roxanne"
	// and "Outlandos D'Amour/03 - Roxanne" are the same song by two
	// apostrophes, one capital letter and a zero; Keep duplicated it. Asking
	// the catalog what it already HAS, rather than asking the filesystem about
	// one guessed path, is the check that actually means "already in library".
	if twinID, twinPath := s.findLibraryTwin(ctx, track); twinID != "" {
		res.AlreadyInLibrary = true
		res.LibraryTrackID = twinID
		res.Path = twinPath
		return "", nil
	}

	albumTitle, err := s.keepAlbumTitle(ctx, id, track)
	if err != nil {
		return "", err
	}
	dest := keepDestination(root, track, albumTitle, filepath.Ext(source))
	if _, err := os.Stat(dest); err == nil {
		// Not an error and not a no-op: Path is set so the id resolver below
		// still hands back the existing library track.
		res.AlreadyInLibrary = true
		res.Path = dest
		return "", nil
	}
	// Before the folder is made, so an unkeepable format leaves no empty
	// album behind.
	format, err := remuxFormat(dest)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("could not create %s: %w", filepath.Dir(dest), err)
	}

	// Write to a temp name in the destination directory so a failure or a
	// crash never leaves a half-copied file where the scanner will find it,
	// and so the final step is an atomic rename on the same filesystem.
	tmp := keepTempPath(dest)
	defer func() { _ = os.Remove(tmp) }()

	cover := s.keepCoverPath(ctx, id, track)
	if err := s.remuxWithTags(ctx, source, tmp, format, track, albumTitle, cover); err != nil {
		return "", err
	}
	info, err := os.Stat(tmp)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("remux produced no output")
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", fmt.Errorf("could not place %s: %w", filepath.Base(dest), err)
	}

	// Best effort, after the copy is in place: a keep that copied the audio
	// and then reported failure would be answered "already in your library"
	// on the retry, which is worse than an album the cover pass can still
	// fix by hand.
	if _, embeddable := embedCoverArgs(format); cover != "" && !embeddable {
		if err := placeCoverSidecar(filepath.Dir(dest), cover); err != nil {
			s.logger("explo: keep: %s: cover could not be placed beside the copy: %v", filepath.Base(dest), err)
		}
	}
	return dest, nil
}

// embedCoverArgs reports whether ffmpeg's muxer for format can carry an
// attached picture, and the options it needs to actually write one.
//
// Checked against ffmpeg 8.1 with the exact command remuxArgs builds:
//   - flac, mp3 and ipod write the picture as given.
//   - aiff accepts the stream but, unless told to write ID3v2 tags, drops
//     it without a word.
//   - asf accepts the stream and writes it as a VIDEO stream: the kept .wma
//     plays as a one-frame film in anything that honours it.
//   - ogg, opus, adts and wav refuse the stream, and the keep fails with it.
//
// For the containers that cannot, keepOne puts the image beside the copy
// instead (placeCoverSidecar). The library never fetches art on its own, so
// a copy that lands without it stays without it.
func embedCoverArgs(format string) ([]string, bool) {
	switch format {
	case "flac", "mp3", "ipod":
		return nil, true
	case "aiff":
		return []string{"-write_id3v2", "1"}, true
	}
	return nil, false
}

// placeCoverSidecar writes samo's cover into an album folder as cover.<ext>,
// the name the scanner looks for before it looks inside any file. Only when
// the folder has no art of its own: the point is to give the copy a cover,
// not to change the cover of an album that was already there.
//
// The extension comes from the bytes, not the source's name — the cover
// cache names everything .jpg whatever it holds — and the write goes through
// the same temp-then-rename as the audio, so a half-written image is never
// there to be read.
func placeCoverSidecar(dir, coverPath string) error {
	if hasCoverSidecar(dir) {
		return nil
	}
	data, err := os.ReadFile(coverPath)
	if err != nil {
		return err
	}
	var ext string
	switch mime := http.DetectContentType(data); mime {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	default:
		return fmt.Errorf("%s is %s, not an image the scanner reads", coverPath, mime)
	}
	dest := filepath.Join(dir, "cover"+ext)
	tmp := keepTempPath(dest)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// hasCoverSidecar mirrors the scanner's own lookup (coverImageStems in
// scanner/sidecar.go): any of these names with an image extension is what it
// will show for the album.
func hasCoverSidecar(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		ext := filepath.Ext(name)
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp":
		default:
			continue
		}
		switch strings.TrimSuffix(name, ext) {
		case "cover", "folder", "front", "artwork", "album":
			return true
		}
	}
	return false
}

// keepTempPath is where a copy is written before the rename that makes it
// real. Its extension is the marker itself, never the audio extension: the
// library watcher and the scanner both decide what a file is by its
// extension, and this file has to count as nothing to them.
//
// It used to be "<dest>.samo-keep-tmp<ext>", and to the watcher that was an
// audio file: it appeared, was written to, and then — a rename is a Remove to
// fsnotify — LEFT the library, which is the one event the watcher answers
// with a whole-library reconcile, because only a library-wide scan can mark
// files missing. Every keep therefore cost a full quick scan of the entire
// music library (18.7k files on the live server, 2026-09-10) for a copy that
// Keep's own subpath scan had already catalogued. Fixed here rather than in
// the watcher because the watcher is right: an audio file that vanishes IS a
// reason to reconcile. The temp file must simply never look like one.
//
// No extension means ffmpeg cannot infer the container from the name, hence
// remuxFormat.
func keepTempPath(dest string) string {
	return dest + ".samo-keep-tmp"
}

// remuxFormat names the ffmpeg muxer for a kept copy. ffmpeg picks the
// container from the output path's extension, and the copy is written to a
// name that has none (keepTempPath), so it has to be told. Each entry is the
// muxer ffmpeg chooses on its own for that extension — checked against the
// "Output #0, <muxer>" line of ffmpeg 8.1 — so the copy is the same container
// the old audio-suffixed temp name produced. The extensions are the scanner's
// (isAudioPath); there is no point writing a file it would not catalogue.
//
// .alac is on the scanner's list but not here: it is a codec, not a
// container, and ffmpeg has no muxer for the extension. Keep never worked for
// one — ffmpeg refused the old temp name just the same — it only failed later
// and less clearly.
func remuxFormat(dest string) (string, error) {
	ext := strings.ToLower(filepath.Ext(dest))
	switch ext {
	case ".flac":
		return "flac", nil
	case ".mp3":
		return "mp3", nil
	case ".m4a", ".m4b":
		return "ipod", nil
	case ".ogg":
		return "ogg", nil
	case ".opus":
		return "opus", nil
	case ".aac":
		return "adts", nil
	case ".wav":
		return "wav", nil
	case ".aif", ".aiff":
		return "aiff", nil
	case ".wma":
		return "asf", nil
	}
	return "", fmt.Errorf("cannot keep a %q file: no container format to write it as", ext)
}

// remuxWithTags copies the audio stream untouched and rewrites the tags around
// it. `-c copy` means no re-encode, so this is lossless and fast. output is
// the temp name and format the container it is written as; see keepTempPath.
func (s *Service) remuxWithTags(ctx context.Context, source, output, format string, track catalog.MusicTrack, albumTitle, coverPath string) error {
	cmd := exec.CommandContext(ctx, s.ffmpegPath, remuxArgs(source, output, format, coverPath, albumTitle, track)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return fmt.Errorf("remux failed: %s", detail)
	}
	return nil
}

// remuxArgs builds the ffmpeg command line for one kept copy.
//
// The cover is the reason this is not just `-map 0`. An explo drop is a
// stranger's untagged rip: it usually carries NO embedded picture, and the art
// the app shows is samo's own — fetched from Cover Art Archive during
// identification and stored as a metadata override pointing at a file under
// the cover cache. None of that follows the audio. Copying the file and
// writing only text tags therefore produced a library album with no artwork at
// all, which is exactly what a kept track is not supposed to be: the whole
// point of remuxing is that the copy carries samo's effective metadata, and
// the cover is metadata.
//
//   - With a cover in hand and a container that can hold one, take the audio
//     from input 0 and the picture from input 1. samo's cover REPLACES any the
//     source carried, because samo's is what the app displays — and when the
//     source's own art is where samo got it, they are the same image anyway.
//   - Without one, `-map 0` keeps whatever the source had, embedded art
//     included.
//   - In a container that cannot hold a picture (embedCoverArgs), audio only.
//     The muxer would refuse samo's cover and the source's own alike, and a
//     source that carries one — ID3 art in a WAV, a picture block in an Ogg —
//     otherwise fails to copy at all.
//
// format is passed as an explicit -f because output carries no extension for
// ffmpeg to infer the container from (keepTempPath).
func remuxArgs(source, output, format, coverPath, albumTitle string, track catalog.MusicTrack) []string {
	args := []string{"-nostdin", "-y", "-loglevel", "error", "-i", source}
	embed, embeddable := embedCoverArgs(format)
	switch {
	case !embeddable:
		args = append(args, "-map", "0:a", "-c", "copy")
	case coverPath == "":
		args = append(args, "-map", "0", "-c", "copy")
	default:
		// Without the disposition the picture is a plain video stream: FLAC
		// refuses it, and players that accept it show a one-frame video rather
		// than cover art.
		args = append(args,
			"-i", coverPath, "-map", "0:a", "-map", "1:v", "-c", "copy",
			"-disposition:v:0", "attached_pic",
			"-metadata:s:v:0", "title=Album cover",
			"-metadata:s:v:0", "comment=Cover (front)",
		)
		args = append(args, embed...)
	}

	add := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, "-metadata", key+"="+value)
		}
	}
	add("title", track.Title)
	add("artist", track.DisplayArtist)
	add("album", albumTitle)
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
	return append(args, "-f", format, output)
}

// keepCoverPath picks the local image to embed in the kept copy: samo's
// effective cover for the track, whatever its origin — a Cover Art Archive
// download, a scanner sidecar, extracted embedded art, an admin upload.
//
// Generated placeholder tiles are deliberately excluded. A placeholder exists
// so the Explore grid is never blank while the real art is still being chased;
// baking one into a library file would outlive that wait and, worse, satisfy
// the scanner — the album would show a fake tile forever, even once real art
// landed. An artless file is the honest state, and the next cover pass can
// still fix the album.
func (s *Service) keepCoverPath(ctx context.Context, trackID string, track catalog.MusicTrack) string {
	for _, image := range track.Images {
		path := strings.TrimSpace(image.Path)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}
		if s.isPlaceholderCoverPath(ctx, trackID, path) {
			continue
		}
		return path
	}
	return ""
}

// keepDestination builds <root>/<album artist>/<album>/<NN> - <title>.<ext>,
// matching the layout the rest of the library already uses. albumTitle is the
// RESOLVED name from keepAlbumTitle, never track.AlbumTitle — see there for
// why the catalog's own value cannot be trusted for a drop.
func keepDestination(root string, track catalog.MusicTrack, albumTitle, ext string) string {
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
		safeComponent(albumTitle, "Unknown Album"),
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
//
// The wait is part of the endpoint's contract: the clients size their own
// request deadline for a keep from it (exploKeepTimeoutMs in the app's
// packages/core server-samo.ts). A longer wait here needs a longer budget
// there, or a keep that did everything right is reported as a failure.
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

// Keepable reports whether Keep would actually do something for this track:
// whether it is a drop sitting in the rotating folder, rather than a track
// already in the library proper.
//
// It exists so a surface can ask BEFORE offering the action. Anywhere the
// answer is no — a server with no explo folder configured, a channel airing a
// track out of the ordinary library, a live relay with no file behind it at
// all — the action is meaningless and should not appear; the alternative is
// offering "Keep in Library" against every song on the radio and letting the
// endpoint refuse most of them.
//
// Free of I/O on purpose. The catalog projection is in memory and the folder
// list is a lock-guarded slice, so a panel that asks this every time the song
// changes costs nothing. Every failure answers false rather than an error,
// because to the only caller there is they all mean the same thing.
func (s *Service) Keepable(trackID string) bool {
	// ffmpeg included: Keep remuxes samo's tags and cover into the copy, and
	// without it the whole batch fails. Offering an action that cannot run is
	// worse than not offering it.
	if s == nil || s.db == nil || s.trackByID == nil || s.ffmpegPath == "" || trackID == "" {
		return false
	}
	dirs := s.effectiveDirs()
	if len(dirs) == 0 {
		return false
	}
	track, err := s.trackByID(trackID)
	if err != nil {
		return false
	}
	// The first file with a path, matching what keepOne would copy — asking
	// about one file and keeping another would make this lie in exactly the
	// case it exists to get right.
	for _, file := range track.AudioFiles {
		if file.Path != "" {
			return underAnyDir(file.Path, dirs)
		}
	}
	return false
}

// keepDurationToleranceSeconds is how far two encodings of the same recording
// may drift and still be treated as the same song. Silence trimming and
// encoder padding move a track by a second or two; three seconds absorbs that
// without letting a genuinely different edit collapse into it.
const keepDurationToleranceSeconds = 3

// findLibraryTwin answers the question Keep actually needs answered before it
// copies anything: does the library ALREADY have this recording, under any
// name at all?
//
// Two rungs, strongest first:
//
//  1. The MusicBrainz recording id. Two files carrying the same recording id
//     are the same performance by definition, whatever either one is called.
//  2. Normalized artist + title + duration, for the (common) library tracks
//     that were never tagged with MusicBrainz ids. Normalization folds away
//     exactly what defeated the old path check — case, apostrophe style, and
//     every other punctuation difference — and the duration gate is what keeps
//     a same-titled remix or live cut from matching the studio version.
//
// Explo drops are excluded (is_explo = 0): the drop folder is full of tracks
// that ARE this track, and matching one of those would report every keep as
// already done.
func (s *Service) findLibraryTwin(ctx context.Context, track catalog.MusicTrack) (string, string) {
	if s.db == nil {
		return "", ""
	}
	if recording := strings.TrimSpace(track.ExternalIDs.MusicBrainzRecordingID); recording != "" {
		var id, path string
		err := s.db.QueryRowContext(ctx, `
			SELECT mt.id, COALESCE(mf.path, '')
			FROM music_tracks mt
			LEFT JOIN media_files mf ON mf.track_id = mt.id
			WHERE mt.is_explo = 0 AND mt.id <> ? AND mt.external_ids_json LIKE ?
			LIMIT 1`,
			track.ID, `%"musicBrainzRecordingId":"`+recording+`"%`).Scan(&id, &path)
		if err == nil && strings.TrimSpace(id) != "" {
			return id, path
		}
		if err != nil && err != sql.ErrNoRows {
			s.logger("explo: keep: recording-id twin lookup failed for %s: %v", track.ID, err)
		}
	}

	title := normalizeKeepIdentity(track.Title)
	artist := normalizeKeepIdentity(keepArtistName(track))
	if title == "" || artist == "" || track.DurationSeconds <= 0 {
		return "", ""
	}
	// Narrowed by duration in SQL and decided in Go: the normalization that
	// makes this correct (Unicode-aware, punctuation-folding) is not something
	// both supported databases can express, and a duration window is a cheap,
	// indexed-enough filter that leaves only a few hundred rows to walk.
	rows, err := s.db.QueryContext(ctx, `
		SELECT mt.id, mt.title, mt.display_artist, COALESCE(mf.path, '')
		FROM music_tracks mt
		LEFT JOIN media_files mf ON mf.track_id = mt.id
		WHERE mt.is_explo = 0 AND mt.id <> ? AND mt.duration_seconds BETWEEN ? AND ?`,
		track.ID,
		track.DurationSeconds-keepDurationToleranceSeconds,
		track.DurationSeconds+keepDurationToleranceSeconds)
	if err != nil {
		s.logger("explo: keep: twin lookup failed for %s: %v", track.ID, err)
		return "", ""
	}
	defer rows.Close()
	for rows.Next() {
		var id, candidateTitle, candidateArtist, path string
		if err := rows.Scan(&id, &candidateTitle, &candidateArtist, &path); err != nil {
			return "", ""
		}
		if normalizeKeepIdentity(candidateTitle) != title {
			continue
		}
		if !keepArtistsMatch(artist, normalizeKeepIdentity(candidateArtist)) {
			continue
		}
		return id, path
	}
	return "", ""
}

// keepArtistName is the artist identity used for twin matching, preferring the
// album artist so a track credited "A feat. B" still lines up with the same
// song filed under A.
func keepArtistName(track catalog.MusicTrack) string {
	if name := firstNonEmpty(track.AlbumArtistNames); name != "" {
		return name
	}
	if track.DisplayArtist != "" {
		return track.DisplayArtist
	}
	return firstNonEmpty(track.ArtistNames)
}

// keepArtistsMatch compares two normalized artist strings. Containment counts:
// the same release is routinely credited "Artist" in one place and
// "Artist, Guest" in another, and requiring equality would let that difference
// alone mint a duplicate — which is the whole class of bug this is here to stop.
func keepArtistsMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.Contains(a, b) || strings.Contains(b, a)
}

// normalizeKeepIdentity reduces a title or artist to its letters and digits,
// lowercased. Everything that distinguishes "Outlandos d’Amour" from
// "Outlandos D'Amour" — case, and a typographic apostrophe against an ASCII
// one — is exactly what this removes, because none of it makes two files
// different recordings.
func normalizeKeepIdentity(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// keepAlbumTitle resolves the album a kept copy is filed under.
//
// track.AlbumTitle is NOT the first choice. For a drop it is whatever the
// sharer tagged the file with — Soulseek rips come off hits compilations, so
// it reads "Het Beste Uit 20 Jaar Top 2000" — and for an untagged drop the
// scanner falls back to the containing folder, which is the rotating drop
// folder itself. Both were being written into the library as real album
// names, and a folder name is how "Weekly-Exploration" became an album.
//
// The identified release group is the answer, and the ledger holds its title
// from identification time (matched_album), so this is one local read. It
// used to ask MusicBrainz for the title here instead, inside the request —
// which put a rate-limited API behind a VPN on the path of a tap: the phone
// gave up at thirty seconds, the copy landed anyway, and the second tap said
// "already in your library". Keep now reads what the pipeline wrote and never
// waits on the network; a blank title is the pipeline's to fill in
// (backfillAlbumTitles), not this request's.
//
// Falling back to the catalog title is allowed only when it is not a drop
// folder's name: that is samo's effective title, which for an identified drop
// is the same release group applied as an override, so a track whose ledger
// row predates matched_album still files correctly. Nothing justifies writing
// the drop folder to disk.
func (s *Service) keepAlbumTitle(ctx context.Context, trackID string, track catalog.MusicTrack) (string, error) {
	if s.db != nil {
		var ledgerAlbum string
		err := s.db.QueryRowContext(ctx, `
			SELECT COALESCE(matched_album, '') FROM explo_tracks WHERE track_id = ?`, trackID).Scan(&ledgerAlbum)
		if err != nil && err != sql.ErrNoRows {
			s.logger("explo: keep: ledger lookup failed for %s: %v", trackID, err)
		}
		if title := strings.TrimSpace(ledgerAlbum); title != "" {
			return title, nil
		}
	}

	fallback := strings.TrimSpace(track.AlbumTitle)
	if fallback == "" || s.isDropFolderName(fallback) {
		return "", fmt.Errorf(
			"no album identified for this track yet — keeping it now would file it under %q",
			firstNonEmpty([]string{fallback, "Unknown Album"}))
	}
	return fallback, nil
}

// isDropFolderName reports whether a name is one of the configured drop
// folders, which is how the scanner names the "album" of a drop that carries
// no album tag of its own.
func (s *Service) isDropFolderName(name string) bool {
	target := normalizeKeepIdentity(name)
	if target == "" {
		return false
	}
	for _, dir := range s.effectiveDirs() {
		clean := strings.TrimRight(filepath.Clean(dir), string(filepath.Separator))
		if clean == "" {
			continue
		}
		if normalizeKeepIdentity(filepath.Base(clean)) == target {
			return true
		}
	}
	return false
}
