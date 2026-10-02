package explo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage"
)

// A YouTube Music playlist imported with Explo becomes a samo playlist of the
// same name. The songs the library already has go in at once; every other
// song is requested through Explo exactly as Search for new requests one (an
// explo_requests row, identified and kept by AdvanceRequests), and joins the
// playlist where YouTube Music has it once it is in the library. Nothing
// about a track's identity or filing is decided here: that is the request
// pipeline's, so a playlist track that turns out to be the wrong song stops
// for review like any other request and never joins the playlist.

const (
	importTrackLibrary     = "library"
	importTrackQueued      = "queued"
	importTrackRequested   = "requested"
	importTrackUnavailable = "unavailable"
)

// PlaylistImport is an imported playlist as the browser follows it.
type PlaylistImport struct {
	PlaylistID string `json:"playlistId"`
	Title      string `json:"title"`
	SourceID   string `json:"sourceId"`
	SourceURL  string `json:"sourceUrl,omitempty"`
	// Unavailable is how many tracks YouTube Music lists but cannot play.
	Unavailable int                   `json:"unavailable,omitempty"`
	Tracks      []PlaylistImportTrack `json:"tracks"`
}

// PlaylistImportTrack is one track of an imported playlist. State is
// in-library once it is in the playlist; queued while samo waits to hand it to
// Explo; then the request's own state (downloading, identifying, in-library,
// needs-review, failed); or unavailable when Explo could not take it.
type PlaylistImportTrack struct {
	Position       int    `json:"position"`
	RecordingID    string `json:"recordingId"`
	Title          string `json:"title"`
	Artist         string `json:"artist"`
	Album          string `json:"album,omitempty"`
	DurationMS     int    `json:"durationMs,omitempty"`
	State          string `json:"state"`
	Message        string `json:"message,omitempty"`
	LibraryTrackID string `json:"libraryTrackId,omitempty"`
	// ReviewTrackID is the downloaded copy in Explore of a track that needs
	// review: Keep it and it joins the playlist.
	ReviewTrackID string `json:"reviewTrackId,omitempty"`
}

type playlistImportRow struct {
	playlistID, sourceID, requestedBy string
}

type importTrackRow struct {
	playlistID, recordingID, title, artist, album, state, libraryTrackID string
	position, durationMS                                                 int
}

