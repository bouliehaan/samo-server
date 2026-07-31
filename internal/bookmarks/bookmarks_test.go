package bookmarks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bouliehaan/samo-server/internal/bookmarks"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// Every method in this package takes a userID, and every one of them is the
// only thing standing between one user's private data and another's. That
// invariant had no test at all, so these lean on it hard: the CRUD tests
// establish the happy path, and the isolation tests assert that the same
// operations refuse to cross a user boundary.

const (
	alice = "user-alice"
	bob   = "user-bob"
	book  = "audiobook_test"
	other = "audiobook_other"
)

// The service validates that the audiobook exists before recording anything
// against it, so the fixture seeds real rows rather than stubbing that check
// out — the FK relationships are part of what these tests are pinning.
func newService(t *testing.T) *bookmarks.Service {
	t.Helper()
	db := storagetest.Open(t)
	ctx := context.Background()

	for _, id := range []string{alice, bob} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users (id, username, role) VALUES (?, ?, ?)`,
			id, id, "user"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
		"library_test", "Books", "audiobook", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{book, other} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO audiobooks (id, library_id, path) VALUES (?, ?, ?)`,
			id, "library_test", "/books/"+id); err != nil {
			t.Fatal(err)
		}
	}
	return bookmarks.New(db, db)
}

func mustCreateBookmark(t *testing.T, svc *bookmarks.Service, userID, audiobookID string, pos int) bookmarks.Bookmark {
	t.Helper()
	bm, err := svc.CreateBookmark(context.Background(), userID, audiobookID, bookmarks.CreateBookmarkInput{
		Title:           "marker",
		Note:            "a note",
		PositionSeconds: pos,
	})
	if err != nil {
		t.Fatalf("create bookmark: %v", err)
	}
	return bm
}

func TestBookmarkRoundTrip(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	created := mustCreateBookmark(t, svc, alice, book, 120)
	if created.ID == "" || created.UserID != alice || created.PositionSeconds != 120 {
		t.Fatalf("unexpected created bookmark: %+v", created)
	}

	listed, err := svc.ListBookmarks(ctx, alice, book)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("expected the created bookmark back, got %+v", listed)
	}
}

func TestBookmarkUpdateAppliesOnlySetFields(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateBookmark(t, svc, alice, book, 120)

	newPos := 300
	updated, err := svc.UpdateBookmark(ctx, alice, created.ID, bookmarks.UpdateBookmarkInput{
		PositionSeconds: &newPos,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.PositionSeconds != 300 {
		t.Fatalf("position not applied: %d", updated.PositionSeconds)
	}
	// Title was not in the patch, so it must survive untouched.
	if updated.Title != created.Title {
		t.Fatalf("an unset field was overwritten: %q -> %q", created.Title, updated.Title)
	}
}

// The core security property: Bob must not see Alice's bookmarks.
func TestBookmarksAreScopedToTheirOwner(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	mustCreateBookmark(t, svc, alice, book, 120)

	got, err := svc.ListBookmarks(ctx, bob, book)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("bob can see alice's bookmarks: %+v", got)
	}

	all, err := svc.ListUserBookmarks(ctx, bob, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("bob's bookmark list includes alice's: %+v", all)
	}
}

func TestBookmarkUpdateRefusesAnotherUsersRow(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateBookmark(t, svc, alice, book, 120)

	newPos := 999
	_, err := svc.UpdateBookmark(ctx, bob, created.ID, bookmarks.UpdateBookmarkInput{PositionSeconds: &newPos})
	if err == nil {
		t.Fatal("bob was allowed to update alice's bookmark")
	}

	// And it really is untouched.
	after, err := svc.ListBookmarks(ctx, alice, book)
	if err != nil {
		t.Fatal(err)
	}
	if after[0].PositionSeconds != 120 {
		t.Fatalf("alice's bookmark was modified by bob: %d", after[0].PositionSeconds)
	}
}

func TestBookmarkDeleteRefusesAnotherUsersRow(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateBookmark(t, svc, alice, book, 120)

	if err := svc.DeleteBookmark(ctx, bob, created.ID); err == nil {
		t.Fatal("bob was allowed to delete alice's bookmark")
	}
	after, err := svc.ListBookmarks(ctx, alice, book)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatal("alice's bookmark was deleted by bob")
	}
}

func TestListBookmarksFiltersByAudiobook(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	mustCreateBookmark(t, svc, alice, book, 10)
	mustCreateBookmark(t, svc, alice, other, 20)

	got, err := svc.ListBookmarks(ctx, alice, book)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AudiobookID != book {
		t.Fatalf("expected only the requested audiobook's bookmarks, got %+v", got)
	}
}

func TestDeleteBookmarkRemovesIt(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	created := mustCreateBookmark(t, svc, alice, book, 120)

	if err := svc.DeleteBookmark(ctx, alice, created.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListBookmarks(ctx, alice, book)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("bookmark survived deletion: %+v", got)
	}
}

func TestNilServiceIsDisabledNotPanicking(t *testing.T) {
	var svc *bookmarks.Service
	_, err := svc.ListBookmarks(context.Background(), alice, book)
	if !errors.Is(err, bookmarks.ErrDisabled) {
		t.Fatalf("a nil service should report ErrDisabled, got %v", err)
	}
}
