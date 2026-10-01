package explo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
)

// Search for new hands a song to Explo to download into the drop folder, and
// the drop folder is the explo silo: everything in it is identified, given art
// and kept OUT of the library. A requested song is the one exception — someone
// asked for it by name — so samo follows each request through the same
// pipeline as a weekly drop and, once the file is identified, keeps it into the
// library exactly as the Keep button would. Nothing else in the folder changes:
// weekly drops stay siloed.
//
// A request moves downloading → identifying → in-library. It stops at failed
// when Explo could not download it, and at needs-review when samo should not
// finish it alone (the audio turned out to be a different song, or it could
// not be identified at all); the file then stays in Explore, where Keep still
// works by hand.
const (
	RequestDownloading = "downloading"
	RequestIdentifying = "identifying"
	RequestInLibrary   = "in-library"
	RequestNeedsReview = "needs-review"
	RequestFailed      = "failed"
)

// requestCoverWait is how long an identified request waits for the cover pass
// before it is kept anyway. Keep embeds the cover it finds, so keeping a
// moment after identification would usually file the song without art; but a
// cover source that is down for the day should not hold the song back.
const requestCoverWait = time.Hour

// requestAlbumWait is how long an identified request waits for the album
// backfill before samo gives up and asks for review. Keep refuses to file a
// song under no album or under a hits compilation, and the backfill usually
// settles that within a pass or two.
const requestAlbumWait = 24 * time.Hour

// requestLostAfter is how long a download may be missing from Explo before
// samo calls it lost. Explo keeps jobs in memory, so a restart forgets them;
// the file may still have been staged, which is checked first.
const requestLostAfter = 30 * time.Minute

// SongRequest is samo's side of a Search for new request.
type SongRequest struct {
	RecordingID    string `json:"recordingId"`
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	LibraryTrackID string `json:"libraryTrackId,omitempty"`
	LibraryAlbumID string `json:"libraryAlbumId,omitempty"`
	// AlbumID is the album the song was asked for as part of, if any.
	AlbumID string `json:"albumId,omitempty"`
}

type songRequestRow struct {
	recordingID, title, artist, state, stagedFile, trackID   string
	libraryTrackID, libraryPath, message, createdAt, updated string
	// The album a track of a whole-album request is filed under; albumID is
	// empty for a song asked for on its own.
	album, albumID, albumArtist              string
	trackNumber, discNumber, discTotal, year int
	durationMS                               int
}

// placement is where Keep files a track of a whole-album request, or nil for
// a song asked for on its own, which goes wherever identification put it.
func (row songRequestRow) placement() *keepPlacement {
	if row.albumID == "" {
		return nil
	}
	return &keepPlacement{Title: row.album, Artist: row.albumArtist, ReleaseGroupID: musicBrainzOnly(row.albumID),
		TrackNumber: row.trackNumber, DiscNumber: row.discNumber, DiscTotal: row.discTotal, Year: row.year}
}