// ImportPlaylist makes the playlist name (the YouTube Music playlist's own
// name when empty) hold listing: the tracks the library has now, in listing
// order, ahead of any the playlist already held; and records the rest to be
// requested. Importing the same playlist again reads it afresh, keeps what
// someone added by hand and asks only for what is still missing. Feeding the
// requests to Explo is AdvancePlaylistImports's, which the caller starts.
func (s *Service) ImportPlaylist(ctx context.Context, ownerID, name, sourceURL string, public bool, listing Playlist) (catalog.MusicPlaylist, error) {
	if s == nil || s.db == nil || s.playlists == nil {
		return catalog.MusicPlaylist{}, ErrDisabled
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(listing.Title)
	}
	if name == "" {
		name = "YouTube Music playlist"
	}
	s.importsMu.Lock()
	defer s.importsMu.Unlock()

	playlist, err := s.playlists.EnsureNamed(ctx, ownerID, name, public)
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}
	tracks := make([]importTrackRow, 0, len(listing.Tracks))
	var inLibrary []string
	for _, song := range listing.Tracks {
		if YouTubeID(song.ID) == "" {
			continue
		}
		row := importTrackRow{playlistID: playlist.ID, position: len(tracks), recordingID: song.ID,
			title: song.Title, artist: song.Artist, album: song.Album, durationMS: song.DurationMS, state: importTrackQueued}
		if id := s.importLibraryTrack(ctx, row); id != "" {
			row.state, row.libraryTrackID = importTrackLibrary, id
			if !slices.Contains(inLibrary, id) {
				inLibrary = append(inLibrary, id)
			}
		} else if request, ok, err := s.Request(ctx, song.ID); err == nil && ok && request.State != RequestFailed {
			// Already asked for (by an earlier import, say): follow it.
			row.state = importTrackRequested
		}
		tracks = append(tracks, row)
	}

	err = storage.Retry(ctx, exploWriteAttempts, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO explo_playlist_imports (playlist_id, source_id, source_url, title, requested_by, unavailable)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (playlist_id) DO UPDATE SET
			  source_id = excluded.source_id, source_url = excluded.source_url, title = excluded.title,
			  requested_by = excluded.requested_by, unavailable = excluded.unavailable, updated_at = CURRENT_TIMESTAMP`,
			playlist.ID, listing.ID, sourceURL, listing.Title, ownerID, listing.Unavailable); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM explo_playlist_import_tracks WHERE playlist_id = ?`, playlist.ID); err != nil {
			return err
		}
		for _, row := range tracks {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO explo_playlist_import_tracks
				  (playlist_id, position, recording_id, title, artist, album, duration_ms, state, library_track_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.playlistID, row.position, row.recordingID, row.title, row.artist, row.album, row.durationMS,
				row.state, row.libraryTrackID); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return catalog.MusicPlaylist{}, fmt.Errorf("record playlist import: %w", err)
	}

	trackIDs := append([]string(nil), inLibrary...)
	for _, id := range playlist.TrackIDs {
		if !slices.Contains(trackIDs, id) {
			trackIDs = append(trackIDs, id)
		}
	}
	if slices.Equal(trackIDs, playlist.TrackIDs) {
		s.installPlaylist(ctx, playlist)
		return playlist, nil
	}
	updated, err := s.playlists.Update(ctx, ownerID, playlist.ID, playlists.UpdateInput{TrackIDs: trackIDs})
	if err != nil {
		return catalog.MusicPlaylist{}, err
	}
	s.installPlaylist(ctx, updated)
	return updated, nil
}

// importLibraryTrack is the library track that already is this playlist
// track: the request's own copy once it was kept, or the library twin Keep
// itself would find (same song by the same artist, at the same length; never
// an explo drop, never a live cut standing in for the studio take).
func (s *Service) importLibraryTrack(ctx context.Context, row importTrackRow) string {
	var kept string
	err := s.db.QueryRowContext(ctx, `
		SELECT er.library_track_id FROM explo_requests er JOIN music_tracks mt ON mt.id = er.library_track_id
		WHERE er.recording_id = ? AND er.state = ? AND mt.is_explo = 0`, row.recordingID, RequestInLibrary).Scan(&kept)
	if err == nil && kept != "" {
		return kept
	}
	id, _ := s.findLibraryTwin(ctx, catalog.MusicTrack{
		Title:           row.title,
		DisplayArtist:   row.artist,
		DurationSeconds: (row.durationMS + 500) / 1000,
	}, "")
	return id
}

