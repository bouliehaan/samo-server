package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

// TestGroupPodcastsFlatAndNested verifies that podcast file layouts produce
// the expected groupings — both the canonical "Show/episode.mp3" layout and
// the flat-at-root layout users sometimes have when they drop episodes
// directly into the library folder.
func TestGroupPodcastsFlatAndNested(t *testing.T) {
	root := "/podcasts"
	cases := []struct {
		name  string
		files []string
		want  []groupedAudio
	}{
		{
			name: "nested show folder",
			files: []string{
				"/podcasts/Joe Rogan/Episode 1.mp3",
				"/podcasts/Joe Rogan/Episode 2.mp3",
				"/podcasts/Tim Ferriss/Episode 1.mp3",
			},
			want: []groupedAudio{
				{Root: "/podcasts/Joe Rogan", Files: []string{"/podcasts/Joe Rogan/Episode 1.mp3", "/podcasts/Joe Rogan/Episode 2.mp3"}},
				{Root: "/podcasts/Tim Ferriss", Files: []string{"/podcasts/Tim Ferriss/Episode 1.mp3"}},
			},
		},
		{
			name: "flat episodes at root",
			files: []string{
				"/podcasts/episode 1.mp3",
				"/podcasts/episode 2.mp3",
			},
			want: []groupedAudio{
				{Root: "/podcasts", Files: []string{"/podcasts/episode 1.mp3", "/podcasts/episode 2.mp3"}},
			},
		},
		{
			name: "season subfolder",
			files: []string{
				"/podcasts/Joe Rogan/Season 1/Episode 1.mp3",
				"/podcasts/Joe Rogan/Season 1/Episode 2.mp3",
			},
			want: []groupedAudio{
				{Root: "/podcasts/Joe Rogan", Files: []string{"/podcasts/Joe Rogan/Season 1/Episode 1.mp3", "/podcasts/Joe Rogan/Season 1/Episode 2.mp3"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := groupPodcasts(root, tc.files)
			if len(got) != len(tc.want) {
				t.Fatalf("groupPodcasts produced %d groups, want %d (groups=%+v)", len(got), len(tc.want), got)
			}
			for i, group := range got {
				if group.Root != tc.want[i].Root {
					t.Errorf("group[%d].Root = %q, want %q", i, group.Root, tc.want[i].Root)
				}
				if len(group.Files) != len(tc.want[i].Files) {
					t.Errorf("group[%d] has %d files, want %d", i, len(group.Files), len(tc.want[i].Files))
				}
			}
		})
	}
}

// TestPodcastLibraryIDIsStable confirms that the library row's ID stays the
// same across re-scans even when the original ID hash doesn't match what
// LibraryID(kind, mediaType, path) would compute now. This regression bit
// migration 016 — library_ids derived from the old (shelf, book/podcast,
// path) tuple no longer match the new (audiobook/podcast, "", path) tuple,
// and recomputing in scanLibrary caused INSERT-with-new-id collisions
// against the path UNIQUE constraint.
func TestScanLibraryPreservesProvidedID(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	// Seed a library row with a manually chosen id (simulating a row that
	// migration 016 carried forward with the old shelf-derived hash).
	libraryDir := filepath.Join(t.TempDir(), "podcasts")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		"library_legacy_hash_xxx", "Podcasts", "podcast", "", libraryDir); err != nil {
		t.Fatal(err)
	}

	scanner := New(db)
	if err := scanner.scanLibrary(ctx, Library{
		ID:   "library_legacy_hash_xxx",
		Name: "Podcasts",
		Kind: "podcast",
		Path: libraryDir,
	}); err != nil {
		t.Fatalf("scanLibrary failed: %v", err)
	}

	// The row should still have the original id (not a recomputed one).
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id = ?`, "library_legacy_hash_xxx").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("library row count under original id = %d, want 1", count)
	}
	// And no row with the freshly-computed id should have been inserted.
	freshID := LibraryID("podcast", "", libraryDir)
	if freshID == "library_legacy_hash_xxx" {
		t.Skip("fresh id happens to match the legacy id; can't distinguish")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id = ?`, freshID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("library row count under recomputed id = %d, want 0 (scanLibrary should not have inserted a duplicate)", count)
	}
}