// RecordRequest notes a download samo asked Explo for. Asking again after a
// download failed, or for a song Explo starts downloading afresh, starts the
// request over; anything else leaves the request where it is.
func (s *Service) RecordRequest(ctx context.Context, job DownloadJob, requestedBy string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	id := strings.TrimSpace(job.ID)
	if id == "" {
		return fmt.Errorf("explo request has no recording id")
	}
	reset := ""
	if job.State == "queued" || job.State == "downloading" {
		reset = `,
		  state = excluded.state, message = '', staged_file = '', track_id = '',
		  library_track_id = '', library_path = '', updated_at = CURRENT_TIMESTAMP`
	}
	// A track asked for as part of an album takes that album; a song asked
	// for on its own leaves an album it is already part of alone.
	album := `album = CASE WHEN explo_requests.album_id = '' THEN excluded.album ELSE explo_requests.album END`
	var placed AlbumTrack
	if job.Song.AlbumTrack != nil && strings.TrimSpace(job.Song.AlbumTrack.AlbumID) != "" {
		placed = *job.Song.AlbumTrack
		album = `album = excluded.album, album_id = excluded.album_id, album_artist = excluded.album_artist,
		  track_number = excluded.track_number, disc_number = excluded.disc_number,
		  disc_total = excluded.disc_total, release_year = excluded.release_year`
	}
	albumTitle := job.Song.Album
	if placed.AlbumID != "" {
		albumTitle = placed.AlbumTitle
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO explo_requests (recording_id, title, artist, album, duration_ms, requested_by, state,
			  album_id, album_artist, track_number, disc_number, disc_total, release_year)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (recording_id) DO UPDATE SET
			  title = excluded.title, artist = excluded.artist, `+album+`,
			  duration_ms = excluded.duration_ms, requested_by = excluded.requested_by`+reset,
			id, job.Song.Title, job.Song.Artist, albumTitle, job.Song.DurationMS, requestedBy, RequestDownloading,
			placed.AlbumID, placed.AlbumArtist, placed.Number, placed.Disc, placed.DiscTotal, placed.Year)
		return err
	}); err != nil {
		return fmt.Errorf("record explo request: %w", err)
	}
	return s.SyncJob(ctx, job)
}

// SyncJob folds Explo's report of a download into samo's request: a failure
// ends it, and a staged file hands it to identification.
func (s *Service) SyncJob(ctx context.Context, job DownloadJob) error {
	if s == nil || s.db == nil {
		return nil
	}
	row, ok, err := s.loadRequest(ctx, job.ID)
	if err != nil || !ok {
		return err
	}
	file := cleanStagedFile(job.File)
	switch {
	case row.state == RequestDownloading && job.State == "failed":
		return s.updateRequest(ctx, row.recordingID, `state = ?, message = ?`, RequestFailed, job.Message)
	case row.state == RequestDownloading && job.State == "staged":
		return s.updateRequest(ctx, row.recordingID, `state = ?, staged_file = ?, message = ''`, RequestIdentifying, file)
	case row.state == RequestIdentifying && row.stagedFile == "" && file != "":
		return s.updateRequest(ctx, row.recordingID, `staged_file = ?`, file)
	}
	return nil
}

// Request returns samo's view of one request.
func (s *Service) Request(ctx context.Context, recordingID string) (SongRequest, bool, error) {
	if s == nil || s.db == nil {
		return SongRequest{}, false, nil
	}
	row, ok, err := s.loadRequest(ctx, recordingID)
	if err != nil || !ok {
		return SongRequest{}, ok, err
	}
	view := SongRequest{
		RecordingID:    row.recordingID,
		State:          row.state,
		Message:        row.message,
		LibraryTrackID: row.libraryTrackID,
		AlbumID:        row.albumID,
	}
	if row.libraryTrackID != "" {
		var albumID sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT album_id FROM music_tracks WHERE id = ?`, row.libraryTrackID).Scan(&albumID); err == nil {
			view.LibraryAlbumID = albumID.String
		}
	}
	return view, true, nil
}