// AdvancePlaylistImports moves every import on: tracks whose request reached
// the library join their playlist, and tracks waiting for Explo are handed to
// it until it says it is full. remote may be nil, when Explo is not
// connected; landed tracks still join.
func (s *Service) AdvancePlaylistImports(ctx context.Context, remote *Remote) {
	if s == nil || s.db == nil || s.playlists == nil {
		return
	}
	s.importsMu.Lock()
	defer s.importsMu.Unlock()

	landed, err := s.queryImportTracks(ctx, `
		t.state = ? AND EXISTS (SELECT 1 FROM explo_requests er JOIN music_tracks mt ON mt.id = er.library_track_id
		  WHERE er.recording_id = t.recording_id AND er.state = ? AND mt.is_explo = 0)`, importTrackRequested, RequestInLibrary)
	if err != nil {
		s.logger("explo: load landed playlist tracks failed: %v", err)
		return
	}
	trackIDs := make([]string, len(landed))
	for i, row := range landed {
		_ = s.db.QueryRowContext(ctx, `SELECT library_track_id FROM explo_requests WHERE recording_id = ?`, row.recordingID).Scan(&trackIDs[i])
	}
	s.quietLandedAlbums(ctx, trackIDs)
	for i, row := range landed {
		if ctx.Err() != nil {
			return
		}
		if trackIDs[i] != "" {
			s.placeImportTrack(ctx, row, trackIDs[i])
		}
	}

	queued, err := s.queryImportTracks(ctx, `t.state = ?`, importTrackQueued)
	if err != nil {
		s.logger("explo: load queued playlist tracks failed: %v", err)
		return
	}
	for _, row := range queued {
		if ctx.Err() != nil {
			return
		}
		// In the library by now (asked for elsewhere, or a scan found it).
		if id := s.importLibraryTrack(ctx, row); id != "" {
			s.placeImportTrack(ctx, row, id)
			continue
		}
		if remote == nil {
			continue
		}
		imported, ok := s.loadPlaylistImport(ctx, row.playlistID)
		if !ok {
			continue
		}
		job, err := remote.AddPlaylistTrack(ctx, row.recordingID, imported.sourceID)
		var remoteErr *RemoteError
		switch {
		case errors.As(err, &remoteErr) && remoteErr.Status == 404:
			s.setImportTrack(ctx, row, importTrackUnavailable, "",
				"Explo could not find this track in the YouTube Music playlist any more.")
			continue
		case err != nil:
			// Full, or away: the rest wait for the next pass.
			return
		}
		if job.Song.Title == "" {
			job.Song = Song{ID: row.recordingID, Source: "youtube", Title: row.title, Artist: row.artist,
				Album: row.album, DurationMS: row.durationMS}
		}
		if err := s.RecordRequest(ctx, job, imported.requestedBy); err != nil {
			s.logger("explo: record playlist track request %s failed: %v", row.recordingID, err)
		}
		// The playlist's song, not one added by hand: its album stays off
		// Recently Added (migration 0033).
		if err := s.updateRequest(ctx, row.recordingID, `for_playlist = TRUE`); err != nil {
			s.logger("explo: mark playlist track request %s failed: %v", row.recordingID, err)
		}
		s.setImportTrack(ctx, row, importTrackRequested, "", "")
	}
}

// quietLandedAlbums makes the albums of a playlist's newly kept songs leave
// Recently Added before the songs are announced in the playlist: the catalog
// derives that from the requests (catalogstore/load_seed.go), so it is
// reloaded, and the albums are touched so clients that sync by updated_at
// fetch them again.
func (s *Service) quietLandedAlbums(ctx context.Context, trackIDs []string) {
	touched := int64(0)
	for _, id := range trackIDs {
		if id == "" {
			continue
		}
		n, err := s.execCount(ctx, `
			UPDATE music_albums SET updated_at = CURRENT_TIMESTAMP
			WHERE id = (SELECT mt.album_id FROM music_tracks mt JOIN explo_requests er ON er.library_track_id = mt.id
			            WHERE mt.id = ? AND er.for_playlist AND er.library_copy LIMIT 1)`, id)
		if err != nil {
			s.logger("explo: touch album of playlist track %s failed: %v", id, err)
		}
		touched += n
	}
	if touched > 0 && s.reloadCatalog != nil {
		if err := s.reloadCatalog(ctx); err != nil {
			s.logger("explo: catalog reload after playlist songs landed failed: %v", err)
		}
	}
}

// RetryPlaylistImport hands the playlist's failed and refused tracks back to
// Explo on the next AdvancePlaylistImports. It reports how many.
func (s *Service) RetryPlaylistImport(ctx context.Context, playlistID string) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrDisabled
	}
	s.importsMu.Lock()
	defer s.importsMu.Unlock()
	var retried int64
	err := storage.Retry(ctx, exploWriteAttempts, func() error {
		result, err := s.db.ExecContext(ctx, `
			UPDATE explo_playlist_import_tracks SET state = ?, message = ''
			WHERE playlist_id = ? AND (state = ? OR (state = ? AND recording_id IN (
			  SELECT recording_id FROM explo_requests WHERE state = ?)))`,
			importTrackQueued, playlistID, importTrackUnavailable, importTrackRequested, RequestFailed)
		if err != nil {
			return err
		}
		retried, err = result.RowsAffected()
		return err
	})
	return int(retried), err
}

