package explo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
)

// A whole album requested through Search for new is its tracks, each its own
// request (requests.go) carrying the album it was asked for with. Nothing
// about an album request lives anywhere else: the album is done when its
// tracks are, and asking for it again asks again only for the tracks that
// failed.

// AlbumRequestTrack is one track of a requested album as samo has it.
type AlbumRequestTrack struct {
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Number     int    `json:"number"`
	Disc       int    `json:"disc"`
	DurationMS int    `json:"durationMs,omitempty"`
	SongRequest
}

// AlbumRequest is samo's record of an album asked for whole.
type AlbumRequest struct {
	AlbumID string              `json:"albumId"`
	Title   string              `json:"title"`
	Artist  string              `json:"artist"`
	Tracks  []AlbumRequestTrack `json:"tracks"`
}

// AlbumRequest returns the tracks requested as part of an album, in album
// order. ok is false when nothing was ever requested with it.
func (s *Service) AlbumRequest(ctx context.Context, albumID string) (AlbumRequest, bool, error) {
	if s == nil || s.db == nil {
		return AlbumRequest{}, false, nil
	}
	rows, err := s.queryRequests(ctx, `album_id = ?`, strings.TrimSpace(albumID))
	if err != nil || len(rows) == 0 {
		return AlbumRequest{}, false, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].discNumber != rows[j].discNumber {
			return rows[i].discNumber < rows[j].discNumber
		}
		return rows[i].trackNumber < rows[j].trackNumber
	})
	album := AlbumRequest{AlbumID: rows[0].albumID, Title: rows[0].album, Artist: rows[0].albumArtist}
	for _, row := range rows {
		request, _, err := s.Request(ctx, row.recordingID)
		if err != nil {
			return AlbumRequest{}, false, err
		}
		album.Tracks = append(album.Tracks, AlbumRequestTrack{Title: row.title, Artist: row.artist,
			Number: row.trackNumber, Disc: row.discNumber, DurationMS: row.durationMS, SongRequest: request})
	}
	return album, true, nil
}

// AlbumTrackInLibrary returns the library track that already is this track of
// this album — the same recording, or the same song by the same artist at the
// same length, filed under an album of that name — so asking for an album
// whose songs are partly in the library downloads only the rest.
func (s *Service) AlbumTrackInLibrary(ctx context.Context, song Song, albumTitle string) string {
	if s == nil || s.db == nil || strings.TrimSpace(albumTitle) == "" {
		return ""
	}
	id, _ := s.findLibraryTwin(ctx, catalog.MusicTrack{
		Title:           song.Title,
		DisplayArtist:   song.Artist,
		DurationSeconds: (song.DurationMS + 500) / 1000,
		ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: musicBrainzOnly(song.ID)},
	}, albumTitle)
	return id
}

// RecordAlbumTrackInLibrary notes a track of a requested album that the
// library already holds on that album, without downloading it again.
func (s *Service) RecordAlbumTrackInLibrary(ctx context.Context, song Song, placed AlbumTrack, libraryTrackID, requestedBy string) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	if strings.TrimSpace(song.ID) == "" || strings.TrimSpace(placed.AlbumID) == "" {
		return fmt.Errorf("album track has no recording or album id")
	}
	return storage.Retry(ctx, exploWriteAttempts, func() error {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO explo_requests (recording_id, title, artist, album, duration_ms, requested_by, state, message,
			  library_track_id, album_id, album_artist, track_number, disc_number, disc_total, release_year)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (recording_id) DO UPDATE SET
			  album = excluded.album, album_id = excluded.album_id, album_artist = excluded.album_artist,
			  track_number = excluded.track_number, disc_number = excluded.disc_number,
			  disc_total = excluded.disc_total, release_year = excluded.release_year,
			  state = excluded.state, message = excluded.message, library_track_id = excluded.library_track_id,
			  updated_at = CURRENT_TIMESTAMP`,
			song.ID, song.Title, song.Artist, placed.AlbumTitle, song.DurationMS, requestedBy, RequestInLibrary,
			"Already in your library.", libraryTrackID, placed.AlbumID, placed.AlbumArtist, placed.Number,
			placed.Disc, placed.DiscTotal, placed.Year)
		return err
	})
}

// AttachToAlbum makes an existing request a track of a requested album. One
// still under way is identified and kept as that album's track; one already
// settled keeps its state, and only shows with the album.
func (s *Service) AttachToAlbum(ctx context.Context, recordingID string, placed AlbumTrack) error {
	if s == nil || s.db == nil {
		return ErrDisabled
	}
	return s.updateRequest(ctx, strings.TrimSpace(recordingID), `
		album = ?, album_id = ?, album_artist = ?, track_number = ?, disc_number = ?, disc_total = ?, release_year = ?`,
		placed.AlbumTitle, placed.AlbumID, placed.AlbumArtist, placed.Number, placed.Disc, placed.DiscTotal, placed.Year)
}