// AdvanceRequests moves every open request one step on: it asks Explo about
// downloads still in flight, finds staged files in the catalog, and keeps the
// ones that are identified. Runs around each identify pass (main.go), so it
// costs nothing while no request is open. remote may be nil, when Explo is not
// connected; staged requests still finish.
func (s *Service) AdvanceRequests(ctx context.Context, remote *Remote) {
	if s == nil || s.db == nil {
		return
	}
	if !s.requestsMu.TryLock() {
		return // another pass is already on it
	}
	defer s.requestsMu.Unlock()

	rows, err := s.openRequests(ctx)
	if err != nil {
		s.logger("explo: load song requests failed: %v", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if row.state == RequestDownloading {
			row = s.pollRequest(ctx, remote, row)
		}
		switch row.state {
		case RequestIdentifying:
			s.advanceIdentifying(ctx, row)
		case RequestInLibrary:
			s.resolveRequestLibraryTrack(ctx, row)
		}
	}
	// Kept rows whose library copy the scanner had not catalogued yet.
	s.resolvePendingLibraryTracks(ctx)
	// Kept songs of imported playlists join their playlists, and the
	// playlists' waiting songs go to Explo.
	s.AdvancePlaylistImports(ctx, remote)
}

// pollRequest asks Explo how a download is going and returns the request as it
// stands afterwards.
func (s *Service) pollRequest(ctx context.Context, remote *Remote, row songRequestRow) songRequestRow {
	if remote == nil {
		return row
	}
	job, err := remote.Job(ctx, row.recordingID)
	var remoteErr *RemoteError
	switch {
	case errors.As(err, &remoteErr) && remoteErr.Status == 404:
		// Explo forgot the job, most likely by restarting. A file it had
		// already staged still carries the requested recording id.
		if trackID := s.locateRequestedTrack(ctx, row); trackID != "" {
			_ = s.updateRequest(ctx, row.recordingID, `state = ?, track_id = ?, message = ''`, RequestIdentifying, trackID)
			row.state, row.trackID = RequestIdentifying, trackID
			return row
		}
		if requestAge(row.updated) > requestLostAfter {
			_ = s.updateRequest(ctx, row.recordingID, `state = ?, message = ?`, RequestFailed,
				"Explo lost track of this download (it may have restarted). Search for the song and add it again.")
			row.state = RequestFailed
		}
		return row
	case err != nil:
		return row // Explo unreachable; try again next pass
	}
	if err := s.SyncJob(ctx, job); err != nil {
		s.logger("explo: sync song request %s failed: %v", row.recordingID, err)
	}
	if updated, ok, err := s.loadRequest(ctx, row.recordingID); err == nil && ok {
		return updated
	}
	return row
}

// advanceIdentifying takes a staged request as far as it can go this pass.
func (s *Service) advanceIdentifying(ctx context.Context, row songRequestRow) {
	if row.trackID == "" {
		row.trackID = s.locateRequestedTrack(ctx, row)
		if row.trackID == "" {
			if requestAge(row.updated) > 2*time.Hour {
				s.noteRequest(ctx, row, "Downloaded, but samo has not found the file in the Explo folder yet. It will be picked up on the next library scan.")
			}
			return
		}
		if err := s.updateRequest(ctx, row.recordingID, `track_id = ?`, row.trackID); err != nil {
			s.logger("explo: link song request %s failed: %v", row.recordingID, err)
			return
		}
	}

	var ledger struct {
		status, recordingID, title, artist, coverStatus, processedAt string
		releaseGroup, album                                          string
		attempts                                                     int
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT et.status, et.musicbrainz_recording_id, et.matched_title, et.matched_artist,
		       et.cover_status, et.processed_at, et.attempts, COALESCE(et.musicbrainz_release_group_id, ''),
		       COALESCE(et.matched_album, '')
		FROM explo_tracks et JOIN music_tracks mt ON mt.id = et.track_id
		WHERE et.track_id = ?`, row.trackID).Scan(&ledger.status, &ledger.recordingID, &ledger.title,
		&ledger.artist, &ledger.coverStatus, &ledger.processedAt, &ledger.attempts, &ledger.releaseGroup, &ledger.album)
	switch {
	case err == sql.ErrNoRows:
		var exists int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM music_tracks WHERE id = ?`, row.trackID).Scan(&exists)
		if exists == 0 {
			// Rotated out (or rescanned under a new id) before it was kept.
			_ = s.updateRequest(ctx, row.recordingID, `track_id = ''`)
			return
		}
		s.noteRequest(ctx, row, "Downloaded. Waiting for samo to identify it.")
		return
	case err != nil:
		s.logger("explo: song request %s ledger lookup failed: %v", row.recordingID, err)
		return
	}

	if ledger.status != "matched" && ledger.status != "matched-fallback" {
		// Asked for from Deezer: MusicBrainz does not list the recording,
		// so nothing samo has can name the audio, now or after the usual
		// weeks of retries. The catalog it was chosen from names it, once
		// one identify pass has had its go, whatever came of it: a
		// fingerprinting error there is no reason to make the song wait an
		// hour for a retry that cannot do better. A song from a YouTube
		// Music playlist is the label's own audio under the label's names,
		// downloaded from the very video the playlist holds.
		if source, _ := listingSource(row.recordingID, row.album); source != "" {
			s.adoptCatalogIdentity(ctx, row)
			return
		}
		if ledger.attempts >= exploMaxIdentifyAttempts {
			s.settleRequest(ctx, row, RequestNeedsReview,
				"samo could not identify the downloaded audio. It is in Explore; Keep it from there if it is the right song.")
			return
		}
		s.noteRequest(ctx, row, "Downloaded, but not identified yet. samo keeps retrying on its usual schedule.")
		return
	}
	if !requestMatches(row, ledger.recordingID, ledger.title, ledger.artist) {
		s.settleRequest(ctx, row, RequestNeedsReview, fmt.Sprintf(
			"The downloaded audio is %q by %s, not the song you asked for. It is in Explore; Keep it from there if you want it.",
			ledger.title, ledger.artist))
		return
	}
	pinned := strings.EqualFold(ledger.releaseGroup, row.albumID)
	if !MusicBrainzID(row.albumID) {
		pinned = ledger.releaseGroup == "" && ledger.album == row.album
	}
	if row.albumID != "" && !pinned {
		// Identified before the request was linked to it (the pass that
		// pins the album, pinRequestedAlbum, did not know). Credit it to
		// the album now and let the cover pass fetch that album's art.
		if err := s.pinLedgerAlbum(ctx, row); err != nil {
			s.logger("explo: pin song request %s to its album failed: %v", row.recordingID, err)
			return
		}
		s.noteRequest(ctx, row, "Identified. Fetching cover art before adding it to your library.")
		return
	}
	// Real art, or the hour is up. A placeholder is not art: the copy is
	// kept without any, for good, so it waits out the hour for the cover
	// pass's next try — or for a track of the same album that got through.
	if ledger.coverStatus != coverStatusDone && s.adoptAlbumCover(ctx, row.trackID) {
		ledger.coverStatus = coverStatusDone
		if s.reloadCatalog != nil {
			// Keep embeds the cover the catalog shows for the track.
			if err := s.reloadCatalog(ctx); err != nil {
				s.logger("explo: catalog reload after sharing an album cover failed: %v", err)
			}
		}
	}
	if ledger.coverStatus != coverStatusDone && requestAge(ledger.processedAt) < requestCoverWait {
		s.noteRequest(ctx, row, "Identified. Fetching cover art before adding it to your library.")
		return
	}

	results, err := s.keep(ctx, []string{row.trackID}, row.placement())
	if err != nil {
		s.settleRequest(ctx, row, RequestNeedsReview, "samo could not add it to your library: "+err.Error())
		return
	}
	if len(results) == 0 {
		return
	}
	result := results[0]
	if result.Error != "" {
		if strings.Contains(result.Error, "album identified") && requestAge(ledger.processedAt) < requestAlbumWait {
			s.noteRequest(ctx, row, "Identified. Waiting for samo to settle which album it belongs to.")
			return
		}
		s.settleRequest(ctx, row, RequestNeedsReview, "samo could not add it to your library: "+result.Error)
		return
	}
	message := "Added to your library."
	if result.AlreadyInLibrary {
		message = "Already in your library."
	}
	if err := s.updateRequest(ctx, row.recordingID,
		`state = ?, message = ?, library_track_id = ?, library_path = ?`,
		RequestInLibrary, message, result.LibraryTrackID, result.Path); err != nil {
		s.logger("explo: settle song request %s failed: %v", row.recordingID, err)
		return
	}
	s.logger("explo: requested song %s / %s %s", row.artist, row.title, strings.ToLower(strings.TrimSuffix(message, ".")))
}