// placeImportTrack puts a library track into its import's playlist after the
// nearest earlier track of the YouTube Music playlist that is there (before
// the nearest later one, when none is), so the playlist keeps YouTube
// Music's order whatever order the downloads finish in. A track already in
// the playlist stays where it is; one someone removed after it joined is not
// put back, as it joins only once.
func (s *Service) placeImportTrack(ctx context.Context, row importTrackRow, trackID string) {
	imported, ok := s.loadPlaylistImport(ctx, row.playlistID)
	if !ok {
		return
	}
	playlist, err := s.playlists.Get(ctx, row.playlistID)
	if errors.Is(err, playlists.ErrNotFound) {
		// Deleted: the requests still finish into the library.
		s.forgetPlaylistImport(ctx, row.playlistID)
		return
	}
	if err != nil {
		s.logger("explo: load imported playlist %s failed: %v", row.playlistID, err)
		return
	}
	if !slices.Contains(playlist.TrackIDs, trackID) {
		placed, err := s.placedImportTracks(ctx, row.playlistID)
		if err != nil {
			s.logger("explo: load imported playlist %s tracks failed: %v", row.playlistID, err)
			return
		}
		at := func(position int) int {
			if id := placed[position]; id != "" {
				return slices.Index(playlist.TrackIDs, id)
			}
			return -1
		}
		index := -1
		for position := row.position - 1; position >= 0 && index < 0; position-- {
			if i := at(position); i >= 0 {
				index = i + 1
			}
		}
		last := maxKey(placed)
		for position := row.position + 1; position <= last && index < 0; position++ {
			index = at(position)
		}
		if index < 0 {
			index = len(playlist.TrackIDs)
		}
		trackIDs := slices.Insert(append([]string(nil), playlist.TrackIDs...), index, trackID)
		updated, err := s.playlists.Update(ctx, imported.requestedBy, row.playlistID, playlists.UpdateInput{TrackIDs: trackIDs})
		if err != nil {
			s.logger("explo: add %s to imported playlist %s failed: %v", trackID, row.playlistID, err)
			return
		}
		s.installPlaylist(ctx, updated)
	}
	s.setImportTrack(ctx, row, importTrackLibrary, trackID, "")
}

func maxKey(m map[int]string) int {
	most := -1
	for key := range m {
		most = max(most, key)
	}
	return most
}

// installPlaylist puts a changed playlist into the live projection.
func (s *Service) installPlaylist(ctx context.Context, playlist catalog.MusicPlaylist) {
	if s.applyPlaylist != nil {
		s.applyPlaylist(playlist)
		return
	}
	if s.reloadCatalog != nil {
		if err := s.reloadCatalog(ctx); err != nil {
			s.logger("explo: catalog reload after playlist import failed: %v", err)
		}
	}
}

