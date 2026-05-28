package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestDeleteMusicAlbumRemovesTracksAndFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	libraryDir := filepath.Join(root, "music")
	trackPath := filepath.Join(libraryDir, "Artist", "Album", "01-track.flac")
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	libraryID := "library_music"
	albumID := "album_test"
	trackID := "track_test"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES (?, 'Music', 'music', ?)`, libraryID, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, display_artist)
		VALUES (?, 'Album', 'Artist')`, albumID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, album_id, duration_seconds)
		VALUES (?, 'Track', ?, 120)`, trackID, albumID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name)
		VALUES ('file_test', ?, ?, ?, 'Artist/Album/01-track.flac', '01-track.flac')`,
		libraryID, trackID, trackPath); err != nil {
		t.Fatal(err)
	}

	result, err := DeleteMusicAlbum(ctx, db, albumID, DeleteOptions{DeleteFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesRemoved != 1 {
		t.Fatalf("files removed = %d, want 1", result.FilesRemoved)
	}
	if _, err := os.Stat(trackPath); !os.IsNotExist(err) {
		t.Fatalf("track file still exists: %v", err)
	}

	var albumCount, trackCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_albums WHERE id = ?`, albumID).Scan(&albumCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_tracks WHERE id = ?`, trackID).Scan(&trackCount); err != nil {
		t.Fatal(err)
	}
	if albumCount != 0 || trackCount != 0 {
		t.Fatalf("albumCount=%d trackCount=%d, want 0/0", albumCount, trackCount)
	}
}

func TestDeleteAudiobookRemovesRowAndFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	libraryDir := filepath.Join(root, "books")
	bookPath := filepath.Join(libraryDir, "Author", "Title", "book.m4b")
	if err := os.MkdirAll(filepath.Dir(bookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bookPath, []byte("book"), 0o644); err != nil {
		t.Fatal(err)
	}

	libraryID := "library_books"
	audiobookID := "audiobook_test"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES (?, 'Books', 'audiobook', ?)`, libraryID, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO audiobooks (id, library_id, path)
		VALUES (?, ?, ?)`, audiobookID, libraryID, filepath.Join("Author", "Title")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, audiobook_id, path, relative_path, file_name)
		VALUES ('file_book', ?, ?, ?, 'Author/Title/book.m4b', 'book.m4b')`,
		libraryID, audiobookID, bookPath); err != nil {
		t.Fatal(err)
	}

	if _, err := DeleteAudiobook(ctx, db, audiobookID, DeleteOptions{DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bookPath); !os.IsNotExist(err) {
		t.Fatalf("audiobook file still exists: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audiobooks WHERE id = ?`, audiobookID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("audiobook count = %d, want 0", count)
	}
}

func TestDeletePodcastShowRejectsRemoteFeed(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	podcastID := "podcast_remote"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib_feeds', 'Podcast Feeds', 'podcast', 'samo://podcast-feeds')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path)
		VALUES (?, 'lib_feeds', 'samo://show')`, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_feeds (id, podcast_id, feed_url, title)
		VALUES ('feed_1', ?, 'https://example.com/feed.xml', 'Remote Show')`, podcastID); err != nil {
		t.Fatal(err)
	}

	_, err = DeletePodcastShow(ctx, db, podcastID, DeleteOptions{DeleteFiles: true})
	if err == nil {
		t.Fatal("expected remote podcast delete to fail")
	}
	if err != ErrRemoteItem {
		t.Fatalf("err = %v, want ErrRemoteItem", err)
	}
}

func TestDeletePodcastShowRemovesFilesystemShow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	libraryDir := filepath.Join(root, "podcasts")
	episodePath := filepath.Join(libraryDir, "Show", "01.mp3")
	if err := os.MkdirAll(filepath.Dir(episodePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(episodePath, []byte("episode"), 0o644); err != nil {
		t.Fatal(err)
	}

	podcastID := "podcast_local"
	episodeID := "episode_local"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib_pod', 'Podcasts', 'podcast', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path)
		VALUES (?, 'lib_pod', 'Show')`, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (id, library_id, podcast_id, title)
		VALUES (?, 'lib_pod', ?, 'Episode 1')`, episodeID, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, podcast_id, episode_id, path, relative_path, file_name)
		VALUES ('file_ep', 'lib_pod', ?, ?, ?, 'Show/01.mp3', '01.mp3')`,
		podcastID, episodeID, episodePath); err != nil {
		t.Fatal(err)
	}

	if _, err := DeletePodcastShow(ctx, db, podcastID, DeleteOptions{DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(episodePath); !os.IsNotExist(err) {
		t.Fatalf("episode file still exists: %v", err)
	}
}

func TestDeletePodcastShowSucceedsWhenFileDeleteDenied(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	libraryDir := filepath.Join(root, "podcasts")
	showDir := filepath.Join(libraryDir, "Show")
	episodePath := filepath.Join(showDir, "01.mp3")
	if err := os.MkdirAll(showDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(episodePath, []byte("episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(showDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(showDir, 0o755) })

	podcastID := "podcast_locked"
	episodeID := "episode_locked"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path)
		VALUES ('lib_pod', 'Podcasts', 'podcast', ?)`, libraryDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcasts (id, library_id, path)
		VALUES (?, 'lib_pod', 'Show')`, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO podcast_episodes (id, library_id, podcast_id, title)
		VALUES (?, 'lib_pod', ?, 'Episode 1')`, episodeID, podcastID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media_files (id, library_id, podcast_id, episode_id, path, relative_path, file_name)
		VALUES ('file_ep', 'lib_pod', ?, ?, ?, 'Show/01.mp3', '01.mp3')`,
		podcastID, episodeID, episodePath); err != nil {
		t.Fatal(err)
	}

	result, err := DeletePodcastShow(ctx, db, podcastID, DeleteOptions{DeleteFiles: true})
	if err != nil {
		t.Fatalf("delete should succeed even when files cannot be removed: %v", err)
	}
	if len(result.FileErrors) == 0 {
		t.Fatal("expected file delete errors")
	}
	if _, err := os.Stat(episodePath); err != nil {
		t.Fatalf("episode file should still exist on disk: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM podcasts WHERE id = ?`, podcastID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("podcast count = %d, want 0", count)
	}
}