// catalogDurationTolerance is how far a download may differ in length from
// the catalog's track and still be taken for it. The download's own search
// already held it to the catalog's length; this catches a provider that let a
// music video's intro, or a different edit, through.
func catalogDurationTolerance(seconds int) int {
	return max(7, seconds/25)
}

// adoptCatalogIdentity identifies a download from the catalog listing of the
// song that was asked for (Deezer's, or a YouTube Music playlist's): the
// title, artist and album someone chose, written to the ledger and the catalog
// as identification would have written them. The one check left is the
// length, against the listing's.
func (s *Service) adoptCatalogIdentity(ctx context.Context, row songRequestRow) {
	source, catalogName := listingSource(row.recordingID, row.album)
	var seconds int
	var albumID sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT duration_seconds, album_id FROM music_tracks WHERE id = ?`, row.trackID).Scan(&seconds, &albumID); err != nil {
		s.logger("explo: song request %s track lookup failed: %v", row.recordingID, err)
		return
	}
	want := (row.durationMS + 500) / 1000
	if want > 0 && seconds > 0 && abs(seconds-want) > catalogDurationTolerance(want) {
		s.settleRequest(ctx, row, RequestNeedsReview, fmt.Sprintf(
			"The download is %s long; the song you asked for is %s. It is in Explore; Keep it from there if it is the right song.",
			clock(seconds), clock(want)))
		return
	}
	if row.albumID != "" {
		waiting, twin := s.albumTwin(ctx, row)
		if waiting {
			s.noteRequest(ctx, row, "Downloaded. Waiting for the rest of the album, to check no two of its tracks came down as the same audio.")
			return
		}
		if twin != nil {
			s.settleRequest(ctx, row, RequestNeedsReview, fmt.Sprintf(
				"The download is the same audio as track %d, %q, which also came down for this album: the source served one recording for both. It is in Explore; Keep it from there if it is the right song.",
				twin.trackNumber, twin.title))
			return
		}
	}
	match := identifiedTrack{Source: source, Title: row.title, Artist: row.artist, Album: row.album}
	if err := s.applyMatch(ctx, row.trackID, albumID.String, match); err != nil {
		s.logger("explo: apply catalog identity for song request %s failed: %v", row.recordingID, err)
		return
	}
	if err := s.recordProcessed(ctx, row.trackID, "matched-fallback", match, ""); err != nil {
		s.logger("explo: record catalog identity for song request %s failed: %v", row.recordingID, err)
		return
	}
	if err := s.resetTrackCoverState(ctx, row.trackID); err != nil {
		s.logger("explo: reset cover state for song request %s failed: %v", row.recordingID, err)
	}
	if s.reloadCatalog != nil {
		if err := s.reloadCatalog(ctx); err != nil {
			s.logger("explo: catalog reload after catalog identity failed: %v", err)
		}
	}
	why := "MusicBrainz does not know this recording"
	if source != "deezer" {
		why = "AcoustID did not recognise the audio"
	}
	s.noteRequest(ctx, row, "Identified from "+catalogName+"'s listing ("+why+"). Fetching cover art before adding it to your library.")
	s.logger("explo: requested song %s / %s identified from %s's listing", row.artist, row.title, catalogName)
}

// albumTwin finds the other track of a whole-album request that came down as
// the same audio as this one. Length is the only other check a Deezer listing
// allows, and an upload naming the album passed it for Quangou's title track:
// "sloth in motion" was kept with "forest of no return"'s audio, seven
// seconds short. Two tracks of one album are never one recording, so the
// track whose listed length the audio fits worse is the wrong one; on a tie,
// both are. The tracks still downloading are waited for (waiting), so the
// wrong one is not kept for coming down first.
//
// A file fpcalc cannot read is no evidence either way: the listing is still
// taken, as it is when identification could not fingerprint the file.
func (s *Service) albumTwin(ctx context.Context, row songRequestRow) (waiting bool, twin *songRequestRow) {
	siblings, err := s.queryRequests(ctx, `album_id = ? AND recording_id <> ?`, row.albumID, row.recordingID)
	if err != nil {
		s.logger("explo: album tracks of song request %s failed: %v", row.recordingID, err)
		return false, nil
	}
	for _, sibling := range siblings {
		if sibling.state == RequestDownloading {
			return true, nil
		}
	}
	path := s.requestDropPath(ctx, row.trackID)
	if path == "" {
		return false, nil
	}
	audio, seconds, err := rawFingerprint(ctx, s.fpcalcPath, path)
	if err != nil {
		s.logger("explo: compare song request %s with its album: %v", row.recordingID, err)
		return false, nil
	}
	fit := abs(seconds - (row.durationMS+500)/1000)
	for i, sibling := range siblings {
		if sibling.state == RequestFailed {
			continue
		}
		other := sibling.libraryPath
		if sibling.state != RequestInLibrary || other == "" {
			other = s.requestDropPath(ctx, sibling.trackID)
		}
		if other == "" {
			continue
		}
		otherAudio, otherSeconds, err := rawFingerprint(ctx, s.fpcalcPath, other)
		if err != nil || fingerprintSimilarity(audio, otherAudio) < sameAudioSimilarity {
			continue
		}
		if fit < abs(otherSeconds-(sibling.durationMS+500)/1000) {
			// This track is the recording; the sibling has its audio.
			if sibling.state == RequestInLibrary {
				s.logger("explo: song request %s / %s was kept with the audio of %s / %s",
					sibling.artist, sibling.title, row.artist, row.title)
			}
			continue
		}
		return false, &siblings[i]
	}
	return false, nil
}

// requestDropPath is where a requested download sits in the explo folder.
func (s *Service) requestDropPath(ctx context.Context, trackID string) string {
	if trackID == "" {
		return ""
	}
	clause, args := exploPathClause(s.effectiveDirs())
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT mf.path FROM media_files mf WHERE mf.track_id = ? AND `+clause+` ORDER BY mf.path LIMIT 1`,
		append([]any{trackID}, args...)...).Scan(&path)
	if err != nil && err != sql.ErrNoRows {
		s.logger("explo: drop file of track %s: %v", trackID, err)
	}
	return path
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func clock(seconds int) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// pinLedgerAlbum credits an identified track of a whole-album request to that
// album in the ledger, as pinRequestedAlbum would have at identification, and
// sends it back to the cover pass for the album's art. processed_at moves so
// the request's wait for that art starts now.
func (s *Service) pinLedgerAlbum(ctx context.Context, row songRequestRow) error {
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE explo_tracks
			SET matched_album = ?, musicbrainz_release_group_id = ?, processed_at = CURRENT_TIMESTAMP,
			    cover_status = '', cover_attempts = 0, cover_attempted_at = ''
			WHERE track_id = ?`, row.album, musicBrainzOnly(row.albumID), row.trackID)
		return err
	})
}

// requestMatches reports whether an identified drop is the song that was asked
// for. The same recording id settles it; otherwise both the title and the
// artist have to agree, allowing for one side naming more ("Song" against
// "Song (Remastered)", "Artist" against "Artist & Guest"). Without this a
// wrong YouTube upload — a cover, a different song with the same title — would
// be filed into the library under its real identity, unasked.
func requestMatches(row songRequestRow, recordingID, title, artist string) bool {
	if recordingID != "" && strings.EqualFold(recordingID, row.recordingID) {
		return true
	}
	titleAgrees := tokenAgreement(row.title, title) > 0
	artistAgrees := tokenAgreement(row.artist, artist) > 0 ||
		keepArtistsMatch(normalizeKeepIdentity(row.artist), normalizeKeepIdentity(artist))
	return titleAgrees && artistAgrees
}

// locateRequestedTrack finds the drop a request downloaded: first by the file
// Explo reports it staged, then by the recording id the file is tagged with
// (Explo tags YouTube downloads with the requested recording; a Soulseek file
// keeps its sharer's tags, so it is only found by name).
func (s *Service) locateRequestedTrack(ctx context.Context, row songRequestRow) string {
	dirs := s.effectiveDirs()
	if len(dirs) == 0 {
		return ""
	}
	clause, args := exploPathClause(dirs)
	if row.stagedFile != "" {
		var trackID string
		err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT mt.id FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
			WHERE %s AND mf.path LIKE ? ESCAPE '\'
			ORDER BY mt.added_at DESC, mt.id LIMIT 1`, clause),
			append(args, "%/"+escapeLike(row.stagedFile))...).Scan(&trackID)
		if err == nil {
			return trackID
		}
		if err != sql.ErrNoRows {
			s.logger("explo: locate song request %s failed: %v", row.recordingID, err)
		}
	}
	var trackID string
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT mt.id FROM music_tracks mt JOIN media_files mf ON mf.track_id = mt.id
		WHERE %s AND mt.external_ids_json LIKE ?
		ORDER BY mt.added_at DESC, mt.id LIMIT 1`, clause),
		append(args, `%"musicBrainzRecordingId":"`+row.recordingID+`"%`)...).Scan(&trackID)
	if err != nil && err != sql.ErrNoRows {
		s.logger("explo: locate song request %s by recording failed: %v", row.recordingID, err)
	}
	return trackID
}

// resolveRequestLibraryTrack fills in the library copy's id when Keep's own
// wait ran out before the scanner catalogued it.
func (s *Service) resolveRequestLibraryTrack(ctx context.Context, row songRequestRow) {
	if row.libraryTrackID != "" || row.libraryPath == "" {
		return
	}
	var trackID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT track_id FROM media_files WHERE path = ? AND track_id IS NOT NULL LIMIT 1`,
		row.libraryPath).Scan(&trackID); err == nil && trackID != "" {
		_ = s.updateRequest(ctx, row.recordingID, `library_track_id = ?`, trackID)
	}
}

