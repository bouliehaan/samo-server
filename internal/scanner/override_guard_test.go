package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestScannerUpsertPreservesOverriddenMusicArtistName(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	artistID := "artist-1"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_artists (id, name, sort_name)
		VALUES (?, 'Kept Name', 'Kept Sort')`, artistID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertMetadataOverride(ctx, db, catalog.OverrideKindMusicArtist, artistID, catalog.MetadataOverridePatch{
		"name":     []byte(`"User Title"`),
		"sortName": []byte(`"User Sort"`),
	}); err != nil {
		t.Fatal(err)
	}

	idx, err := catalog.LoadOverrideIndex(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db)
	scanner.overrideIndex = idx
	if err := scanner.upsertMusicArtist(ctx, catalog.MusicArtist{
		ID:       artistID,
		Name:     "Scanned Name",
		SortName: "Scanned Sort",
	}); err != nil {
		t.Fatal(err)
	}

	var name, sortName string
	if err := db.QueryRowContext(ctx, `SELECT name, sort_name FROM music_artists WHERE id = ?`, artistID).
		Scan(&name, &sortName); err != nil {
		t.Fatal(err)
	}
	if name != "Kept Name" || sortName != "Kept Sort" {
		t.Fatalf("stored artist = %q / %q, want kept source values", name, sortName)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.MusicArtists) != 1 {
		t.Fatalf("artists = %#v", seed.MusicArtists)
	}
	if seed.MusicArtists[0].Name != "User Title" || seed.MusicArtists[0].SortName != "User Sort" {
		t.Fatalf("projected artist = %#v", seed.MusicArtists[0])
	}
}

func TestScannerSkipsAlbumArtistSyncWhenOverridden(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	albumID := "album-1"
	artistID := "artist-old"
	if _, err := db.ExecContext(ctx, `INSERT INTO music_albums (id, title) VALUES (?, 'Album')`, albumID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO music_artists (id, name) VALUES (?, 'Old Artist')`, artistID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_album_artists (album_id, artist_id, position)
		VALUES (?, ?, 0)`, albumID, artistID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertMetadataOverride(ctx, db, catalog.OverrideKindMusicAlbum, albumID, catalog.MetadataOverridePatch{
		"artists": []byte(`[{"name":"Old Artist","role":"artist"}]`),
	}); err != nil {
		t.Fatal(err)
	}

	idx, err := catalog.LoadOverrideIndex(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db)
	scanner.overrideIndex = idx
	newArtist := catalog.MusicArtist{ID: "artist-new", Name: "New Artist"}
	if err := scanner.setAlbumArtists(ctx, albumID, []catalog.MusicArtist{newArtist}, true); err != nil {
		t.Fatal(err)
	}

	var linkedArtistID string
	if err := db.QueryRowContext(ctx, `
		SELECT artist_id FROM music_album_artists WHERE album_id = ?`, albumID).
		Scan(&linkedArtistID); err != nil {
		t.Fatal(err)
	}
	if linkedArtistID != artistID {
		t.Fatalf("album artist = %q, want preserved %q", linkedArtistID, artistID)
	}
}
