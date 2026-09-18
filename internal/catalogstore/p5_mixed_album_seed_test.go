package catalogstore

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// P5 (2026-09-17): the scanner's denormalized music_albums.track_count counts
// every row carrying the album id, drop-folder twin included. The projection
// the server serves must count what the album view lists.
func TestP5LoadSeedRecountsMixedAlbum(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`INSERT INTO libraries (id, name, kind, path) VALUES ('music-library', 'Music', 'music', '/music')`)
	exec(`INSERT INTO music_artists (id, name) VALUES ('artist-1', 'Radiohead')`)
	// The scanner counted both files under the album.
	exec(`INSERT INTO music_albums (id, title, display_artist, track_count, duration_seconds, hidden_from_recently_added)
	      VALUES ('album-mixed', 'Pablo Honey', 'Radiohead', 2, 477, 0)`)
	exec(`INSERT INTO music_albums (id, title, display_artist, track_count, duration_seconds, hidden_from_recently_added)
	      VALUES ('album-explo', 'Weekly Drop', 'Someone', 2, 401, 1)`)
	exec(`INSERT INTO music_album_artists (album_id, artist_id, position) VALUES ('album-mixed', 'artist-1', 0)`)
	exec(`INSERT INTO music_tracks (id, title, album_id, album_title, duration_seconds, is_explo)
	      VALUES ('track-kept', 'Creep', 'album-mixed', 'Pablo Honey', 239, 0)`)
	exec(`INSERT INTO music_tracks (id, title, album_id, album_title, duration_seconds, is_explo)
	      VALUES ('track-twin', 'Creep', 'album-mixed', 'Pablo Honey', 238, 1)`)
	exec(`INSERT INTO music_tracks (id, title, album_id, album_title, duration_seconds, is_explo)
	      VALUES ('track-drop-1', 'Drop One', 'album-explo', 'Weekly Drop', 200, 1)`)
	exec(`INSERT INTO music_tracks (id, title, album_id, album_title, duration_seconds, is_explo)
	      VALUES ('track-drop-2', 'Drop Two', 'album-explo', 'Weekly Drop', 201, 1)`)
	exec(`INSERT INTO media_files (id, library_id, track_id, path, file_name, duration_seconds)
	      VALUES ('file-kept', 'music-library', 'track-kept', '/music/Radiohead/Pablo Honey/02 - Creep.mp3', '02 - Creep.mp3', 239)`)
	exec(`INSERT INTO media_files (id, library_id, track_id, path, file_name, duration_seconds)
	      VALUES ('file-twin', 'music-library', 'track-twin', '/music/explo/Weekly-Exploration/creep.mp3', 'creep.mp3', 238)`)

	seed, err := LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	albums := map[string]catalog.MusicAlbum{}
	for _, album := range seed.MusicAlbums {
		albums[album.ID] = album
	}
	if got := albums["album-mixed"]; got.TrackCount != 1 || got.DurationSeconds != 239 {
		t.Fatalf("mixed album count/duration = %d/%d, want 1/239 (the twin is not one of its tracks)", got.TrackCount, got.DurationSeconds)
	}
	if got := albums["album-mixed"]; got.IsExplo || got.HiddenFromRecentlyAdded {
		t.Fatal("a library album with a kept copy must stay a library album")
	}
	// A wholly explo album keeps the scanner's numbers: all its tracks show.
	if got := albums["album-explo"]; got.TrackCount != 2 || got.DurationSeconds != 401 || !got.IsExplo {
		t.Fatalf("explo album = %#v, want stored 2/401 and IsExplo", got)
	}

	// The projection the API serves agrees with the counts.
	service := catalog.NewService(seed)
	tracks := service.MusicTracksForAlbum("album-mixed")
	if len(tracks) != 1 || tracks[0].ID != "track-kept" {
		t.Fatalf("mixed album tracks = %#v, want only the kept copy", tracks)
	}
	if got := service.MusicTracksForAlbum("album-explo"); len(got) != 2 {
		t.Fatalf("explo album tracks = %d, want 2", len(got))
	}
}