func (s *Service) resolvePendingLibraryTracks(ctx context.Context) {
	rows, err := s.queryRequests(ctx, `state = ? AND library_track_id = '' AND library_path <> ''`, RequestInLibrary)
	if err != nil {
		return
	}
	for _, row := range rows {
		s.resolveRequestLibraryTrack(ctx, row)
	}
}

func (s *Service) openRequests(ctx context.Context) ([]songRequestRow, error) {
	return s.queryRequests(ctx, `state IN (?, ?)`, RequestDownloading, RequestIdentifying)
}

func (s *Service) loadRequest(ctx context.Context, recordingID string) (songRequestRow, bool, error) {
	rows, err := s.queryRequests(ctx, `recording_id = ?`, strings.TrimSpace(recordingID))
	if err != nil || len(rows) == 0 {
		return songRequestRow{}, false, err
	}
	return rows[0], true, nil
}

func (s *Service) queryRequests(ctx context.Context, where string, args ...any) ([]songRequestRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT recording_id, title, artist, state, staged_file, track_id, library_track_id,
		       library_path, message, created_at, updated_at,
		       album, album_id, album_artist, track_number, disc_number, disc_total, release_year, duration_ms
		FROM explo_requests WHERE `+where+` ORDER BY created_at, recording_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []songRequestRow
	for rows.Next() {
		var row songRequestRow
		if err := rows.Scan(&row.recordingID, &row.title, &row.artist, &row.state, &row.stagedFile, &row.trackID,
			&row.libraryTrackID, &row.libraryPath, &row.message, &row.createdAt, &row.updated,
			&row.album, &row.albumID, &row.albumArtist, &row.trackNumber, &row.discNumber, &row.discTotal, &row.year,
			&row.durationMS); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Service) updateRequest(ctx context.Context, recordingID, set string, args ...any) error {
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `UPDATE explo_requests SET `+set+`, updated_at = CURRENT_TIMESTAMP WHERE recording_id = ?`,
			append(args, recordingID)...)
		return err
	})
}

