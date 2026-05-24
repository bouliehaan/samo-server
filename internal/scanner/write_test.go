package scanner

import (
	"context"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/media"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestScannerWritesHydratableMusicRows(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	library := Library{ID: "library-1", Name: "Music", Kind: "music", Path: "/music"}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	artist := catalog.MusicArtist{ID: "artist-1", Name: "The Static"}
	if err := scanner.upsertMusicArtist(ctx, artist); err != nil {
		t.Fatal(err)
	}
	album := catalog.MusicAlbum{
		ID:            "album-1",
		Title:         "Night Broadcasts",
		DisplayArtist: "The Static",
		Compilation:   true,
		Genres:        []string{"Ambient"},
	}
	if err := scanner.upsertMusicAlbum(ctx, album); err != nil {
		t.Fatal(err)
	}
	if err := scanner.setAlbumArtists(ctx, album.ID, []catalog.MusicArtist{artist}); err != nil {
		t.Fatal(err)
	}
	track := catalog.MusicTrack{
		ID:              "track-1",
		Title:           "Signal One",
		DisplayArtist:   "The Static",
		AlbumID:         album.ID,
		AlbumTitle:      album.Title,
		DurationSeconds: 245,
		Genres:          []string{"Ambient"},
		ExternalIDs:     catalog.ExternalIDs{ISRC: "US-SAM-26-00001"},
	}
	if err := scanner.upsertMusicTrack(ctx, track); err != nil {
		t.Fatal(err)
	}
	if err := scanner.setTrackArtists(ctx, track.ID, []catalog.MusicArtist{artist}); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertAudioFile(ctx, library.ID, audioFileOwner{TrackID: track.ID}, catalog.AudioFile{
		ID:              "file-1",
		Path:            "/music/signal.flac",
		FileName:        "signal.flac",
		Container:       "flac",
		MimeType:        "audio/flac",
		Codec:           "flac",
		MetadataFormats: []string{"vorbis"},
		SampleRate:      96000,
		DurationSeconds: 245,
		EmbeddedTags:    catalog.Tags{"title": []string{"Signal One"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertGenre(ctx, string(media.KindMusic), "Ambient"); err != nil {
		t.Fatal(err)
	}

	seed, err := catalog.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed.MusicTracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(seed.MusicTracks))
	}
	if seed.MusicTracks[0].DisplayArtist != "The Static" {
		t.Fatalf("display artist = %q, want The Static", seed.MusicTracks[0].DisplayArtist)
	}
	if got := seed.MusicTracks[0].AudioFiles[0].MetadataFormats[0]; got != "vorbis" {
		t.Fatalf("metadata format = %q, want vorbis", got)
	}
}

// Regression: narrators flow into the same audiobook_contributors junction
// table as authors. Earlier code (pre-rename, with shelf_authors) only
// upserted authors into the contributor table before inserting the join
// row, which tripped the FK constraint (`insert shelf item author:
// FOREIGN KEY constraint failed`) whenever an audiobook had narrators
// that weren't also listed as authors. The fix is now baked into
// setAudiobookContributors (it upserts every contributor before linking).
func TestSetAudiobookContributorsUpsertsNarrators(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	library := Library{ID: "library-books", Name: "Books", Kind: "audiobook", Path: "/books"}
	if err := scanner.upsertLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}

	item := catalog.AudiobookItem{
		ID:        "audiobook-1",
		LibraryID: library.ID,
		Path:      "/books/the-signal",
		Book:      &catalog.BookMetadata{Title: "The Signal"},
	}
	if err := scanner.upsertAudiobook(ctx, item); err != nil {
		t.Fatal(err)
	}

	authors := []catalog.ContributorRef{
		{ID: "person-author", Name: "Ada Archive", Role: "author"},
	}
	narrators := []catalog.ContributorRef{
		{ID: "person-narrator", Name: "Vox Reader", Role: "narrator"},
	}
	if err := scanner.setAudiobookContributors(ctx, item.ID, append(authors, narrators...)); err != nil {
		t.Fatalf("setAudiobookContributors with narrator: %v", err)
	}

	var linked int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audiobook_contributors WHERE audiobook_id = ?`, item.ID,
	).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 2 {
		t.Fatalf("linked contributors = %d, want 2 (1 author + 1 narrator)", linked)
	}

	var narratorRole string
	if err := db.QueryRowContext(ctx,
		`SELECT role FROM audiobook_contributors WHERE audiobook_id = ? AND contributor_id = ?`,
		item.ID, "person-narrator",
	).Scan(&narratorRole); err != nil {
		t.Fatalf("narrator join row missing: %v", err)
	}
	if narratorRole != "narrator" {
		t.Fatalf("narrator role = %q, want narrator", narratorRole)
	}

	var narratorName string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM contributors WHERE id = ?`, "person-narrator",
	).Scan(&narratorName); err != nil {
		t.Fatalf("narrator was not upserted into contributors: %v", err)
	}
	if narratorName != "Vox Reader" {
		t.Fatalf("narrator name = %q, want Vox Reader", narratorName)
	}
}
