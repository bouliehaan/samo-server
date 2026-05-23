package shelfuser

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestBookmarkCollectionAndSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash)
		VALUES ('user-1', 'reader', 'Reader', 'user', 'hash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('lib-1', 'Books', 'shelf', 'book', '/books')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO shelf_items (id, library_id, media_type, media_kind, path, book_json)
		VALUES ('book-1', 'lib-1', 'book', 'audiobook', '/books/one', '{"title":"One"}')`); err != nil {
		t.Fatal(err)
	}

	service := New(db)
	bookmark, err := service.CreateBookmark(ctx, "user-1", "book-1", CreateBookmarkInput{
		Title:           "Chapter 3",
		PositionSeconds: 3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	bookmarks, err := service.ListBookmarks(ctx, "user-1", "book-1")
	if err != nil || len(bookmarks) != 1 {
		t.Fatalf("bookmarks = %#v err = %v", bookmarks, err)
	}
	if bookmarks[0].ID != bookmark.ID {
		t.Fatalf("bookmark id = %q", bookmarks[0].ID)
	}

	collection, err := service.CreateCollection(ctx, "user-1", CreateCollectionInput{
		Name:    "To Finish",
		ItemIDs: []string{"book-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if collection.ItemCount != 1 {
		t.Fatalf("item count = %d", collection.ItemCount)
	}

	session, err := service.RecordSession(ctx, "user-1", RecordSessionInput{
		ItemID:               "book-1",
		StartPositionSeconds: 100,
		EndPositionSeconds:   250,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := service.ListSessionsForItem(ctx, "user-1", "book-1", 10)
	if err != nil || len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestCollectionRejectsPodcastItems(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, role, password_hash)
		VALUES ('user-1', 'reader', 'Reader', 'user', 'hash')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path)
		VALUES ('lib-1', 'Podcasts', 'shelf', 'podcast', 'samo://feeds')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO shelf_items (id, library_id, media_type, media_kind, path, podcast_json)
		VALUES ('pod-1', 'lib-1', 'podcast', 'podcast', 'samo://show', '{"title":"Show"}')`); err != nil {
		t.Fatal(err)
	}
	_, err = New(db).CreateCollection(ctx, "user-1", CreateCollectionInput{
		Name:    "Bad",
		ItemIDs: []string{"pod-1"},
	})
	if err != ErrNotAudiobook {
		t.Fatalf("err = %v, want ErrNotAudiobook", err)
	}
}
