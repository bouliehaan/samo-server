package catalogstore

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// An album a YouTube Music playlist import brought is in the library but not
// on Recently Added; an album with anything else in it — a track someone
// added, or a song the import found already there — is.
func TestPlaylistOnlyAlbumsLeaveRecentlyAdded(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, album := range []string{"imported", "mixed", "found", "plain"} {
		exec(`INSERT INTO music_albums (id, title, track_count) VALUES (?, ?, 1)`, album, album)
	}
	exec(`INSERT INTO music_tracks (id, title, album_id) VALUES ('t-imported', 'Iris', 'imported'), ('t-mixed-copy', 'Slide', 'mixed'),
		('t-mixed-mine', 'Black Balloon', 'mixed'), ('t-found', 'Name', 'found'), ('t-plain', 'Mine', 'plain')`)
	// An explo drop in the imported album does not make it the library's.
	exec(`INSERT INTO music_tracks (id, title, album_id, is_explo) VALUES ('t-drop', 'Iris', 'imported', 1)`)
	exec(`INSERT INTO explo_requests (recording_id, state, library_track_id, for_playlist, library_copy) VALUES
		('youtube-aaaaaaaaaaa', 'in-library', 't-imported', TRUE, TRUE),
		('youtube-bbbbbbbbbbb', 'in-library', 't-mixed-copy', TRUE, TRUE),
		('youtube-ccccccccccc', 'in-library', 't-found', TRUE, FALSE)`)
	seed, err := LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hidden := map[string]bool{}
	for _, album := range seed.MusicAlbums {
		hidden[album.ID] = album.HiddenFromRecentlyAdded
		if album.IsExplo {
			t.Errorf("%s marked explo", album.ID)
		}
	}
	if !hidden["imported"] || hidden["mixed"] || hidden["found"] || hidden["plain"] {
		t.Fatalf("hidden = %v", hidden)
	}
}