// PlaylistImport returns samo's record of an imported playlist; ok is false
// when the playlist was not imported with Explo.
func (s *Service) PlaylistImport(ctx context.Context, playlistID string) (PlaylistImport, bool, error) {
	if s == nil || s.db == nil {
		return PlaylistImport{}, false, nil
	}
	view := PlaylistImport{PlaylistID: playlistID, Tracks: []PlaylistImportTrack{}}
	err := s.db.QueryRowContext(ctx, `
		SELECT title, source_id, source_url, unavailable FROM explo_playlist_imports WHERE playlist_id = ?`,
		playlistID).Scan(&view.Title, &view.SourceID, &view.SourceURL, &view.Unavailable)
	if err == sql.ErrNoRows {
		return PlaylistImport{}, false, nil
	}
	if err != nil {
		return PlaylistImport{}, false, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.position, t.recording_id, t.title, t.artist, t.album, t.duration_ms, t.state, t.library_track_id,
		       t.message, COALESCE(er.state, ''), COALESCE(er.message, ''), COALESCE(er.track_id, '')
		FROM explo_playlist_import_tracks t
		LEFT JOIN explo_requests er ON er.recording_id = t.recording_id
		WHERE t.playlist_id = ? ORDER BY t.position`, playlistID)
	if err != nil {
		return PlaylistImport{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var track PlaylistImportTrack
		var state, message, requestState, requestMessage, dropTrack string
		if err := rows.Scan(&track.Position, &track.RecordingID, &track.Title, &track.Artist, &track.Album,
			&track.DurationMS, &state, &track.LibraryTrackID, &message, &requestState, &requestMessage, &dropTrack); err != nil {
			return PlaylistImport{}, false, err
		}
		switch {
		case state == importTrackLibrary:
			track.State = RequestInLibrary
		case state == importTrackRequested && requestState != "":
			track.State, track.Message = requestState, requestMessage
			switch requestState {
			case RequestInLibrary:
				track.Message = "In your library. It joins the playlist on samo's next pass."
			case RequestNeedsReview:
				track.ReviewTrackID = dropTrack
			}
		case state == importTrackUnavailable:
			track.State, track.Message = importTrackUnavailable, message
		default:
			track.State, track.Message = importTrackQueued, "Waiting for Explo to take it."
		}
		view.Tracks = append(view.Tracks, track)
	}
	return view, true, rows.Err()
}

func (s *Service) loadPlaylistImport(ctx context.Context, playlistID string) (playlistImportRow, bool) {
	row := playlistImportRow{playlistID: playlistID}
	err := s.db.QueryRowContext(ctx, `SELECT source_id, requested_by FROM explo_playlist_imports WHERE playlist_id = ?`,
		playlistID).Scan(&row.sourceID, &row.requestedBy)
	if err != nil {
		if err != sql.ErrNoRows {
			s.logger("explo: load playlist import %s failed: %v", playlistID, err)
		}
		return row, false
	}
	return row, true
}

func (s *Service) placedImportTracks(ctx context.Context, playlistID string) (map[int]string, error) {
	rows, err := s.queryImportTracks(ctx, `t.playlist_id = ? AND t.state = ?`, playlistID, importTrackLibrary)
	if err != nil {
		return nil, err
	}
	placed := make(map[int]string, len(rows))
	for _, row := range rows {
		placed[row.position] = row.libraryTrackID
	}
	return placed, nil
}

func (s *Service) queryImportTracks(ctx context.Context, where string, args ...any) ([]importTrackRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.playlist_id, t.position, t.recording_id, t.title, t.artist, t.album, t.duration_ms, t.state, t.library_track_id
		FROM explo_playlist_import_tracks t JOIN explo_playlist_imports i ON i.playlist_id = t.playlist_id
		WHERE `+where+` ORDER BY i.created_at, t.playlist_id, t.position`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []importTrackRow
	for rows.Next() {
		var row importTrackRow
		if err := rows.Scan(&row.playlistID, &row.position, &row.recordingID, &row.title, &row.artist, &row.album,
			&row.durationMS, &row.state, &row.libraryTrackID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Service) setImportTrack(ctx context.Context, row importTrackRow, state, libraryTrackID, message string) {
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `
			UPDATE explo_playlist_import_tracks SET state = ?, library_track_id = ?, message = ?
			WHERE playlist_id = ? AND position = ?`, state, libraryTrackID, message, row.playlistID, row.position)
		return err
	}); err != nil {
		s.logger("explo: update playlist import track %s failed: %v", row.recordingID, err)
	}
}

func (s *Service) forgetPlaylistImport(ctx context.Context, playlistID string) {
	if err := storage.Retry(ctx, exploWriteAttempts, func() error {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM explo_playlist_import_tracks WHERE playlist_id = ?`, playlistID); err != nil {
			return err
		}
		_, err := s.db.ExecContext(ctx, `DELETE FROM explo_playlist_imports WHERE playlist_id = ?`, playlistID)
		return err
	}); err != nil {
		s.logger("explo: forget playlist import %s failed: %v", playlistID, err)
	}
}
