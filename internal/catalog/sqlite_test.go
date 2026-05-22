package catalog

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestLoadSeedFromDBHydratesMusicAndShelf(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	exec(`INSERT INTO music_artists (id, name) VALUES ('artist-1', 'The Static')`)
	exec(`INSERT INTO libraries (id, name, kind, media_type, path) VALUES ('music-library', 'Music', 'music', '', '/music')`)
	exec(`INSERT INTO music_albums (id, title) VALUES ('album-1', 'Night Broadcasts')`)
	exec(`INSERT INTO music_album_artists (album_id, artist_id, position) VALUES ('album-1', 'artist-1', 0)`)
	exec(`INSERT INTO music_tracks (id, title, album_id, album_title, duration_seconds, genres_json, external_ids_json) VALUES ('track-1', 'Signal One', 'album-1', 'Night Broadcasts', 245, '["ambient"]', '{"isrc":"US-SAM-26-00001"}')`)
	exec(`INSERT INTO music_track_artists (track_id, artist_id, role, position) VALUES ('track-1', 'artist-1', 'artist', 0)`)
	exec(`INSERT INTO media_files (id, library_id, track_id, path, file_name, duration_seconds, sample_rate, embedded_tags_json) VALUES ('file-1', 'music-library', 'track-1', '/music/file.flac', 'file.flac', 245, 96000, '{"title":["Signal One"]}')`)
	exec(`INSERT INTO libraries (id, name, kind, media_type, path) VALUES ('library-1', 'Audiobooks', 'shelf', 'book', '/books')`)
	exec(`INSERT INTO shelf_items (id, library_id, media_type, path, duration_seconds, book_json) VALUES ('book-1', 'library-1', 'book', '/books/Ada/Signal Manual', 7200, '{"title":"Signal Manual","externalIds":{"audibleAsin":"B000SAMO"}}')`)

	seed, err := LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	if len(seed.MusicTracks) != 1 {
		t.Fatalf("music tracks = %d, want 1", len(seed.MusicTracks))
	}
	if seed.MusicTracks[0].ExternalIDs.ISRC != "US-SAM-26-00001" {
		t.Fatalf("ISRC = %q, want seeded ISRC", seed.MusicTracks[0].ExternalIDs.ISRC)
	}
	if len(seed.ShelfItems) != 1 || seed.ShelfItems[0].Book == nil {
		t.Fatalf("shelf items = %#v, want one book item", seed.ShelfItems)
	}
	if seed.ShelfItems[0].Book.ExternalIDs.AudibleASIN != "B000SAMO" {
		t.Fatalf("Audible ASIN = %q, want B000SAMO", seed.ShelfItems[0].Book.ExternalIDs.AudibleASIN)
	}
}