// noteRequest updates what a request is waiting on. It leaves updated_at
// alone, so the request's age keeps measuring time since it last moved.
func (s *Service) noteRequest(ctx context.Context, row songRequestRow, message string) {
	if row.message == message {
		return
	}
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `UPDATE explo_requests SET message = ? WHERE recording_id = ?`, message, row.recordingID)
		return err
	}); err != nil {
		s.logger("explo: note song request %s failed: %v", row.recordingID, err)
	}
}

func (s *Service) settleRequest(ctx context.Context, row songRequestRow, state, message string) {
	if err := s.updateRequest(ctx, row.recordingID, `state = ?, message = ?`, state, message); err != nil {
		s.logger("explo: settle song request %s failed: %v", row.recordingID, err)
		return
	}
	s.logger("explo: requested song %s / %s: %s", row.artist, row.title, message)
}

// requestAge is how long ago a stored timestamp was. An unreadable one counts
// as just now, so nothing times out on a parse failure.
func requestAge(stamp string) time.Duration {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
	if err != nil {
		return 0
	}
	return time.Since(at)
}

// cleanStagedFile accepts Explo's report of where it put a file only as a
// relative path inside the drop folder.
func cleanStagedFile(file string) string {
	file = strings.TrimSpace(strings.ReplaceAll(file, `\`, "/"))
	file = strings.TrimLeft(file, "/")
	if file == "" || file == "." || strings.HasPrefix(file, "../") || strings.Contains(file, "/../") || file == ".." {
		return ""
	}
	return file
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