// TestRefreshStatsUpdatesLibraryAndArtistCounts verifies that calling
// RefreshStats on existing data correctly populates libraries.item_count
// and music_artists.album_count / track_count. This was the user-reported
// "0 albums and songs from Drake" / "Audiobooks Library 0 items" bug.
func TestRefreshStatsUpdatesLibraryAndArtistCounts(t *testing.T) {
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
	// Music library with one track linked to one album and one artist.
	musicLib := Library{ID: "lib_music", Name: "Music", Kind: "music", Path: "/srv/music"}
	if err := scanner.upsertLibrary(ctx, musicLib); err != nil {
		t.Fatal(err)
	}
	artist := catalog.MusicArtist{ID: "artist_drake", Name: "Drake"}
	if err := scanner.upsertMusicArtist(ctx, artist); err != nil {
		t.Fatal(err)
	}
	album := catalog.MusicAlbum{ID: "album_views", Title: "Views", DisplayArtist: "Drake"}
	if err := scanner.upsertMusicAlbum(ctx, album); err != nil {
		t.Fatal(err)
	}
	if err := scanner.setAlbumArtists(ctx, album.ID, []catalog.MusicArtist{artist}, true); err != nil {
		t.Fatal(err)
	}
	track := catalog.MusicTrack{ID: "track_hotline", Title: "Hotline Bling", AlbumID: album.ID, DurationSeconds: 267}
	if err := scanner.upsertMusicTrack(ctx, track); err != nil {
		t.Fatal(err)
	}
	if err := scanner.setTrackArtists(ctx, track.ID, []catalog.MusicArtist{artist}); err != nil {
		t.Fatal(err)
	}
	if err := scanner.upsertAudioFile(ctx, musicLib.ID, audioFileOwner{TrackID: track.ID}, catalog.AudioFile{
		ID:              "file_hotline",
		Path:            "/srv/music/Drake/Views/Hotline Bling.mp3",
		DurationSeconds: 267,
	}, "", ""); err != nil {
		t.Fatal(err)
	}
	// Audiobook library with one book.
	bookLib := Library{ID: "lib_books", Name: "Books", Kind: "audiobook", Path: "/srv/books"}
	if err := scanner.upsertLibrary(ctx, bookLib); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.upsertAudiobook(ctx, catalog.AudiobookItem{
		ID:        "book_1",
		LibraryID: bookLib.ID,
		Path:      "/srv/books/book1",
	}); err != nil {
		t.Fatal(err)
	}

	if err := scanner.RefreshStats(ctx); err != nil {
		t.Fatalf("RefreshStats failed: %v", err)
	}

	var artistAlbumCount, artistTrackCount int
	if err := db.QueryRowContext(ctx, `SELECT album_count, track_count FROM music_artists WHERE id = ?`, artist.ID).
		Scan(&artistAlbumCount, &artistTrackCount); err != nil {
		t.Fatal(err)
	}
	if artistAlbumCount != 1 {
		t.Errorf("artist album_count = %d, want 1", artistAlbumCount)
	}
	if artistTrackCount != 1 {
		t.Errorf("artist track_count = %d, want 1", artistTrackCount)
	}

	var musicLibCount int
	if err := db.QueryRowContext(ctx, `SELECT item_count FROM libraries WHERE id = ?`, musicLib.ID).Scan(&musicLibCount); err != nil {
		t.Fatal(err)
	}
	if musicLibCount != 1 {
		t.Errorf("music library item_count = %d, want 1", musicLibCount)
	}

	var bookLibCount int
	if err := db.QueryRowContext(ctx, `SELECT item_count FROM libraries WHERE id = ?`, bookLib.ID).Scan(&bookLibCount); err != nil {
		t.Fatal(err)
	}
	if bookLibCount != 1 {
		t.Errorf("audiobook library item_count = %d, want 1", bookLibCount)
	}
}
